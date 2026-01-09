package watcher

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phiat/claude-esp/internal/parser"
)

// Session represents a Claude Code session with its files
type Session struct {
	ID          string
	ProjectPath string
	MainFile    string
	Subagents   map[string]string // agentID -> file path
	mu          sync.RWMutex      // protects Subagents map
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

// Watcher monitors Claude session files for new content
type Watcher struct {
	claudeDir      string
	pollInterval   time.Duration
	sessions       map[string]*Session
	sessionsMu     sync.RWMutex      // protects sessions map
	filePositions  map[string]int64  // track read position per file
	Items          chan parser.StreamItem
	Errors         chan error
	NewAgent       chan NewAgentMsg
	NewSession     chan NewSessionMsg
	stopCh         chan struct{}
	watchActive    bool          // if true, only watch recently modified sessions
	activeWindow   time.Duration // how recent is "active"
}

// New creates a new watcher for active sessions
func New(sessionID string) (*Watcher, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	claudeDir := filepath.Join(homeDir, ".claude", "projects")

	w := &Watcher{
		claudeDir:     claudeDir,
		pollInterval:  500 * time.Millisecond,
		sessions:      make(map[string]*Session),
		filePositions: make(map[string]int64),
		Items:         make(chan parser.StreamItem, 100),
		Errors:        make(chan error, 10),
		NewAgent:      make(chan NewAgentMsg, 10),
		NewSession:    make(chan NewSessionMsg, 10),
		stopCh:        make(chan struct{}),
		watchActive:   sessionID == "", // watch all active if no specific session
		activeWindow:  5 * time.Minute,
	}

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
		basename := filepath.Base(path)
		if !info.IsDir() &&
			strings.HasSuffix(path, ".jsonl") &&
			!strings.Contains(path, "/subagents/") &&
			!strings.HasPrefix(basename, "agent-") {
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
	projectPath := strings.ReplaceAll(projectDir, "-", "/")
	if strings.HasPrefix(projectPath, "/") {
		projectPath = projectPath[1:]
	}

	session := &Session{
		ID:          id,
		ProjectPath: projectPath,
		MainFile:    mainFile,
		Subagents:   make(map[string]string),
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
		basename := filepath.Base(path)
		if info.IsDir() ||
			!strings.HasSuffix(path, ".jsonl") ||
			strings.Contains(path, "/subagents/") ||
			strings.HasPrefix(basename, "agent-") {
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

// Start begins watching for new content
func (w *Watcher) Start() {
	go w.watchLoop()
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopCh)
}

func (w *Watcher) watchLoop() {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Initial read of all sessions
	w.sessionsMu.RLock()
	sessions := make([]*Session, 0, len(w.sessions))
	for _, s := range w.sessions {
		sessions = append(sessions, s)
	}
	w.sessionsMu.RUnlock()

	for _, session := range sessions {
		w.readSessionFiles(session)
	}

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			// Check for new active sessions if watching all
			if w.watchActive {
				w.checkForNewSessions()
			}

			// Get snapshot of sessions to avoid holding lock during iteration
			w.sessionsMu.RLock()
			sessions := make([]*Session, 0, len(w.sessions))
			for _, s := range w.sessions {
				sessions = append(sessions, s)
			}
			w.sessionsMu.RUnlock()

			// Check for new subagents and read updates
			for _, session := range sessions {
				w.checkForNewSubagents(session)
				w.readSessionFiles(session)
			}
		}
	}
}

func (w *Watcher) checkForNewSessions() {
	now := time.Now()

	filepath.Walk(w.claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		basename := filepath.Base(path)
		if info.IsDir() ||
			!strings.HasSuffix(path, ".jsonl") ||
			strings.Contains(path, "/subagents/") ||
			strings.HasPrefix(basename, "agent-") {
			return nil
		}

		// Check if recently modified
		if now.Sub(info.ModTime()) > w.activeWindow {
			return nil
		}

		id := strings.TrimSuffix(basename, ".jsonl")

		// Check if session exists (read lock)
		w.sessionsMu.RLock()
		_, exists := w.sessions[id]
		w.sessionsMu.RUnlock()

		if !exists {
			session, err := w.buildSession(path)
			if err != nil {
				return nil
			}

			// Add new session (write lock)
			w.sessionsMu.Lock()
			w.sessions[session.ID] = session
			w.sessionsMu.Unlock()

			// Notify about new session
			select {
			case w.NewSession <- NewSessionMsg{SessionID: session.ID, ProjectPath: session.ProjectPath}:
			default:
			}
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

			// Check if agent exists (read lock)
			session.mu.RLock()
			_, exists := session.Subagents[agentID]
			session.mu.RUnlock()

			if !exists {
				path := filepath.Join(subagentDir, entry.Name())

				// Add new agent (write lock)
				session.mu.Lock()
				session.Subagents[agentID] = path
				session.mu.Unlock()

				select {
				case w.NewAgent <- NewAgentMsg{SessionID: session.ID, AgentID: agentID}:
				default:
				}
			}
		}
	}
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
	pos, exists := w.filePositions[path]
	if exists {
		file.Seek(pos, 0)
	}

	scanner := bufio.NewScanner(file)
	// Increase buffer size for large JSON lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

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
				item.AgentName = fmt.Sprintf("Agent-%s", agentID[:min(7, len(agentID))])
			}

			select {
			case w.Items <- item:
			case <-w.stopCh:
				return
			}
		}
	}

	// Update position
	newPos, _ := file.Seek(0, 1)
	w.filePositions[path] = newPos
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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	claudeDir := filepath.Join(homeDir, ".claude", "projects")
	var sessions []SessionInfo
	now := time.Now()

	err = filepath.Walk(claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		basename := filepath.Base(path)
		// Skip directories, non-jsonl files, subagent files, and agent-prefixed files
		if info.IsDir() ||
			!strings.HasSuffix(path, ".jsonl") ||
			strings.Contains(path, "/subagents/") ||
			strings.HasPrefix(basename, "agent-") {
			return nil
		}

		// If filtering by active time, skip old sessions
		if activeWithin > 0 && now.Sub(info.ModTime()) > activeWithin {
			return nil
		}

		// Extract project path from parent directory name
		projectDir := filepath.Base(filepath.Dir(path))
		projectPath := strings.ReplaceAll(projectDir, "-", "/")
		if strings.HasPrefix(projectPath, "/") {
			projectPath = projectPath[1:] // remove leading slash
		}

		sessions = append(sessions, SessionInfo{
			ID:          strings.TrimSuffix(basename, ".jsonl"),
			Path:        path,
			ProjectPath: projectPath,
			Modified:    info.ModTime(),
			IsActive:    now.Sub(info.ModTime()) < 2*time.Minute,
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
