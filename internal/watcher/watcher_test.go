package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
