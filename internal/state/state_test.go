package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPermsAndAtomicWrite(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root, "xiaox")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("binding dir perm %o", info.Mode().Perm())
	}

	sess := &Session{
		BotToken:  "tok",
		BaseURL:   "https://example",
		BotID:     "bot@im.bot",
		UserID:    "wxid_me",
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	if err := st.SaveSession(sess); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(st.sessionPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("session perm %o", fi.Mode().Perm())
	}
	got, err := st.LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "wxid_me" || got.BotToken != "tok" {
		t.Fatalf("%+v", got)
	}

	matches, _ := filepath.Glob(filepath.Join(st.Root, ".tmp-*"))
	if len(matches) != 0 {
		t.Fatalf("tmp leftovers %v", matches)
	}

	if err := st.SaveCursor("cursor-1"); err != nil {
		t.Fatal(err)
	}
	c, err := st.LoadCursor()
	if err != nil || c != "cursor-1" {
		t.Fatalf("cursor %q %v", c, err)
	}
	fi, _ = os.Stat(st.cursorPath())
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("cursor perm %o", fi.Mode().Perm())
	}
}

func TestContextTokenPersistence(t *testing.T) {
	st, err := Open(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{"wxid_a": "ctok-1"}
	if err := st.SaveTokens(m); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if got["wxid_a"] != "ctok-1" {
		t.Fatalf("%v", got)
	}
	fi, _ := os.Stat(st.tokensPath())
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("tokens perm %o", fi.Mode().Perm())
	}

	if st.IsExpired() {
		t.Fatal("not expired yet")
	}
	if err := st.MarkExpired(); err != nil {
		t.Fatal(err)
	}
	if !st.IsExpired() {
		t.Fatal("expected expired")
	}
	if err := st.ClearExpired(); err != nil {
		t.Fatal(err)
	}
	if st.IsExpired() {
		t.Fatal("cleared")
	}
}
