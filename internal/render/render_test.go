package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mauritzuph/tesla-order-cli/internal/summary"
	"github.com/mauritzuph/tesla-order-cli/internal/testutil"
	"github.com/mauritzuph/tesla-order-cli/internal/timeline"
)

func output(t *testing.T, format string, all bool, orders map[string]any) string {
	t.Helper()
	if orders == nil {
		orders = map[string]any{"RN1": testutil.Entry()}
	}
	var buf bytes.Buffer
	if err := Render(&buf, Build(orders, "2026-11-08T16:46:08+00:00", all), format); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestPretty(t *testing.T) {
	out := output(t, "pretty", false, nil)
	for _, want := range []string{"Model 3 · RN1", "Pickup service center", "-250 EUR", "✓  Delivery details",
		"●  Financing", "Fetched 2026-11-08 16:46", "VIN"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "Reserved") {
		t.Error("empty optional fields should be hidden")
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("no colors when not writing to a terminal")
	}
}

func TestTableKeepsRawValues(t *testing.T) {
	out := output(t, "table", false, nil)
	if !strings.Contains(out, "PICKUP_SERVICE_CENTER") || !strings.Contains(out, "Delivery checklist") {
		t.Error(out)
	}
}

func TestStructuredFormatsRoundtrip(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		text := output(t, format, true, nil)
		var doc map[string]any
		var err error
		if format == "json" {
			err = json.Unmarshal([]byte(text), &doc)
		} else {
			err = yaml.Unmarshal([]byte(text), &doc)
		}
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		order := doc["orders"].([]any)[0].(map[string]any)
		if order["reference_number"] != "RN1" {
			t.Errorf("%s: ref %v", format, order["reference_number"])
		}
		if v, ok := order["order"].(map[string]any)["vin"]; !ok || v != nil {
			t.Errorf("%s: empty fields should be kept as null", format)
		}
		amount := order["data_fields"].(map[string]any)["details.tasks.finalPayment.amountDue"]
		if amount != -250.0 && amount != -250 {
			t.Errorf("%s: data field %v", format, amount)
		}
	}
	if !strings.Contains(output(t, "json", false, nil), "https://www.tesla.com/schedule/RN1") {
		t.Error("URLs must not be HTML-escaped")
	}
}

func TestAllAddsDataFields(t *testing.T) {
	if !strings.Contains(output(t, "pretty", true, nil), "details.tasks.finalPayment.amountDue") {
		t.Error("--all missing data fields")
	}
	if strings.Contains(output(t, "pretty", false, nil), "details.tasks.finalPayment.amountDue") {
		t.Error("data fields shown without --all")
	}
}

func TestNoOrdersAndUnknownFormat(t *testing.T) {
	for _, f := range Formats {
		out := output(t, f, false, map[string]any{})
		if !strings.Contains(out, "No orders") && !strings.Contains(out, "orders: []") && !strings.Contains(out, `"orders": []`) {
			t.Errorf("%s: %q", f, out)
		}
	}
	if err := Render(&bytes.Buffer{}, Document{}, "xml"); err == nil {
		t.Error("xml should fail")
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		key      string
		v        any
		humanize bool
		want     string
	}{
		{"x", nil, false, "—"},
		{"x", true, false, "yes"},
		{"x", summary.Money{Amount: 1234567.0, Currency: "EUR"}, false, "1,234,567 EUR"},
		{"x", summary.Money{Amount: 12.5, Currency: "EUR"}, false, "12.5 EUR"},
		{"x", summary.Money{Amount: -250.0, Currency: "EUR"}, false, "-250 EUR"},
		{"x", "2026-11-06T09:43:37.978232", false, "2026-11-06 09:43"},
		{"x", "PAYMENT_COMPLETE", true, "Payment complete"},
		{"x", "PAYMENT_COMPLETE", false, "PAYMENT_COMPLETE"},
		{"option_codes", []any{"APBS", "MDL3"}, true, "APBS, MDL3"},
		{"x", map[string]any{"a": "B_C"}, true, "a: B c"},
	}
	for _, c := range cases {
		if got := FormatValue(c.key, c.v, c.humanize); got != c.want {
			t.Errorf("FormatValue(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestReferralHintOnlyInPretty(t *testing.T) {
	if !strings.Contains(output(t, "pretty", false, nil), ReferralURL) {
		t.Error("pretty output should end with the referral link")
	}
	for _, f := range []string{"table", "json", "yaml"} {
		if strings.Contains(output(t, f, false, nil), ReferralURL) {
			t.Errorf("%s output must not contain the referral link", f)
		}
	}
}

func TestTimeline(t *testing.T) {
	doc := Build(map[string]any{"RN1": testutil.Entry()}, "", false)
	doc.Timelines = timeline.Timelines{"RN1": {
		{Key: "status", Label: "Status", To: "BOOKED"},
		{Key: timeline.KeyTrackingStarted, Label: "Tracking started", At: "2026-11-08T16:46:08Z"},
		{Key: "routing_location", Label: "Routing location changed", At: "2026-11-10T08:00:00Z", From: "1001", To: "1002"},
	}}
	for format, wants := range map[string][]string{
		"pretty": {"Timeline", "before tracking", "Booked", "2026-11-08 16:46", "Tracking started", "1001 → 1002"},
		"table":  {"Timeline", "BOOKED", "Routing location changed", "1001 → 1002"},
		"json":   {`"timeline"`, `"key": "routing_location"`, `"from": "1001"`},
	} {
		var buf bytes.Buffer
		if err := Render(&buf, doc, format); err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("%s: missing %q", format, want)
			}
		}
	}
	if out := output(t, "pretty", false, nil); strings.Contains(out, "Timeline") {
		t.Error("no timeline section without milestones")
	}
}
