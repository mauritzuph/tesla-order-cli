// Package callback collects the tesla://auth/callback URL during login.
//
// Login polls several sources at once and takes the first valid URL:
//   - FileInbox: written by the macOS tesla:// handler app.
//   - Clipboard: fallback; copying the URL from DevTools is enough (macOS only).
package callback

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/auth"
)

type Source interface {
	Poll() string
}

// FileInbox reads (and removes) a file written by the handler app.
type FileInbox struct{ Path string }

func (f FileInbox) Clear() { _ = os.Remove(f.Path) }

func (f FileInbox) Poll() string {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "" // handler still writing
	}
	_ = os.Remove(f.Path)
	return text
}

// Clipboard returns clipboard text that appeared after it was created.
type Clipboard struct {
	Read func() (string, error)
	last string
}

// NewClipboard watches the system clipboard. ok is false where unsupported.
func NewClipboard() (*Clipboard, bool) {
	if runtime.GOOS != "darwin" {
		return nil, false
	}
	return NewClipboardWith(func() (string, error) {
		out, err := exec.Command("pbpaste").Output()
		return string(out), err
	}), true
}

func NewClipboardWith(read func() (string, error)) *Clipboard {
	c := &Clipboard{Read: read}
	c.last = c.read()
	return c
}

func (c *Clipboard) read() string {
	s, err := c.Read()
	if err != nil {
		return ""
	}
	return s
}

func (c *Clipboard) Poll() string {
	text := c.read()
	if text == c.last {
		return ""
	}
	c.last = text
	return strings.TrimSpace(text)
}

var ErrTimeout = errors.New("timed out waiting for the Tesla login to finish")

// Clock is injectable for tests.
type Clock struct {
	Now   func() time.Time
	Sleep func(time.Duration)
}

// WaitForCode polls sources until one yields a valid callback and returns its
// auth code. Unrelated text and callbacks from earlier attempts (state
// mismatch) are skipped; a callback reporting a login error is returned.
func WaitForCode(sources []Source, state string, timeout time.Duration, clock Clock) (string, error) {
	if clock.Now == nil {
		clock.Now = time.Now
	}
	if clock.Sleep == nil {
		clock.Sleep = time.Sleep
	}
	deadline := clock.Now().Add(timeout)
	for {
		for _, s := range sources {
			u := s.Poll()
			if u == "" || !auth.IsCallbackURL(u) {
				continue
			}
			code, err := auth.ParseCallback(u, state)
			if errors.Is(err, auth.ErrStateMismatch) {
				continue
			}
			return code, err
		}
		if !clock.Now().Before(deadline) {
			return "", ErrTimeout
		}
		clock.Sleep(500 * time.Millisecond)
	}
}
