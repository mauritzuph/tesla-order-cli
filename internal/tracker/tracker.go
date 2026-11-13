// Package tracker implements `check`: refresh tokens, fetch orders, compare with
// the last snapshot, save.
package tracker

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/api"
	"github.com/mauritzuph/tesla-order-cli/internal/auth"
	"github.com/mauritzuph/tesla-order-cli/internal/diff"
	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
	"github.com/mauritzuph/tesla-order-cli/internal/store"
	"github.com/mauritzuph/tesla-order-cli/internal/summary"
	"github.com/mauritzuph/tesla-order-cli/internal/timeline"
)

const (
	ExitOK            = 0
	ExitError         = 1
	ExitLoginRequired = 2
)

// DefaultIgnore skips UI text and app layout in the /tasks response; changes
// there are not order news.
var DefaultIgnore = []string{"*.details.strings*", "*.details.state*"}

type Result struct {
	ExitCode int
	Orders   map[string]any
	Changes  []diff.Change
	Timeline timeline.Timelines
	FirstRun bool
	Err      error
}

// ValidTokens loads tokens, refreshing (and persisting the rotated tokens) when needed.
func ValidTokens(s *store.Store, c auth.Client, now time.Time) (auth.Tokens, error) {
	t := s.LoadTokens()
	if t == nil {
		return auth.Tokens{}, &auth.ErrLoginRequired{Reason: "Not logged in yet."}
	}
	if !t.Expired(now) {
		return *t, nil
	}
	slog.Debug("access token expired, refreshing")
	fresh, err := c.Refresh(*t)
	if err != nil {
		return auth.Tokens{}, err
	}
	if err := s.SaveTokens(fresh); err != nil {
		return auth.Tokens{}, err
	}
	return fresh, nil
}

type Options struct {
	Country string
	Ignore  []string
	Now     func() time.Time
}

func Run(s *store.Store, client httpx.Client, opts Options) Result {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	tokens, err := ValidTokens(s, auth.Client{HTTP: client, Now: now}, now())
	if err == nil {
		var orders map[string]any
		orders, err = api.API{HTTP: client, Token: tokens.AccessToken, Country: opts.Country}.FetchSnapshot()
		if err == nil {
			return compare(s, orders, opts, now())
		}
	}
	var login *auth.ErrLoginRequired
	if errors.As(err, &login) {
		return Result{ExitCode: ExitLoginRequired, Err: err}
	}
	return Result{ExitCode: ExitError, Err: err}
}

func compare(s *store.Store, orders map[string]any, opts Options, now time.Time) Result {
	previous := s.LoadSnapshot()
	snap := store.Snapshot{FetchedAt: now.UTC().Format(time.RFC3339Nano), Orders: orders}
	var previousOrders map[string]any
	if previous != nil {
		previousOrders = previous.Orders
	}
	tl, err := updateTimeline(s, previousOrders, orders, now)
	if err != nil {
		return Result{ExitCode: ExitError, Err: err}
	}
	if previous == nil {
		if err := s.SaveSnapshot(snap); err != nil {
			return Result{ExitCode: ExitError, Err: err}
		}
		return Result{ExitCode: ExitOK, Orders: orders, Timeline: tl, FirstRun: true}
	}
	ignore := append(append([]string{}, DefaultIgnore...), opts.Ignore...)
	changes := diff.Diff(previous.Orders, orders, ignore)
	if len(changes) > 0 {
		if err := s.SaveSnapshot(snap); err != nil {
			return Result{ExitCode: ExitError, Err: err}
		}
	}
	return Result{ExitCode: ExitOK, Orders: orders, Changes: changes, Timeline: tl}
}

// updateTimeline adds the milestones since the last snapshot and saves them.
func updateTimeline(s *store.Store, previous, current map[string]any, now time.Time) (timeline.Timelines, error) {
	tl := s.LoadTimeline()
	if timeline.Update(tl, summaries(previous), summaries(current), now.UTC().Format(time.RFC3339)) {
		if err := s.SaveTimeline(tl); err != nil {
			return nil, err
		}
	}
	return tl, nil
}

func summaries(orders map[string]any) map[string]summary.Summary {
	out := map[string]summary.Summary{}
	for ref, entry := range orders {
		e, _ := entry.(map[string]any)
		out[ref] = summary.Summarize(e)
	}
	return out
}

// FormatChanges groups changes per order and shortens paths to what matters.
func FormatChanges(changes []diff.Change) string {
	var order []string
	byRef := map[string][]diff.Change{}
	for _, c := range changes {
		ref, _, _ := strings.Cut(c.Path, ".")
		ref, _, _ = strings.Cut(ref, "[")
		if _, ok := byRef[ref]; !ok {
			order = append(order, ref)
		}
		byRef[ref] = append(byRef[ref], c)
	}
	var blocks []string
	for _, ref := range order {
		cs := byRef[ref]
		if len(cs) == 1 && cs[0].Path == ref {
			blocks = append(blocks, "Order "+ref+" was "+string(cs[0].Kind)+".")
			continue
		}
		lines := []string{"Order " + ref + " changed:"}
		for _, c := range cs {
			c.Path = strings.TrimPrefix(strings.TrimPrefix(c.Path, ref+"."), "details.tasks.")
			lines = append(lines, "  "+c.String())
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}
