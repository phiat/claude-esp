package watcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phiat/claude-esp/internal/parser"
)

const (
	// DefaultPollInterval is how often to check for new content
	DefaultPollInterval = 500 * time.Millisecond
	// DefaultActiveWindow is how recent a session must be modified to be considered active
	DefaultActiveWindow = 5 * time.Minute
	// ItemChannelBuffer is the buffer size for the Items channel
	ItemChannelBuffer = 100
	// ErrorChannelBuffer is the buffer size for error channels
	ErrorChannelBuffer = 10
	// AutoSkipLineThreshold is the total line count above which we auto-skip history
	// Each JSONL line is roughly one API turn; 100 lines ≈ 50 conversation exchanges
	AutoSkipLineThreshold = 100
	// KeepRecentLines is how many recent lines to show when auto-skipping
	KeepRecentLines = 10
	// CleanupInterval is how often to clean up stale file position entries
	CleanupInterval = 5 * time.Minute
	// FileReadBufferSize is the buffer size for reading files (32KB)
	FileReadBufferSize = 32 * 1024
	// ScannerInitBufferSize is the initial buffer for JSON line scanner (64KB)
	ScannerInitBufferSize = 64 * 1024
	// ScannerMaxBufferSize is the max buffer for JSON line scanner (1MB)
	ScannerMaxBufferSize = 1024 * 1024
	// AgentIDDisplayLength is how many chars of agent ID to show in display name
	AgentIDDisplayLength = 7
	// RecentActivityThreshold is how recent a session must be to show as "active" in listings
	RecentActivityThreshold = 2 * time.Minute
)

// getClaudeProjectsDir returns the path to Claude's projects directory
func getClaudeProjectsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home dir: %w", err)
	}
	return filepath.Join(homeDir, ".claude", "projects"), nil
}

// resolveProjectPath converts an encoded directory name back to a real path.
// The encoded name like "-home-user-project-name" needs smart conversion because
// directory names can contain dashes (e.g., "claude-esp-rs" should not become "claude/esp/rs").
// We try progressively from right to left to find existing paths.
func resolveProjectPath(encoded string) string {
	encoded = strings.TrimPrefix(encoded, "-")
	if encoded == "" {
		return ""
	}

	parts := strings.Split(encoded, "-")
	if len(parts) == 0 {
		return encoded
	}

	// Try progressively joining segments from the right with dashes
	// to find the actual directory name
	for joinFrom := len(parts) - 1; joinFrom >= 1; joinFrom-- {
		pathPart := strings.Join(parts[:joinFrom], "/")
		dirPart := strings.Join(parts[joinFrom:], "-")
		testPath := "/" + pathPart + "/" + dirPart

		if _, err := os.Stat(testPath); err == nil {
			return pathPart + "/" + dirPart
		}
	}

	// Fallback to naive conversion
	return strings.ReplaceAll(encoded, "-", "/")
}

// isMainSessionFile returns true if the path is a main session JSONL file
// (not a subagent file, not a directory)
func isMainSessionFile(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	if !strings.HasSuffix(path, ".jsonl") {
		return false
	}
	if strings.Contains(path, "/subagents/") {
		return false
	}
	basename := filepath.Base(path)
	if strings.HasPrefix(basename, "agent-") {
		return false
	}
	return true
}

// Session represents a Claude Code session with its files
type Session struct {
	ID              string
	ProjectPath     string
	MainFile        string
	Subagents       map[string]string          // agentID -> file path
	BackgroundTasks map[string]*BackgroundTask // toolID -> task info
	mu              sync.RWMutex               // protects Subagents and BackgroundTasks maps
}

// BackgroundTask represents a background task launched by an agent
type BackgroundTask struct {
	ToolID        string // e.g., "toolu_01XYZ..."
	ParentAgentID string // which agent spawned this (empty = main)
	ToolName      string // e.g., "Bash: npm install"
	OutputPath    string // path to tool-results file
	IsComplete    bool   // whether the task has finished
}

// NewAgentMsg signals when a new agent is discovered
type NewAgentMsg struct {
	SessionID string
	AgentID   string
}

// NewSessionMsg signals when a new session is discovered
type NewSessionMsg struct {
	SessionID   string
	ProjectPath string
}

// NewBackgroundTaskMsg signals when a new background task is discovered
type NewBackgroundTaskMsg struct {
	SessionID     string
	ParentAgentID string
	ToolID        string
	ToolName      string
	OutputPath    string
	IsComplete    bool
}

// Watcher monitors Claude session files for new content
type Watcher struct {
	claudeDir         string
	pollInterval      time.Duration
	sessions          map[string]*Session
	sessionsMu        sync.RWMutex     // protects sessions map
	filePositions     map[string]int64 // track read position per file
	filePosMu         sync.RWMutex     // protects filePositions map
	Items             chan parser.StreamItem
	Errors            chan error
	NewAgent          chan NewAgentMsg
	NewSession        chan NewSessionMsg
	NewBackgroundTask chan NewBackgroundTaskMsg
	ctx               context.Context
	cancel            context.CancelFunc
	watchActive       atomic.Bool   // if true, only watch recently modified sessions
	activeWindow      time.Duration // how recent is "active"
	skipHistory       atomic.Bool   // if true, start from end of files (live only)
}

// New creates a new watcher for active sessions
func New(sessionID string) (*Watcher, error) {
	claudeDir, err := getClaudeProjectsDir()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		claudeDir:         claudeDir,
		pollInterval:      DefaultPollInterval,
		sessions:          make(map[string]*Session),
		filePositions:     make(map[string]int64),
		Items:             make(chan parser.StreamItem, ItemChannelBuffer),
		Errors:            make(chan error, ErrorChannelBuffer),
		NewAgent:          make(chan NewAgentMsg, ErrorChannelBuffer),
		NewSession:        make(chan NewSessionMsg, ErrorChannelBuffer),
		NewBackgroundTask: make(chan NewBackgroundTaskMsg, ErrorChannelBuffer),
		ctx:               ctx,
		cancel:            cancel,
		activeWindow:      DefaultActiveWindow,
	}
	w.watchActive.Store(sessionID == "") // watch all active if no specific session

	if sessionID != "" {
		// Watch a specific session
		session, err := w.findSession(sessionID)
		if err != nil {
			return nil, err
		}
		w.sessions[session.ID] = session
	} else {
		// Find all active sessions
		if err := w.discoverActiveSessions(); err != nil {
			return nil, err
		}
	}

	return w, nil
}

// GetSessions returns a copy of all watched sessions
func (w *Watcher) GetSessions() map[string]*Session {
	w.sessionsMu.RLock()
	defer w.sessionsMu.RUnlock()

	// Return a copy to avoid race conditions
	copy := make(map[string]*Session, len(w.sessions))
	for k, v := range w.sessions {
		copy[k] = v
	}
	return copy
}

// findSession finds a specific session by ID
func (w *Watcher) findSession(sessionID string) (*Session, error) {
	var jsonlFiles []string

	err := filepath.Walk(w.claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if isMainSessionFile(path, info) {
			jsonlFiles = append(jsonlFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk claude dir: %w", err)
	}

	if len(jsonlFiles) == 0 {
		return nil, fmt.Errorf("no session files found in %s", w.claudeDir)
	}

	// Sort by modification time (most recent first)
	sort.Slice(jsonlFiles, func(i, j int) bool {
		infoI, _ := os.Stat(jsonlFiles[i])
		infoJ, _ := os.Stat(jsonlFiles[j])
		if infoI == nil || infoJ == nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	var mainFile string
	if sessionID != "" {
		// Find specific session
		for _, f := range jsonlFiles {
			if strings.Contains(f, sessionID) {
				mainFile = f
				break
			}
		}
		if mainFile == "" {
			return nil, fmt.Errorf("session %s not found", sessionID)
		}
	} else {
		mainFile = jsonlFiles[0]
	}

	return w.buildSession(mainFile)
}

func (w *Watcher) buildSession(mainFile string) (*Session, error) {
	base := filepath.Base(mainFile)
	id := strings.TrimSuffix(base, ".jsonl")

	// Extract project path from parent directory name
	projectDir := filepath.Base(filepath.Dir(mainFile))
	projectPath := resolveProjectPath(projectDir)

	session := &Session{
		ID:              id,
		ProjectPath:     projectPath,
		MainFile:        mainFile,
		Subagents:       make(map[string]string),
		BackgroundTasks: make(map[string]*BackgroundTask),
	}

	// Find subagent files
	subagentDir := filepath.Join(filepath.Dir(mainFile), id, "subagents")
	if entries, err := os.ReadDir(subagentDir); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".jsonl") {
				agentID := strings.TrimPrefix(strings.TrimSuffix(entry.Name(), ".jsonl"), "agent-")
				session.Subagents[agentID] = filepath.Join(subagentDir, entry.Name())
			}
		}
	}

	return session, nil
}

func (w *Watcher) discoverActiveSessions() error {
	now := time.Now()

	err := filepath.Walk(w.claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !isMainSessionFile(path, info) {
			return nil
		}

		// Check if recently modified
		if now.Sub(info.ModTime()) > w.activeWindow {
			return nil
		}

		session, err := w.buildSession(path)
		if err != nil {
			return nil
		}

		w.sessions[session.ID] = session
		return nil
	})

	return err
}

// SetSkipHistory configures the watcher to start from the end of files
func (w *Watcher) SetSkipHistory(skip bool) {
	w.skipHistory.Store(skip)
}

// RemoveSession removes a session from being watched
func (w *Watcher) RemoveSession(sessionID string) {
	w.sessionsMu.Lock()
	delete(w.sessions, sessionID)
	w.sessionsMu.Unlock()
}

// ToggleAutoDiscovery toggles automatic discovery of new sessions
func (w *Watcher) ToggleAutoDiscovery() {
	current := w.watchActive.Load()
	w.watchActive.Store(!current)
}

// IsAutoDiscoveryEnabled returns whether auto-discovery is enabled
func (w *Watcher) IsAutoDiscoveryEnabled() bool {
	return w.watchActive.Load()
}

// ActivityInfo contains activity status for a session/agent
type ActivityInfo struct {
	SessionID string
	AgentID   string // empty for main
	IsActive  bool
}

// GetActivityInfo returns activity status for all watched sessions and agents
// An agent is considered active if its file was modified within the given duration
func (w *Watcher) GetActivityInfo(activeWithin time.Duration) []ActivityInfo {
	var info []ActivityInfo
	now := time.Now()

	w.sessionsMu.RLock()
	defer w.sessionsMu.RUnlock()

	for _, session := range w.sessions {
		// Check main file
		if fi, err := os.Stat(session.MainFile); err == nil {
			info = append(info, ActivityInfo{
				SessionID: session.ID,
				AgentID:   "",
				IsActive:  now.Sub(fi.ModTime()) < activeWithin,
			})
		}

		// Check subagent files
		session.mu.RLock()
		for agentID, path := range session.Subagents {
			if fi, err := os.Stat(path); err == nil {
				info = append(info, ActivityInfo{
					SessionID: session.ID,
					AgentID:   agentID,
					IsActive:  now.Sub(fi.ModTime()) < activeWithin,
				})
			}
		}
		session.mu.RUnlock()
	}

	return info
}

// Start begins watching for new content
func (w *Watcher) Start() {
	go w.watchLoop()
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	w.cancel()
}

// getSessionsSnapshot returns a copy of all sessions to avoid holding lock during iteration
func (w *Watcher) getSessionsSnapshot() []*Session {
	w.sessionsMu.RLock()
	defer w.sessionsMu.RUnlock()
	sessions := make([]*Session, 0, len(w.sessions))
	for _, s := range w.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// initializeSessionReading reads or skips existing session content at startup
func (w *Watcher) initializeSessionReading(sessions []*Session) {
	shouldSkip := w.skipHistory.Load()
	if !shouldSkip {
		// Auto-skip if total line count exceeds threshold
		totalLines := w.countTotalLines(sessions)
		shouldSkip = totalLines > AutoSkipLineThreshold
	}

	if shouldSkip {
		for _, session := range sessions {
			w.skipToEndOfFiles(session)
		}
	} else {
		for _, session := range sessions {
			w.readSessionFiles(session)
		}
	}
}

// handlePollTick processes a single poll interval
func (w *Watcher) handlePollTick() {
	if w.watchActive.Load() {
		w.checkForNewSessions()
	}

	for _, session := range w.getSessionsSnapshot() {
		w.checkForNewSubagents(session)
		w.checkForBackgroundTasks(session)
		w.readSessionFiles(session)
	}
}

// checkForBackgroundTasks discovers background tasks in tool-results/ directory
func (w *Watcher) checkForBackgroundTasks(session *Session) {
	toolResultsDir := filepath.Join(filepath.Dir(session.MainFile), session.ID, "tool-results")
	entries, err := os.ReadDir(toolResultsDir)
	if err != nil {
		return // tool-results dir doesn't exist yet
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		// Extract tool ID from filename (e.g., "toolu_01XYZ.txt" -> "toolu_01XYZ")
		toolID := strings.TrimSuffix(entry.Name(), ".txt")

		// Check if we already know about this task
		session.mu.RLock()
		_, exists := session.BackgroundTasks[toolID]
		session.mu.RUnlock()

		if exists {
			continue
		}

		outputPath := filepath.Join(toolResultsDir, entry.Name())

		// Try to find which agent spawned this task by searching JSONL files
		parentAgentID, toolName := w.findBackgroundTaskParent(session, toolID)

		// Check if complete by looking for tool_result in JSONL
		isComplete := w.isBackgroundTaskComplete(session, toolID)

		task := &BackgroundTask{
			ToolID:        toolID,
			ParentAgentID: parentAgentID,
			ToolName:      toolName,
			OutputPath:    outputPath,
			IsComplete:    isComplete,
		}

		session.mu.Lock()
		session.BackgroundTasks[toolID] = task
		session.mu.Unlock()

		// Notify about new background task
		select {
		case w.NewBackgroundTask <- NewBackgroundTaskMsg{
			SessionID:     session.ID,
			ParentAgentID: parentAgentID,
			ToolID:        toolID,
			ToolName:      toolName,
			OutputPath:    outputPath,
			IsComplete:    isComplete,
		}:
		default:
		}
	}
}

// findBackgroundTaskParent searches JSONL files to find which agent spawned a tool
func (w *Watcher) findBackgroundTaskParent(session *Session, toolID string) (parentAgentID string, toolName string) {
	// Search main file first
	if name := w.findToolInFile(session.MainFile, toolID); name != "" {
		return "", name // spawned by main
	}

	// Search subagent files
	session.mu.RLock()
	defer session.mu.RUnlock()
	for agentID, path := range session.Subagents {
		if name := w.findToolInFile(path, toolID); name != "" {
			return agentID, name
		}
	}

	return "", "Background Task" // fallback name
}

// findToolInFile searches a JSONL file for a tool_use with the given ID
func (w *Watcher) findToolInFile(path string, toolID string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, ScannerInitBufferSize)
	scanner.Buffer(buf, ScannerMaxBufferSize)

	for scanner.Scan() {
		line := scanner.Text()
		// Quick check if this line might contain our tool ID
		if !strings.Contains(line, toolID) {
			continue
		}

		// Parse to extract tool name
		toolName := extractToolNameFromLine(line, toolID)
		if toolName != "" {
			return toolName
		}
	}

	return ""
}

// extractToolNameFromLine extracts tool name from a JSONL line containing the tool ID
func extractToolNameFromLine(line string, toolID string) string {
	// Simple extraction: look for "name":"<toolname>" near the tool ID
	// This is a simplified approach - could use full JSON parsing if needed

	// Find tool_use block with this ID
	if !strings.Contains(line, `"type":"tool_use"`) && !strings.Contains(line, `"type": "tool_use"`) {
		return ""
	}

	// Look for name field - try common patterns
	patterns := []string{`"name":"`, `"name": "`}
	for _, pattern := range patterns {
		idx := strings.Index(line, pattern)
		if idx == -1 {
			continue
		}
		start := idx + len(pattern)
		end := strings.Index(line[start:], `"`)
		if end > 0 {
			name := line[start : start+end]
			// Try to get a command/pattern for context
			return formatToolName(name, line)
		}
	}

	return ""
}

// formatToolName creates a display name like "Bash: npm install"
func formatToolName(toolName string, line string) string {
	// For Bash, try to extract the command
	if toolName == "Bash" {
		if cmd := extractField(line, "command"); cmd != "" {
			if len(cmd) > 30 {
				cmd = cmd[:30] + "..."
			}
			return "Bash: " + cmd
		}
	}

	// For Task, try to get description
	if toolName == "Task" {
		if desc := extractField(line, "description"); desc != "" {
			if len(desc) > 30 {
				desc = desc[:30] + "..."
			}
			return "Task: " + desc
		}
	}

	return toolName
}

// extractField extracts a JSON field value (simple string extraction)
func extractField(line string, field string) string {
	patterns := []string{`"` + field + `":"`, `"` + field + `": "`}
	for _, pattern := range patterns {
		idx := strings.Index(line, pattern)
		if idx == -1 {
			continue
		}
		start := idx + len(pattern)
		// Find the end quote, handling escaped quotes
		end := start
		for end < len(line) {
			if line[end] == '"' && (end == start || line[end-1] != '\\') {
				break
			}
			end++
		}
		if end > start {
			return line[start:end]
		}
	}
	return ""
}

// isBackgroundTaskComplete checks if a tool_result exists for the given tool ID
func (w *Watcher) isBackgroundTaskComplete(session *Session, toolID string) bool {
	// Check main file
	if w.fileContainsToolResult(session.MainFile, toolID) {
		return true
	}

	// Check subagent files
	session.mu.RLock()
	defer session.mu.RUnlock()
	for _, path := range session.Subagents {
		if w.fileContainsToolResult(path, toolID) {
			return true
		}
	}

	return false
}

// fileContainsToolResult checks if a file contains a tool_result for the given tool ID
func (w *Watcher) fileContainsToolResult(path string, toolID string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, ScannerInitBufferSize)
	scanner.Buffer(buf, ScannerMaxBufferSize)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, toolID) && strings.Contains(line, `"tool_result"`) {
			return true
		}
	}

	return false
}

func (w *Watcher) watchLoop() {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(CleanupInterval)
	defer cleanupTicker.Stop()

	w.initializeSessionReading(w.getSessionsSnapshot())

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-cleanupTicker.C:
			w.cleanupFilePositions()
		case <-ticker.C:
			w.handlePollTick()
		}
	}
}

func (w *Watcher) checkForNewSessions() {
	now := time.Now()

	filepath.Walk(w.claudeDir, func(path string, info os.FileInfo, err error) error {
		// Check for context cancellation to avoid goroutine leak
		select {
		case <-w.ctx.Done():
			return filepath.SkipAll
		default:
		}

		if err != nil {
			return nil
		}
		if !isMainSessionFile(path, info) {
			return nil
		}

		// Check if recently modified
		if now.Sub(info.ModTime()) > w.activeWindow {
			return nil
		}

		basename := filepath.Base(path)
		id := strings.TrimSuffix(basename, ".jsonl")

		// Check and add with write lock to avoid TOCTOU race
		w.sessionsMu.Lock()
		_, exists := w.sessions[id]
		if exists {
			w.sessionsMu.Unlock()
			return nil
		}

		session, err := w.buildSession(path)
		if err != nil {
			w.sessionsMu.Unlock()
			return nil
		}

		w.sessions[session.ID] = session
		w.sessionsMu.Unlock()

		// Notify about new session
		select {
		case w.NewSession <- NewSessionMsg{SessionID: session.ID, ProjectPath: session.ProjectPath}:
		default:
		}
		return nil
	})
}

func (w *Watcher) checkForNewSubagents(session *Session) {
	subagentDir := filepath.Join(filepath.Dir(session.MainFile), session.ID, "subagents")
	entries, err := os.ReadDir(subagentDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			agentID := strings.TrimPrefix(strings.TrimSuffix(entry.Name(), ".jsonl"), "agent-")
			path := filepath.Join(subagentDir, entry.Name())

			// Check and add with write lock to avoid TOCTOU race
			session.mu.Lock()
			_, exists := session.Subagents[agentID]
			if exists {
				session.mu.Unlock()
				continue
			}
			session.Subagents[agentID] = path
			session.mu.Unlock()

			select {
			case w.NewAgent <- NewAgentMsg{SessionID: session.ID, AgentID: agentID}:
			default:
			}
		}
	}
}

func (w *Watcher) countTotalLines(sessions []*Session) int {
	var total int
	for _, session := range sessions {
		total += countFileLines(session.MainFile)
		session.mu.RLock()
		for _, path := range session.Subagents {
			total += countFileLines(path)
		}
		session.mu.RUnlock()
	}
	return total
}

// countFileLines counts newlines in a file without parsing content
func countFileLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	count := 0
	buf := make([]byte, FileReadBufferSize)
	for {
		n, err := file.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i] == '\n' {
				count++
			}
		}
		if err != nil {
			break
		}
	}
	return count
}

func (w *Watcher) skipToEndOfFiles(session *Session) {
	// Set position to near end of main file, keeping last N lines
	mainPos := findPositionForLastNLines(session.MainFile, KeepRecentLines)

	// Get subagent positions
	session.mu.RLock()
	subagentPaths := make([]string, 0, len(session.Subagents))
	for _, path := range session.Subagents {
		subagentPaths = append(subagentPaths, path)
	}
	session.mu.RUnlock()

	subagentPositions := make(map[string]int64, len(subagentPaths))
	for _, path := range subagentPaths {
		subagentPositions[path] = findPositionForLastNLines(path, KeepRecentLines)
	}

	// Write all positions under lock
	w.filePosMu.Lock()
	w.filePositions[session.MainFile] = mainPos
	for path, pos := range subagentPositions {
		w.filePositions[path] = pos
	}
	w.filePosMu.Unlock()
}

// findPositionForLastNLines returns the byte offset to start reading the last N lines
func findPositionForLastNLines(path string, n int) int64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	// Collect positions of all newlines
	var newlinePositions []int64
	var pos int64
	buf := make([]byte, FileReadBufferSize)
	for {
		bytesRead, err := file.Read(buf)
		for i := 0; i < bytesRead; i++ {
			if buf[i] == '\n' {
				newlinePositions = append(newlinePositions, pos+int64(i)+1)
			}
		}
		pos += int64(bytesRead)
		if err != nil {
			break
		}
	}

	// If fewer than N lines, start from beginning
	if len(newlinePositions) <= n {
		return 0
	}

	// Return position after the newline that's N lines from the end
	return newlinePositions[len(newlinePositions)-n]
}

func (w *Watcher) readSessionFiles(session *Session) {
	// Read main file
	w.readFile(session.MainFile, session.ID, "")

	// Get snapshot of subagents to avoid holding lock during file reads
	session.mu.RLock()
	subagents := make(map[string]string, len(session.Subagents))
	for k, v := range session.Subagents {
		subagents[k] = v
	}
	session.mu.RUnlock()

	// Read subagent files
	for agentID, path := range subagents {
		w.readFile(path, session.ID, agentID)
	}
}

func (w *Watcher) readFile(path string, sessionID string, agentID string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	// Seek to last known position
	w.filePosMu.RLock()
	pos, exists := w.filePositions[path]
	w.filePosMu.RUnlock()
	if exists {
		file.Seek(pos, 0)
	}

	scanner := bufio.NewScanner(file)
	// Increase buffer size for large JSON lines
	buf := make([]byte, 0, ScannerInitBufferSize)
	scanner.Buffer(buf, ScannerMaxBufferSize)

	for scanner.Scan() {
		line := scanner.Text()
		items, err := parser.ParseLine(line)
		if err != nil {
			select {
			case w.Errors <- err:
			default:
			}
			continue
		}

		for _, item := range items {
			// Set session ID
			item.SessionID = sessionID

			// Set agent ID from context if not already set
			if agentID != "" && item.AgentID == "" {
				item.AgentID = agentID
				item.AgentName = fmt.Sprintf("Agent-%s", agentID[:min(AgentIDDisplayLength, len(agentID))])
			}

			select {
			case w.Items <- item:
			case <-w.ctx.Done():
				return
			}
		}
	}

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		select {
		case w.Errors <- fmt.Errorf("scanner error reading %s: %w", path, err):
		default:
		}
	}

	// Update position
	newPos, _ := file.Seek(0, 1)
	w.filePosMu.Lock()
	w.filePositions[path] = newPos
	w.filePosMu.Unlock()
}

// cleanupFilePositions removes entries for files that no longer exist
func (w *Watcher) cleanupFilePositions() {
	w.filePosMu.Lock()
	defer w.filePosMu.Unlock()
	for path := range w.filePositions {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			delete(w.filePositions, path)
		}
	}
}

// ListSessions returns recent sessions
func ListSessions(limit int) ([]SessionInfo, error) {
	return listSessionsFiltered(limit, 0)
}

// ListActiveSessions returns sessions modified within the given duration
func ListActiveSessions(within time.Duration) ([]SessionInfo, error) {
	return listSessionsFiltered(0, within)
}

func listSessionsFiltered(limit int, activeWithin time.Duration) ([]SessionInfo, error) {
	claudeDir, err := getClaudeProjectsDir()
	if err != nil {
		return nil, err
	}

	var sessions []SessionInfo
	now := time.Now()

	err = filepath.Walk(claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !isMainSessionFile(path, info) {
			return nil
		}

		// If filtering by active time, skip old sessions
		if activeWithin > 0 && now.Sub(info.ModTime()) > activeWithin {
			return nil
		}

		// Extract project path from parent directory name
		basename := filepath.Base(path)
		projectDir := filepath.Base(filepath.Dir(path))
		projectPath := resolveProjectPath(projectDir)

		sessions = append(sessions, SessionInfo{
			ID:          strings.TrimSuffix(basename, ".jsonl"),
			Path:        path,
			ProjectPath: projectPath,
			Modified:    info.ModTime(),
			IsActive:    now.Sub(info.ModTime()) < RecentActivityThreshold,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by modification time
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})

	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	return sessions, nil
}

// SessionInfo contains basic info about a session
type SessionInfo struct {
	ID          string
	Path        string
	ProjectPath string
	Modified    time.Time
	IsActive    bool
}
