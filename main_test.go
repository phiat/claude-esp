package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func TestTruncatePathASCII(t *testing.T) {
	if got := truncatePath("/short/path", 30); got != "/short/path" {
		t.Errorf("truncatePath(%q, 30) = %q, want it uncut", "/short/path", got)
	}

	full := "/very/long/path/that/exceeds/the/budget"
	got := truncatePath(full, 20)
	if w := runewidth.StringWidth(got); w > 20 {
		t.Errorf("truncatePath(..., 20) = %q, width %d exceeds 20", got, w)
	}
	if !strings.HasPrefix(got, "...") {
		t.Errorf("truncatePath(..., 20) = %q, want a leading ellipsis", got)
	}
	if !strings.HasSuffix(full, strings.TrimPrefix(got, "...")) {
		t.Errorf("truncatePath(..., 20) = %q, not a tail of the input", got)
	}
}

func TestTruncatePathCJKNeverSplitsRune(t *testing.T) {
	// Byte slicing corrupted this, and the tail cut made it worse by landing
	// inside a rune from the other direction.
	path := "/Users/me/Documents/創世紀元網站專案"
	for max := 0; max <= 50; max++ {
		got := truncatePath(path, max)
		if w := runewidth.StringWidth(got); w > max && w > 3 {
			t.Errorf("truncatePath(max=%d) = %q, width %d exceeds budget", max, got, w)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncatePath(max=%d) = %q, not valid UTF-8", max, got)
		}
	}
}
