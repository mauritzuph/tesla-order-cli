package timeline

import (
	"reflect"
	"testing"

	"github.com/mauritzuph/tesla-order-cli/internal/summary"
)

func order(vin any, routing any, ready any) summary.Summary {
	return summary.Summarize(map[string]any{
		"order": map[string]any{"referenceNumber": "RN1", "orderStatus": "BOOKED", "vin": vin},
		"details": map[string]any{"tasks": map[string]any{
			"registration":       map[string]any{"orderDetails": map[string]any{"vehicleRoutingLocation": routing}},
			"deliveryAcceptance": map[string]any{"vehicleIsReady": ready},
		}},
	})
}

func TestStartMarksExistingStateAsBeforeTracking(t *testing.T) {
	got := Start(order("LRW3F7FS1PC000042", nil, false), "T0")
	want := []Milestone{
		{Key: "status", Label: "Status", To: "BOOKED"},
		{Key: "vin", Label: "VIN assigned", To: "LRW3F7FS1PC000042"},
		{Key: KeyTrackingStarted, Label: "Tracking started", At: "T0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestDetect(t *testing.T) {
	got := Detect(order(nil, "1001", false), order("LRW3F7FS1PC000042", "1002", true), "T1")
	want := []Milestone{
		{Key: "vin", Label: "VIN assigned", At: "T1", To: "LRW3F7FS1PC000042"},
		{Key: "routing_location", Label: "Routing location changed", At: "T1", From: "1001", To: "1002"},
		{Key: "vehicle_ready", Label: "Vehicle ready", At: "T1", To: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if got := Detect(order("V", "1", true), order("V", "1", true), "T2"); len(got) != 0 {
		t.Errorf("unchanged order: %+v", got)
	}
}

func TestUpdate(t *testing.T) {
	tl := Timelines{}
	cur := map[string]summary.Summary{"RN1": order(nil, nil, nil)}
	if !Update(tl, nil, cur, "T0") || len(tl["RN1"]) != 2 {
		t.Fatalf("first update: %+v", tl)
	}
	if Update(tl, cur, cur, "T1") {
		t.Errorf("nothing changed but added: %+v", tl)
	}
	next := map[string]summary.Summary{"RN1": order("LRW3F7FS1PC000042", nil, nil)}
	if !Update(tl, cur, next, "T2") || tl["RN1"][2].Key != "vin" || tl["RN1"][2].At != "T2" {
		t.Errorf("got %+v", tl)
	}
}
