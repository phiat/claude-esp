// TUI integration tests for claude-esp using tui-use + claude -p.
//
// One claude -p call seeds all content, then tests run against it.
// Runtime: ~30-40s (seed) + ~20s (tests).
//
// Prerequisites: tui-use, claude-esp binary, claude CLI (authenticated)
//
// Run:
//
//	go test -v -tags=tui -timeout 120s ./tests/
//
//go:build tui

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// ── config ──────────────────────────────────────────────────────────────

var (
	binary   string
	model    string
	workDir  string
	fakeHome string

	seedOnce sync.Once
	seedErr  error
)

func TestMain(m *testing.M) {
	// Resolve binary
	if b := os.Getenv("CLAUDE_ESP_BIN"); b != "" {
		binary = b
	} else {
		// Walk up to find repo root (where go.mod lives)
		dir, _ := os.Getwd()
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				fmt.Fprintln(os.Stderr, "cannot find repo root")
				os.Exit(1)
			}
			dir = parent
		}
		binary = filepath.Join(dir, "claude-esp")
	}
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintf(os.Stderr, "binary not found: %s\n", binary)
		os.Exit(1)
	}

	model = os.Getenv("CLAUDE_TEST_MODEL")
	if model == "" {
		model = "haiku"
	}

	// Create temp dirs
	var err error
	workDir, err = os.MkdirTemp("", "esp-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeHome, err = os.MkdirTemp("", "esp-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.MkdirAll(filepath.Join(fakeHome, "projects"), 0o755)

	code := m.Run()

	// Cleanup
	os.RemoveAll(workDir)
	os.RemoveAll(fakeHome)

	os.Exit(code)
}

// ── seed ────────────────────────────────────────────────────────────────

func seed(t *testing.T) {
	t.Helper()
	seedOnce.Do(func() {
		t.Log("seeding session with claude -p...")
		cmd := exec.Command("claude", "-p",
			"Run: echo ESP_TOOL_MARKER_42. Then say exactly: ESP_TEXT_MARKER_99",
			"--model", model,
			"--allowedTools", "Bash",
		)
		cmd.Dir = workDir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			seedErr = fmt.Errorf("claude -p seed failed: %w", err)
			return
		}

		// Copy session files to fake CLAUDE_HOME
		proj := strings.ReplaceAll(workDir, "/", "-")
		src := filepath.Join(os.Getenv("HOME"), ".claude", "projects", proj)
		dst := filepath.Join(fakeHome, "projects", proj)
		os.MkdirAll(dst, 0o755)

		entries, err := filepath.Glob(filepath.Join(src, "*.jsonl"))
		if err != nil || len(entries) == 0 {
			seedErr = fmt.Errorf("no session files found in %s", src)
			return
		}
		for _, f := range entries {
			data, err := os.ReadFile(f)
			if err != nil {
				seedErr = fmt.Errorf("reading %s: %w", f, err)
				return
			}
			dst := filepath.Join(dst, filepath.Base(f))
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				seedErr = fmt.Errorf("writing %s: %w", dst, err)
				return
			}
			// Touch to refresh mtime for session discovery
			os.Chtimes(dst, time.Now(), time.Now())
		}
		t.Log("session seeded")
	})
	if seedErr != nil {
		t.Fatal(seedErr)
	}
}

// ── tui-use helpers ─────────────────────────────────────────────────────

type espSession struct {
	id string
	t  *testing.T
}

func espStart(t *testing.T, cols, rows int) *espSession {
	t.Helper()
	seed(t)

	out, err := exec.Command("tui-use", "start",
		"--cols", fmt.Sprint(cols),
		"--rows", fmt.Sprint(rows),
		"--label", "esp",
		"--cwd", workDir,
		"--", "env", fmt.Sprintf("CLAUDE_HOME=%s", fakeHome), binary,
	).Output()
	if err != nil {
		t.Fatalf("tui-use start: %v", err)
	}
	id := strings.TrimSpace(string(out))

	s := &espSession{id: id, t: t}
	s.use()

	// Wait for content to load
	cmd := exec.Command("tui-use", "wait", "--text", "Thinking", "10000")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		time.Sleep(5 * time.Second)
	}
	return s
}

func (s *espSession) use() {
	exec.Command("tui-use", "use", s.id).Run()
}

func (s *espSession) stop() {
	if s.id == "" {
		return
	}
	s.use()
	exec.Command("tui-use", "kill").Run()
	s.id = ""
	time.Sleep(300 * time.Millisecond)
}

func (s *espSession) snap() string {
	s.use()
	out, err := exec.Command("tui-use", "snapshot", "--format", "json").Output()
	if err != nil {
		s.t.Fatalf("snapshot: %v", err)
	}
	var result struct {
		Screen string `json:"screen"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		s.t.Fatalf("parse snapshot: %v", err)
	}
	return result.Screen
}

func (s *espSession) key(k string) {
	s.use()
	exec.Command("tui-use", "type", k).Run()
	time.Sleep(400 * time.Millisecond)
}

// ── assertions ──────────────────────────────────────────────────────────

func has(t *testing.T, screen, substr, label string) {
	t.Helper()
	if !strings.Contains(screen, substr) {
		t.Errorf("%s: expected %q in screen", label, substr)
	}
}

func hasnt(t *testing.T, screen, substr, label string) {
	t.Helper()
	if strings.Contains(screen, substr) {
		t.Errorf("%s: unexpected %q in screen", label, substr)
	}
}

func maxWidth(screen string) int {
	max := 0
	for _, line := range strings.Split(screen, "\n") {
		if w := utf8.RuneCountInString(line); w > max {
			max = w
		}
	}
	return max
}

// ── layout tests ────────────────────────────────────────────────────────

func TestLayout80x24(t *testing.T) {
	s := espStart(t, 80, 24)
	defer s.stop()
	screen := s.snap()

	has(t, screen, "Thinking[t]", "header")
	has(t, screen, "q: quit", "help")
	has(t, screen, "╭", "border")
	if w := maxWidth(screen); w > 80 {
		t.Errorf("width %d > 80", w)
	}
}

func TestLayout60x24(t *testing.T) {
	s := espStart(t, 60, 24)
	defer s.stop()
	screen := s.snap()

	has(t, screen, "Thinking[t]", "header")
	has(t, screen, "╭", "border")
	if w := maxWidth(screen); w > 60 {
		t.Errorf("width %d > 60", w)
	}
}

func TestLayout40x20(t *testing.T) {
	s := espStart(t, 40, 20)
	defer s.stop()
	screen := s.snap()

	has(t, screen, "Thinking[t]", "header")
	has(t, screen, "╭", "border")
	if w := maxWidth(screen); w > 40 {
		t.Errorf("width %d > 40", w)
	}
}

// ── functional tests ────────────────────────────────────────────────────

func TestToggles(t *testing.T) {
	s := espStart(t, 100, 40)
	defer s.stop()

	for _, k := range []string{"t", "i", "o", "x", "a"} {
		s.key(k)
		screen := s.snap()
		has(t, screen, "☐", k+" off")

		s.key(k)
		screen = s.snap()
		has(t, screen, "☑", k+" on")
	}

	// Tree toggle — border count
	s.key("h")
	screen := s.snap()
	has(t, screen, "☐ Tree", "tree off")
	if n := strings.Count(screen, "╭"); n != 1 {
		t.Errorf("tree off: expected 1 border, got %d", n)
	}

	s.key("h")
	time.Sleep(300 * time.Millisecond)
	screen = s.snap()
	has(t, screen, "☑ Tree", "tree on")
	if n := strings.Count(screen, "╭"); n < 2 {
		t.Errorf("tree on: expected ≥2 borders, got %d", n)
	}
}

func TestContent(t *testing.T) {
	s := espStart(t, 120, 40)
	defer s.stop()
	screen := s.snap()

	has(t, screen, "ESP_TEXT_MARKER_99", "text marker")
	has(t, screen, "ESP_TOOL_MARKER_42", "tool marker")
	has(t, screen, "🔧", "tool icon")
	has(t, screen, "💬", "response icon")
	has(t, screen, "📤", "output icon")
	has(t, screen, "────", "separators")
	if !strings.Contains(screen, "📁") && !strings.Contains(screen, "📂") {
		t.Error("missing folder icon")
	}
	has(t, screen, "Main", "main agent")
}

func TestFilters(t *testing.T) {
	s := espStart(t, 120, 40)
	defer s.stop()

	// Thinking filter: turn off thinking, text should remain
	s.key("t")
	screen := s.snap()
	has(t, screen, "☐ Thinking", "thinking off")
	has(t, screen, "ESP_TEXT_MARKER_99", "text survives")
	s.key("t") // restore

	// Output filter: turn off output, 📤 should vanish
	s.key("o")
	screen = s.snap()
	has(t, screen, "☐ Output", "output off")
	hasnt(t, screen, "📤", "output icon gone")
	s.key("o") // restore
}

func TestTreeInteract(t *testing.T) {
	s := espStart(t, 120, 40)
	defer s.stop()

	s.key("\t") // focus tree
	time.Sleep(200 * time.Millisecond)

	s.use()
	exec.Command("tui-use", "type", " ").Run() // uncheck
	time.Sleep(400 * time.Millisecond)

	screen := s.snap()
	has(t, screen, "☐", "unchecked")

	s.use()
	exec.Command("tui-use", "type", " ").Run() // recheck
	time.Sleep(400 * time.Millisecond)
}
