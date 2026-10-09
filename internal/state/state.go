package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/ilink"
)

const (
	sessionFile = "session.json"
	tokensFile  = "context_tokens.json"
	cursorFile  = "cursor"
	expiredFile = "expired"
	dirPerm     = 0o700
	filePerm    = 0o600
)

// ErrNoSession is returned when no session file exists yet.
var ErrNoSession = errors.New("no wechat session file")

// Session is the persisted iLink login state for one binding.
type Session struct {
	BotToken  string    `json:"bot_token"`
	BaseURL   string    `json:"base_url"`
	BotID     string    `json:"ilink_bot_id"`
	UserID    string    `json:"ilink_user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ToIlink converts to the protocol session used by ilink.Client.
func (s *Session) ToIlink() *ilink.Session {
	if s == nil {
		return nil
	}
	return &ilink.Session{
		BotToken: s.BotToken,
		BaseURL:  s.BaseURL,
		BotID:    s.BotID,
		UserID:   s.UserID,
	}
}

// FromIlink builds a persisted session, stamping created_at as now.
func FromIlink(s *ilink.Session) *Session {
	return &Session{
		BotToken:  s.BotToken,
		BaseURL:   s.BaseURL,
		BotID:     s.BotID,
		UserID:    s.UserID,
		CreatedAt: time.Now().UTC(),
	}
}

// Store is the per-binding state directory under state_dir/<binding>/.
type Store struct {
	Root string // absolute path of the binding directory
}

// Open ensures state_dir and state_dir/<binding> exist with 0700 perms.
func Open(stateDir, binding string) (*Store, error) {
	if err := MkdirAll0700(stateDir); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	root := filepath.Join(stateDir, binding)
	if err := MkdirAll0700(root); err != nil {
		return nil, fmt.Errorf("binding state dir: %w", err)
	}
	return &Store{Root: root}, nil
}

func (s *Store) sessionPath() string { return filepath.Join(s.Root, sessionFile) }
func (s *Store) tokensPath() string  { return filepath.Join(s.Root, tokensFile) }
func (s *Store) cursorPath() string  { return filepath.Join(s.Root, cursorFile) }
func (s *Store) expiredPath() string { return filepath.Join(s.Root, expiredFile) }

// LoadSession reads session.json.
func (s *Store) LoadSession() (*Session, error) {
	f, err := os.Open(s.sessionPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()
	var sess Session
	if err := json.NewDecoder(f).Decode(&sess); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	if sess.BotToken == "" || sess.BaseURL == "" {
		return nil, fmt.Errorf("session incomplete (missing token or base_url)")
	}
	return &sess, nil
}

// SaveSession writes session.json atomically (0600).
func (s *Store) SaveSession(sess *Session) error {
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return WriteAtomic(s.sessionPath(), b)
}

// LoadTokens returns the wxid → context_token map. Missing file → empty map.
func (s *Store) LoadTokens() (map[string]string, error) {
	b, err := os.ReadFile(s.tokensPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode context_tokens: %w", err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// SaveTokens writes context_tokens.json atomically (0600).
func (s *Store) SaveTokens(m map[string]string) error {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return WriteAtomic(s.tokensPath(), b)
}

// LoadCursor returns the last get_updates_buf. Missing file → "".
func (s *Store) LoadCursor() (string, error) {
	b, err := os.ReadFile(s.cursorPath())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SaveCursor writes the cursor file atomically (0600).
func (s *Store) SaveCursor(cursor string) error {
	return WriteAtomic(s.cursorPath(), []byte(cursor))
}

// IsExpired reports whether the expired marker file exists.
func (s *Store) IsExpired() bool {
	_, err := os.Stat(s.expiredPath())
	return err == nil
}

// MarkExpired writes the expired marker (0600).
func (s *Store) MarkExpired() error {
	return WriteAtomic(s.expiredPath(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
}

// ClearExpired removes the expired marker if present.
func (s *Store) ClearExpired() error {
	err := os.Remove(s.expiredPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// HasSession reports whether session.json exists.
func (s *Store) HasSession() bool {
	_, err := os.Stat(s.sessionPath())
	return err == nil
}

// HasCursor reports whether the cursor file exists and is non-empty.
func (s *Store) HasCursor() bool {
	b, err := os.ReadFile(s.cursorPath())
	return err == nil && len(b) > 0
}

// MkdirAll0700 creates path and parents with 0700, then chmod 0700.
func MkdirAll0700(path string) error {
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return err
	}
	return os.Chmod(path, dirPerm)
}

// WriteFile0600 writes data to path with 0600 permissions.
func WriteFile0600(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	return os.Chmod(path, filePerm)
}

// WriteAtomic writes data to path via a temp file + rename, mode 0600.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := MkdirAll0700(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleanup = false
	return os.Chmod(path, filePerm)
}
