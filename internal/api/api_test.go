package api

import (
	"net/url"
	"testing"

	"github.com/mauritzuph/tesla-order-cli/internal/testutil"
)

func TestFetchSnapshotKeysByReferenceNumber(t *testing.T) {
	http := testutil.NewFakeHTTP(map[string]testutil.Route{
		OrdersURL: {Value: map[string]any{"response": []any{
			map[string]any{"referenceNumber": "RN1"}, map[string]any{"referenceNumber": "RN2"},
		}}},
		TasksURL: {Func: func(u string, _ any) any {
			parsed, _ := url.Parse(u)
			return map[string]any{"query": parsed.Query().Get("referenceNumber") + "/" + parsed.Query().Get("deviceCountry")}
		}},
	})
	snap, err := API{HTTP: http, Token: "tok", Country: "NL"}.FetchSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := snap["RN2"].(map[string]any)["details"].(map[string]any)["query"]; got != "RN2/NL" {
		t.Errorf("got %v", got)
	}
	for _, c := range http.Calls {
		if c.Headers["Authorization"] != "Bearer tok" {
			t.Errorf("missing auth on %s", c.URL)
		}
	}
}

func TestNoOrders(t *testing.T) {
	http := testutil.NewFakeHTTP(map[string]testutil.Route{OrdersURL: {Value: map[string]any{"response": nil}}})
	snap, err := API{HTTP: http, Token: "t"}.FetchSnapshot()
	if err != nil || len(snap) != 0 {
		t.Fatalf("got %v, %v", snap, err)
	}
}
