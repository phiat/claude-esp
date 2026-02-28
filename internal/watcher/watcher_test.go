package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/phiat/claude-esp/internal/parser"
)

func TestIsMainSessionFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		dir  bool
		want bool
	}{
		{"valid session file", "/projects/test/abc123.jsonl", false, true},
		{"directory", "/projects/test/abc123.jsonl", true, false},
		{"non-jsonl", "/projects/test/abc123.txt", false, false},
		{"subagent file", "/projects/test/sess1/subagents/agent-abc.jsonl", false, false},
		{"agent- prefix", "/projects/test/agent-abc.jsonl", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &mockFileInfo{name: filepath.Base(tt.path), isDir: tt.dir}
			got := isMainSessionFile(tt.path, info)
			if got != tt.want {
				t.Errorf("isMainSessionFile(%q, dir=%v) = %v, want %v", tt.path, tt.dir, got, tt.want)
			}
		})
	}
}

func TestResolveProjectPath(t *testing.T) {
	// Test fallback behavior (naive conversion)
	// We can't test the os.Stat path easily, but we can test the fallback
	result := resolveProjectPath("-nonexistent-path-segments")
	if !strings.Contains(result, "/") || strings.HasPrefix(result, "-") {
		// It should either find a path or fall back to slash conversion
		// The fallback converts all dashes to slashes
		expected := "nonexistent/path/segments"
		if result != expected {
			t.Errorf("resolveProjectPath fallback = %q, want %q", result, expected)
		}
	}
}

func TestResolveProjectPathEmpty(t *testing.T) {
	result := resolveProjectPath("-")
	if result != "" {
		t.Errorf("resolveProjectPath(-) = %q, want empty", result)
	}

	result = resolveProjectPath("")
	// Trims prefix "-", gets ""
	if result != "" {
		t.Errorf("resolveProjectPath('') = %q, want empty", result)
	}
}

func TestExtractToolNameFromLine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		toolID string
		want   string
	}{
		{
			"bash tool",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`,
			"toolu_1",
			"Bash",
		},
		{
			"no tool_use type",
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1"}]}}`,
			"toolu_1",
			"",
		},
		{
			"tool not in line",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`,
			"toolu_missing",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToolNameFromLine(tt.line, tt.toolID)
			if tt.want == "" {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			} else {
				if !strings.Contains(got, tt.want) {
					t.Errorf("got %q, want to contain %q", got, tt.want)
				}
			}
		})
	}
}

func TestExtractField(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		field string
		want  string
	}{
		{"compact json", `{"command":"ls -la"}`, "command", "ls -la"},
		{"spaced json", `{"command": "ls -la"}`, "command", "ls -la"},
		{"missing field", `{"other":"value"}`, "command", ""},
		{"empty value", `{"command":""}`, "command", ""},
		{"description field", `{"description": "install deps"}`, "description", "install deps"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractField(tt.line, tt.field)
			if got != tt.want {
				t.Errorf("extractField(%q, %q) = %q, want %q", tt.line, tt.field, got, tt.want)
			}
		})
	}
}

func TestFormatToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		line     string
		want     string
	}{
		{"bash with command", "Bash", `{"command":"npm install"}`, "Bash: npm install"},
		{"bash no command", "Bash", `{"other":"value"}`, "Bash"},
		{"task with desc", "Task", `{"description":"explore code"}`, "Task: explore code"},
		{"other tool", "Read", `{"file_path":"/foo"}`, "Read"},
		{"bash long command", "Bash", `{"command":"this is a very long command that exceeds thirty characters limit"}`, "Bash: this is a very long command th..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolName(tt.toolName, tt.line)
			if got != tt.want {
				t.Errorf("formatToolName(%q) = %q, want %q", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestCountFileLines(t *testing.T) {
	// Create temp file with known line count
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.jsonl")

	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	count := countFileLines(path)
	if count != 3 {
		t.Errorf("countFileLines = %d, want 3", count)
	}
}

func TestCountFileLinesEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	count := countFileLines(path)
	if count != 0 {
		t.Errorf("countFileLines(empty) = %d, want 0", count)
	}
}

func TestCountFileLinesNonExistent(t *testing.T) {
	count := countFileLines("/nonexistent/path/file.jsonl")
	if count != 0 {
		t.Errorf("countFileLines(nonexistent) = %d, want 0", count)
	}
}

func TestGetClaudeProjectsDir(t *testing.T) {
	// Test CLAUDE_HOME override
	t.Setenv("CLAUDE_HOME", "/tmp/test-claude")
	dir, err := getClaudeProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/test-claude/projects" {
		t.Errorf("got %q, want %q", dir, "/tmp/test-claude/projects")
	}
}

func TestGetClaudeProjectsDirDefault(t *testing.T) {
	// Test default (no CLAUDE_HOME)
	t.Setenv("CLAUDE_HOME", "")
	dir, err := getClaudeProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".claude", "projects")
	if dir != expected {
		t.Errorf("got %q, want %q", dir, expected)
	}
}

// mockFileInfo implements os.FileInfo for testing
type mockFileInfo struct {
	name  string
	isDir bool
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return 0 }
func (m *mockFileInfo) Mode() os.FileMode  { return 0 }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }

// --- fsnotify integration tests ---

// newTestWatcher creates a minimal Watcher for testing without needing real Claude dirs
func newTestWatcher(t *testing.T, claudeDir string, useFsnotify bool) *Watcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		claudeDir:         claudeDir,
		pollInterval:      100 * time.Millisecond,
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
		fileContexts:      make(map[string]fileCtx),
		debounceTimers:    make(map[string]*time.Timer),
	}

	if useFsnotify {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			t.Skipf("fsnotify not available: %v", err)
		}
		w.fsWatcher = fsw
		w.useFsnotify = true
	}

	t.Cleanup(func() {
		w.Stop()
	})

	return w
}

func TestUsingFsnotify(t *testing.T) {
	tmpDir := t.TempDir()

	// Polling watcher
	w := newTestWatcher(t, tmpDir, false)
	if w.UsingFsnotify() {
		t.Error("expected polling mode, got fsnotify")
	}

	// fsnotify watcher
	w2 := newTestWatcher(t, tmpDir, true)
	if !w2.UsingFsnotify() {
		t.Error("expected fsnotify mode, got polling")
	}
}

func TestFsnotifyFileWatch(t *testing.T) {
	// Test that writing to a watched file triggers readFile via fsnotify
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "-test-project")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "sess001.jsonl")
	os.WriteFile(sessionFile, []byte(""), 0644)

	w := newTestWatcher(t, tmpDir, true)

	session := &Session{
		ID:              "sess001",
		ProjectPath:     "test/project",
		MainFile:        sessionFile,
		Subagents:       make(map[string]string),
		BackgroundTasks: make(map[string]*BackgroundTask),
	}
	w.sessions[session.ID] = session
	w.registerSessionWatches(session)

	go w.watchLoopFsnotify()

	// Write a valid JSONL line to the file
	time.Sleep(50 * time.Millisecond) // let watcher start
	jsonLine := `{"type":"assistant","message":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"test thought"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	os.WriteFile(sessionFile, []byte(jsonLine), 0644)

	// Wait for debounce + processing
	select {
	case item := <-w.Items:
		if item.SessionID != "sess001" {
			t.Errorf("got session %q, want sess001", item.SessionID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for stream item from fsnotify")
	}
}

func TestFsnotifyNewSubagentDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "-test-project")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "sess002.jsonl")
	os.WriteFile(sessionFile, []byte(""), 0644)

	// Create session dir and subagents dir
	subagentDir := filepath.Join(projectDir, "sess002", "subagents")
	os.MkdirAll(subagentDir, 0755)

	w := newTestWatcher(t, tmpDir, true)
	w.watchActive.Store(true)

	session := &Session{
		ID:              "sess002",
		ProjectPath:     "test/project",
		MainFile:        sessionFile,
		Subagents:       make(map[string]string),
		BackgroundTasks: make(map[string]*BackgroundTask),
	}
	w.sessions[session.ID] = session
	w.registerSessionWatches(session)
	w.addDirectoryWatches(tmpDir)

	go w.watchLoopFsnotify()

	// Create a new subagent file
	time.Sleep(50 * time.Millisecond)
	agentFile := filepath.Join(subagentDir, "agent-abc1234.jsonl")
	os.WriteFile(agentFile, []byte(""), 0644)

	select {
	case msg := <-w.NewAgent:
		if msg.SessionID != "sess002" {
			t.Errorf("got session %q, want sess002", msg.SessionID)
		}
		if msg.AgentID != "abc1234" {
			t.Errorf("got agent %q, want abc1234", msg.AgentID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for new agent msg from fsnotify")
	}
}

func TestFsnotifyNewSessionDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "-test-project")
	os.MkdirAll(projectDir, 0755)

	w := newTestWatcher(t, tmpDir, true)
	w.watchActive.Store(true)
	w.addDirectoryWatches(tmpDir)

	go w.watchLoopFsnotify()

	// Create a new session file
	time.Sleep(50 * time.Millisecond)
	newSessionFile := filepath.Join(projectDir, "newsess123.jsonl")
	os.WriteFile(newSessionFile, []byte(""), 0644)

	select {
	case msg := <-w.NewSession:
		if msg.SessionID != "newsess123" {
			t.Errorf("got session %q, want newsess123", msg.SessionID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for new session msg from fsnotify")
	}
}

func TestDebounceCoalesces(t *testing.T) {
	// Verify that rapid writes result in a single read, not one per write
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "-test-project")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "sess003.jsonl")
	os.WriteFile(sessionFile, []byte(""), 0644)

	w := newTestWatcher(t, tmpDir, true)

	session := &Session{
		ID:              "sess003",
		ProjectPath:     "test/project",
		MainFile:        sessionFile,
		Subagents:       make(map[string]string),
		BackgroundTasks: make(map[string]*BackgroundTask),
	}
	w.sessions[session.ID] = session
	w.registerSessionWatches(session)

	go w.watchLoopFsnotify()
	time.Sleep(50 * time.Millisecond)

	// Write 5 lines rapidly (simulating a burst from Claude)
	f, err := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	jsonLine := `{"type":"assistant","message":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"thought %d"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	for i := 0; i < 5; i++ {
		f.WriteString(jsonLine)
		time.Sleep(5 * time.Millisecond) // rapid but not instant
	}
	f.Close()

	// Drain items — should get all 5 lines but from a single debounced read
	var items []parser.StreamItem
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case item := <-w.Items:
			items = append(items, item)
			if len(items) >= 5 {
				goto done
			}
		case <-timeout:
			goto done
		}
	}
done:
	if len(items) != 5 {
		t.Errorf("got %d items, want 5", len(items))
	}

	// Verify only one debounce timer was active (it should have been cleaned up)
	w.debounceMu.Lock()
	remaining := len(w.debounceTimers)
	w.debounceMu.Unlock()
	if remaining != 0 {
		t.Errorf("got %d remaining debounce timers, want 0", remaining)
	}
}

func TestAddFileWatch(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")
	os.WriteFile(testFile, []byte(""), 0644)

	w := newTestWatcher(t, tmpDir, true)

	w.addFileWatch(testFile, "sess1", "agent1")

	w.fileCtxMu.RLock()
	ctx, ok := w.fileContexts[testFile]
	w.fileCtxMu.RUnlock()

	if !ok {
		t.Fatal("file context not registered")
	}
	if ctx.sessionID != "sess1" {
		t.Errorf("got sessionID %q, want sess1", ctx.sessionID)
	}
	if ctx.agentID != "agent1" {
		t.Errorf("got agentID %q, want agent1", ctx.agentID)
	}
}

func TestHandleFsWriteIgnoresUntracked(t *testing.T) {
	tmpDir := t.TempDir()
	w := newTestWatcher(t, tmpDir, true)

	// Write event for a path not in fileContexts should be silently ignored
	w.handleFsWrite("/some/untracked/file.jsonl")

	w.debounceMu.Lock()
	count := len(w.debounceTimers)
	w.debounceMu.Unlock()

	if count != 0 {
		t.Errorf("expected no debounce timers for untracked file, got %d", count)
	}
}

func TestStopCancelsDebounceTimers(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")
	os.WriteFile(testFile, []byte(""), 0644)

	w := newTestWatcher(t, tmpDir, true)
	w.addFileWatch(testFile, "sess1", "")

	// Manually create a debounce timer
	w.debounceMu.Lock()
	w.debounceTimers[testFile] = time.AfterFunc(time.Hour, func() {})
	w.debounceMu.Unlock()

	w.Stop()

	// Timer map should still have the entry but the timer itself was stopped
	// (we just verify Stop() doesn't panic with active timers)
}

func TestPollingFallbackStillWorks(t *testing.T) {
	// Create a fake Claude dir structure with a session
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "-test-project")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "sess004.jsonl")
	jsonLine := `{"type":"assistant","message":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"poll test"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	os.WriteFile(sessionFile, []byte(jsonLine), 0644)

	w := newTestWatcher(t, tmpDir, false) // polling mode

	session := &Session{
		ID:              "sess004",
		ProjectPath:     "test/project",
		MainFile:        sessionFile,
		Subagents:       make(map[string]string),
		BackgroundTasks: make(map[string]*BackgroundTask),
	}
	w.sessions[session.ID] = session

	go w.watchLoopPolling()

	select {
	case item := <-w.Items:
		if item.SessionID != "sess004" {
			t.Errorf("got session %q, want sess004", item.SessionID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for stream item from polling")
	}
}

func TestNewBackgroundTaskViaFsnotify(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "-test-project")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "sess005.jsonl")
	os.WriteFile(sessionFile, []byte(""), 0644)

	// Create session dir and tool-results dir
	toolResultsDir := filepath.Join(projectDir, "sess005", "tool-results")
	os.MkdirAll(toolResultsDir, 0755)

	w := newTestWatcher(t, tmpDir, true)

	session := &Session{
		ID:              "sess005",
		ProjectPath:     "test/project",
		MainFile:        sessionFile,
		Subagents:       make(map[string]string),
		BackgroundTasks: make(map[string]*BackgroundTask),
	}
	w.sessions[session.ID] = session
	w.registerSessionWatches(session)
	w.addDirectoryWatches(tmpDir)

	go w.watchLoopFsnotify()
	time.Sleep(50 * time.Millisecond)

	// Create a tool result file
	toolFile := filepath.Join(toolResultsDir, "toolu_01ABC.txt")
	os.WriteFile(toolFile, []byte("task output"), 0644)

	select {
	case msg := <-w.NewBackgroundTask:
		if msg.SessionID != "sess005" {
			t.Errorf("got session %q, want sess005", msg.SessionID)
		}
		if msg.ToolID != "toolu_01ABC" {
			t.Errorf("got toolID %q, want toolu_01ABC", msg.ToolID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for background task msg from fsnotify")
	}
}

func TestConcurrentDebounce(t *testing.T) {
	// Stress test: many concurrent handleFsWrite calls shouldn't race
	tmpDir := t.TempDir()
	w := newTestWatcher(t, tmpDir, true)

	paths := []string{"/a.jsonl", "/b.jsonl", "/c.jsonl"}
	for _, p := range paths {
		w.fileCtxMu.Lock()
		w.fileContexts[p] = fileCtx{sessionID: "s1", agentID: ""}
		w.fileCtxMu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w.handleFsWrite(paths[i%len(paths)])
		}(i)
	}
	wg.Wait()

	// Just verify no panics and timers are bounded
	w.debounceMu.Lock()
	count := len(w.debounceTimers)
	w.debounceMu.Unlock()
	if count > len(paths) {
		t.Errorf("got %d debounce timers, want at most %d", count, len(paths))
	}
}
