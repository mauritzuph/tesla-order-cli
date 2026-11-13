package tracker

import (
	"strings"
	"testing"
	"time"

	"github.com/mauritzuph/tesla-order-cli/internal/api"
	"github.com/mauritzuph/tesla-order-cli/internal/auth"
	"github.com/mauritzuph/tesla-order-cli/internal/diff"
	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
	"github.com/mauritzuph/tesla-order-cli/internal/store"
	"github.com/mauritzuph/tesla-order-cli/internal/testutil"
)

var now = time.Unix(1_800_000_000, 0)

func routes(status, window string) map[string]testutil.Route {
	return map[string]testutil.Route{
		api.OrdersURL: {Value: map[string]any{"response": []any{map[string]any{"referenceNumber": "RN1", "orderStatus": status}}}},
		api.TasksURL:  {Value: map[string]any{"tasks": map[string]any{"scheduling": map[string]any{"deliveryWindowDisplay": window}}}},
	}
}

func setup(t *testing.T) *store.Store {
	s := store.New(t.TempDir())
	if err := s.SaveTokens(auth.Tokens{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: 1_800_003_600}); err != nil {
		t.Fatal(err)
	}
	return s
}

func run(s *store.Store, http httpx.Client, ignore ...string) Result {
	return Run(s, http, Options{Ignore: ignore, Now: func() time.Time { return now }})
}

func TestNotLoggedIn(t *testing.T) {
	res := run(store.New(t.TempDir()), testutil.NewFakeHTTP(nil))
	if res.ExitCode != ExitLoginRequired {
		t.Fatalf("got %+v", res)
	}
}

func TestFirstRunThenChanges(t *testing.T) {
	s := setup(t)
	res := run(s, testutil.NewFakeHTTP(routes("BOOKED", "Dec")))
	if res.ExitCode != ExitOK || !res.FirstRun || s.LoadSnapshot() == nil {
		t.Fatalf("first run: %+v", res)
	}
	fetched := s.LoadSnapshot().FetchedAt

	res = run(s, testutil.NewFakeHTTP(routes("BOOKED", "Dec")))
	if len(res.Changes) != 0 || res.FirstRun || s.LoadSnapshot().FetchedAt != fetched {
		t.Fatalf("no-change run should keep snapshot: %+v", res)
	}

	res = run(s, testutil.NewFakeHTTP(routes("BOOKED", "Nov 20 - Nov 30")))
	want := diff.Change{Kind: diff.Changed, Path: "RN1.details.tasks.scheduling.deliveryWindowDisplay", Old: "Dec", New: "Nov 20 - Nov 30"}
	if len(res.Changes) != 1 || res.Changes[0] != want {
		t.Fatalf("got %v", res.Changes)
	}
}

func TestIgnorePatternsAndUIText(t *testing.T) {
	s := setup(t)
	run(s, testutil.NewFakeHTTP(routes("BOOKED", "Dec")))
	if res := run(s, testutil.NewFakeHTTP(routes("DELIVERED", "Dec")), "*.orderStatus"); len(res.Changes) != 0 {
		t.Fatalf("ignored change reported: %v", res.Changes)
	}

	s = setup(t)
	r := routes("BOOKED", "Dec")
	r[api.TasksURL] = testutil.Route{Value: map[string]any{"strings": map[string]any{"back": "Back"}, "state": map[string]any{"pad": 8}}}
	run(s, testutil.NewFakeHTTP(r))
	r[api.TasksURL] = testutil.Route{Value: map[string]any{"strings": map[string]any{"back": "Zurück"}, "state": map[string]any{"pad": 9}}}
	if res := run(s, testutil.NewFakeHTTP(r)); len(res.Changes) != 0 {
		t.Fatalf("UI text changes reported: %v", res.Changes)
	}
}

func TestExpiredTokenIsRefreshedAndPersisted(t *testing.T) {
	s := store.New(t.TempDir())
	_ = s.SaveTokens(auth.Tokens{AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: 1_799_999_990})
	r := routes("BOOKED", "Dec")
	r[auth.TokenURL] = testutil.Route{Value: map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 28800}}
	http := testutil.NewFakeHTTP(r)
	if res := run(s, http); res.ExitCode != ExitOK {
		t.Fatalf("got %+v", res)
	}
	if got := *s.LoadTokens(); got != (auth.Tokens{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresAt: 1_800_028_800}) {
		t.Fatalf("tokens not persisted: %+v", got)
	}
	if h := http.CallsTo(api.OrdersURL)[0].Headers["Authorization"]; h != "Bearer new-access" {
		t.Errorf("used %q", h)
	}
}

func TestFailures(t *testing.T) {
	s := store.New(t.TempDir())
	_ = s.SaveTokens(auth.Tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: 0})
	http := testutil.NewFakeHTTP(map[string]testutil.Route{auth.TokenURL: {Err: &httpx.Error{Status: 401}}})
	if res := run(s, http); res.ExitCode != ExitLoginRequired {
		t.Errorf("rejected refresh: %+v", res)
	}
	s = setup(t)
	http = testutil.NewFakeHTTP(map[string]testutil.Route{api.OrdersURL: {Err: &httpx.Error{Status: 500, Message: "oops"}}})
	res := run(s, http)
	if res.ExitCode != ExitError || !strings.Contains(res.Err.Error(), "500") || s.LoadSnapshot() != nil {
		t.Errorf("API failure: %+v", res)
	}
}

func TestFormatChanges(t *testing.T) {
	got := FormatChanges([]diff.Change{
		{Kind: diff.Added, Path: "RN2", New: map[string]any{}},
		{Kind: diff.Changed, Path: "RN1.details.tasks.scheduling.window", Old: "a", New: "b"},
		{Kind: diff.Added, Path: "RN1.order.vin", New: "5YJ"},
	})
	want := "Order RN2 was added.\n\nOrder RN1 changed:\n  ~ scheduling.window: \"a\" -> \"b\"\n  + order.vin: \"5YJ\""
	if got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestTimelineRecordsMilestones(t *testing.T) {
	s := setup(t)
	run(s, testutil.NewFakeHTTP(routes("BOOKED", "")))
	run(s, testutil.NewFakeHTTP(routes("BOOKED", "")))
	res := run(s, testutil.NewFakeHTTP(routes("BOOKED", "Nov 20 - Nov 30")))

	got := s.LoadTimeline()["RN1"]
	if len(got) != 3 || got[0].Key != "status" || got[0].At != "" || got[1].Key != "tracking_started" {
		t.Fatalf("got %+v", got)
	}
	if w := got[2]; w.Key != "window" || w.Label != "Delivery window announced" || w.To != "Nov 20 - Nov 30" ||
		w.At != now.UTC().Format(time.RFC3339) {
		t.Errorf("got %+v", w)
	}
	if len(res.Timeline["RN1"]) != 3 {
		t.Errorf("result timeline: %+v", res.Timeline)
	}
}
