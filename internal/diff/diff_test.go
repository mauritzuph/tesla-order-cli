package diff

import (
	"reflect"
	"strings"
	"testing"
)

func TestNoChanges(t *testing.T) {
	data := map[string]any{"a": map[string]any{"b": []any{1.0, map[string]any{"c": 2.0}}}}
	if got := Diff(data, data, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestNestedChangesSortedByPath(t *testing.T) {
	old := map[string]any{"RN1": map[string]any{"order": map[string]any{"status": "BOOKED", "vin": nil}, "gone": 1.0}}
	new := map[string]any{"RN1": map[string]any{"order": map[string]any{"status": "DELIVERED", "vin": "5YJ"}, "new": true}}
	want := []Change{
		{Removed, "RN1.gone", 1.0, nil},
		{Added, "RN1.new", nil, true},
		{Changed, "RN1.order.status", "BOOKED", "DELIVERED"},
		{Changed, "RN1.order.vin", nil, "5YJ"},
	}
	if got := Diff(old, new, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestListsByIndexAndTypeChange(t *testing.T) {
	got := Diff(map[string]any{"x": []any{1.0, 2.0}}, map[string]any{"x": []any{1.0, 3.0, 4.0}}, nil)
	want := []Change{{Changed, "x[1]", 2.0, 3.0}, {Added, "x[2]", nil, 4.0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	got = Diff(map[string]any{"x": map[string]any{"a": 1.0}}, map[string]any{"x": "flat"}, nil)
	if len(got) != 1 || got[0].Kind != Changed || got[0].Path != "x" {
		t.Fatalf("type change: %v", got)
	}
}

func TestIgnoreGlobs(t *testing.T) {
	old := map[string]any{"RN1": map[string]any{"meta": map[string]any{"lastUpdated": 1.0}, "status": "A"}}
	new := map[string]any{"RN1": map[string]any{"meta": map[string]any{"lastUpdated": 2.0}, "status": "B"}}
	got := Diff(old, new, []string{"*.lastUpdated"})
	if len(got) != 1 || got[0].Path != "RN1.status" {
		t.Fatalf("got %v", got)
	}
	if !Glob("*.details.strings*").MatchString("RN1.details.strings.back[0]") {
		t.Error("glob should match across dots and brackets")
	}
}

func TestChangeString(t *testing.T) {
	cases := map[string]Change{
		`~ a: "x" -> 2`:  {Changed, "a", "x", 2.0},
		"+ a: 1":         {Added, "a", nil, 1.0},
		"- a (was true)": {Removed, "a", true, nil},
	}
	for want, c := range cases {
		if c.String() != want {
			t.Errorf("got %q, want %q", c.String(), want)
		}
	}
	long := Change{Added, "a", nil, strings.Repeat("y", 500)}.String()
	if len([]rune(long)) > 130 || !strings.HasSuffix(long, "…") {
		t.Errorf("long value not truncated: %d", len(long))
	}
}
