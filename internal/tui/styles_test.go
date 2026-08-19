package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func TestTruncateASCII(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 25, "short"},
		{"0123456789abcdef", 10, "0123456..."},
		{"0123456789", 3, "012"},
	}
	for _, c := range cases {
		if got := Truncate(c.in, c.max); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestTruncateCJKTitleFits(t *testing.T) {
	// 26 bytes but only 22 display columns (14 ASCII + 4 CJK x 2). The byte
	// length check mis-measured this as over-long and then cut it into
	// invalid UTF-8.
	title := "Claude-esp-rs 安裝確認"
	if got := Truncate(title, 25); got != title {
		t.Errorf("Truncate(%q, 25) = %q, want it uncut", title, got)
	}
}

func TestTruncateCJKTitleOverflows(t *testing.T) {
	title := "Claude-esp-rs 安裝確認與完整操作方式"
	got := Truncate(title, 25)

	if w := runewidth.StringWidth(got); w > 25 {
		t.Errorf("Truncate(%q, 25) = %q, width %d exceeds 25", title, got, w)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("Truncate(%q, 25) = %q, want a trailing ellipsis", title, got)
	}
}

func TestTruncateCJKNeverSplitsRune(t *testing.T) {
	title := "測試中文標題截斷行為不要壞掉"
	for max := 0; max <= 40; max++ {
		got := Truncate(title, max)
		if w := runewidth.StringWidth(got); w > max && w > 3 {
			t.Errorf("Truncate(max=%d) = %q, width %d exceeds budget", max, got, w)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(max=%d) = %q, not valid UTF-8", max, got)
		}
	}
}

func TestTruncateEmoji(t *testing.T) {
	got := Truncate("Hello 🔧🔧🔧🔧🔧🔧 world", 15)
	if w := runewidth.StringWidth(got); w > 15 {
		t.Errorf("Truncate(..., 15) = %q, width %d exceeds 15", got, w)
	}
	if !utf8.ValidString(got) {
		t.Errorf("Truncate(..., 15) = %q, not valid UTF-8", got)
	}
}
