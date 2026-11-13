// Package store keeps local state: tokens, the last order snapshot and the
// order timelines.
package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mauritzuph/tesla-order-cli/internal/auth"
	"github.com/mauritzuph/tesla-order-cli/internal/timeline"
)

const EnvDataDir = "TESLA_ORDER_CLI_HOME"

// DefaultDir is $TESLA_ORDER_CLI_HOME or ~/.tesla-order-cli.
func DefaultDir() string {
	if d := os.Getenv(EnvDataDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tesla-order-cli"
	}
	return filepath.Join(home, ".tesla-order-cli")
}

// legacyDirName is the data folder used before the project was renamed.
const legacyDirName = ".tesla-order-tracker"

// MigrateLegacyDir moves ~/.tesla-order-tracker to dir, once: only if dir
// doesn't exist yet and the old folder does. Returns the old path if moved.
func MigrateLegacyDir(dir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	old := filepath.Join(home, legacyDirName)
	if _, err := os.Stat(dir); err == nil {
		return "", nil
	}
	if _, err := os.Stat(old); err != nil {
		return "", nil
	}
	if err := os.Rename(old, dir); err != nil {
		return "", err
	}
	return old, nil
}

// Snapshot is the last fetched state of all orders, keyed by reference number.
type Snapshot struct {
	FetchedAt string         `json:"fetched_at"`
	Orders    map[string]any `json:"orders"`
}

type Store struct{ Dir string }

func New(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) TokensPath() string   { return filepath.Join(s.Dir, "tokens.json") }
func (s *Store) SnapshotPath() string { return filepath.Join(s.Dir, "orders.json") }
func (s *Store) TimelinePath() string { return filepath.Join(s.Dir, "timeline.json") }

// LoadTokens returns nil when there are no (readable) tokens.
func (s *Store) LoadTokens() *auth.Tokens {
	var t auth.Tokens
	if !s.read(s.TokensPath(), &t) {
		return nil
	}
	return &t
}

func (s *Store) SaveTokens(t auth.Tokens) error { return s.write(s.TokensPath(), t) }

func (s *Store) DeleteTokens() (bool, error) {
	err := os.Remove(s.TokensPath())
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// LoadSnapshot returns nil when there is no (readable) snapshot.
func (s *Store) LoadSnapshot() *Snapshot {
	var snap Snapshot
	if !s.read(s.SnapshotPath(), &snap) || snap.Orders == nil {
		return nil
	}
	return &snap
}

func (s *Store) SaveSnapshot(snap Snapshot) error { return s.write(s.SnapshotPath(), snap) }

// LoadTimeline returns an empty timeline when there is none (yet).
func (s *Store) LoadTimeline() timeline.Timelines {
	t := timeline.Timelines{}
	if !s.read(s.TimelinePath(), &t) || t == nil {
		return timeline.Timelines{}
	}
	return t
}

func (s *Store) SaveTimeline(t timeline.Timelines) error { return s.write(s.TimelinePath(), t) }

func (s *Store) read(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("ignoring unreadable file", "path", path, "err", err)
		}
		return false
	}
	if err := json.Unmarshal(data, v); err != nil {
		slog.Warn("ignoring unreadable file", "path", path, "err", err)
		return false
	}
	return true
}

// write is atomic and readable only by the current user.
func (s *Store) write(path string, v any) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
