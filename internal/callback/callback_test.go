package callback

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/auth"
)

type scripted struct{ values []string }

func (s *scripted) Poll() string {
	if len(s.values) == 0 {
		return ""
	}
	v := s.values[0]
	s.values = s.values[1:]
	return v
}

func fakeClock() Clock {
	now := time.Unix(0, 0)
	return Clock{Now: func() time.Time { return now }, Sleep: func(d time.Duration) { now = now.Add(d) }}
}

const cb = auth.RedirectURI

func TestFirstValidCallbackWins(t *testing.T) {
	a := &scripted{[]string{"", cb + "?code=A&state=s1"}}
	b := &scripted{[]string{"", "", cb + "?code=B&state=s1"}}
	code, err := WaitForCode([]Source{a, b}, "s1", 10*time.Second, fakeClock())
	if err != nil || code != "A" {
		t.Fatalf("got %q %v", code, err)
	}
}

func TestSkipsUnrelatedTextAndStaleAttempts(t *testing.T) {
	s := &scripted{[]string{"some copied text", cb + "?code=OLD&state=previous", cb + "?code=NEW&state=s1"}}
	code, err := WaitForCode([]Source{s}, "s1", 10*time.Second, fakeClock())
	if err != nil || code != "NEW" {
		t.Fatalf("got %q %v", code, err)
	}
}

func TestLoginErrorAndTimeout(t *testing.T) {
	_, err := WaitForCode([]Source{&scripted{[]string{cb + "?error=x&error_description=denied"}}}, "s1", time.Second, fakeClock())
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Errorf("got %v", err)
	}
	_, err = WaitForCode([]Source{&scripted{}}, "s1", 3*time.Second, fakeClock())
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("got %v", err)
	}
}

func TestFileInboxReadsOnce(t *testing.T) {
	inbox := FileInbox{Path: filepath.Join(t.TempDir(), "callback.txt")}
	if inbox.Poll() != "" {
		t.Fatal("empty inbox")
	}
	_ = os.WriteFile(inbox.Path, nil, 0o600)
	if inbox.Poll() != "" {
		t.Fatal("still being written")
	}
	_ = os.WriteFile(inbox.Path, []byte(cb+"?code=x\n"), 0o600)
	if inbox.Poll() != cb+"?code=x" || inbox.Poll() != "" {
		t.Fatal("should read exactly once")
	}
	_ = os.WriteFile(inbox.Path, []byte("stale"), 0o600)
	inbox.Clear()
	if inbox.Poll() != "" {
		t.Fatal("clear failed")
	}
}

func TestClipboardIgnoresPreexistingContent(t *testing.T) {
	text := cb + "?code=stale"
	c := NewClipboardWith(func() (string, error) { return text, nil })
	if c.Poll() != "" {
		t.Fatal("pre-existing content returned")
	}
	text = " " + cb + "?code=new "
	if c.Poll() != cb+"?code=new" || c.Poll() != "" {
		t.Fatal("new content not returned exactly once")
	}
	broken := NewClipboardWith(func() (string, error) { return "", errors.New("no pbpaste") })
	if broken.Poll() != "" {
		t.Fatal("errors should be ignored")
	}
}
