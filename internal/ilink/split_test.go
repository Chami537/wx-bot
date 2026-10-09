package ilink

import (
	"strings"
	"testing"
)

func TestSplitTextASCII(t *testing.T) {
	if got := SplitText("hello", 10); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("short: %q", got)
	}
	exact := strings.Repeat("a", 2000)
	got := SplitText(exact, MaxTextRunes)
	if len(got) != 1 || got[0] != exact {
		t.Fatalf("exact 2000: chunks=%d len0=%d", len(got), len([]rune(got[0])))
	}
	over := strings.Repeat("b", 2001)
	got = SplitText(over, MaxTextRunes)
	if len(got) != 2 {
		t.Fatalf("2001 runes: %d chunks", len(got))
	}
	if len([]rune(got[0])) != 2000 || len([]rune(got[1])) != 1 {
		t.Fatalf("boundary: %d + %d", len([]rune(got[0])), len([]rune(got[1])))
	}
}

func TestSplitTextCJK(t *testing.T) {
	s := strings.Repeat("你", 10)
	got := SplitText(s, 3)
	if len(got) != 4 {
		t.Fatalf("chunks %d: %q", len(got), got)
	}
	if got[0] != "你你你" || got[3] != "你" {
		t.Fatalf("cjk chunks %q", got)
	}
}

func TestSplitTextEmoji(t *testing.T) {
	s := strings.Repeat("😀", 5)
	got := SplitText(s, 2)
	if len(got) != 3 {
		t.Fatalf("emoji chunks %d %q", len(got), got)
	}
	if got[0] != "😀😀" || got[2] != "😀" {
		t.Fatalf("emoji split %q", got)
	}
}

func TestSplitTextEmpty(t *testing.T) {
	got := SplitText("", 8)
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("%q", got)
	}
}
