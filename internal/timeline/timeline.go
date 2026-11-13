// Package timeline records when logistics milestones of an order were first
// seen: VIN assigned, routing location changed, vehicle at the delivery
// center, ... The API only has the current state, so the dates are the checks
// that noticed the change, not the moment it happened.
package timeline

import (
	"reflect"

	"github.com/mauritzuph/tesla-order-cli/internal/summary"
)

// Milestone is one entry. At is RFC 3339; empty means it was already the case
// when tracking started.
type Milestone struct {
	Key   string `json:"key" yaml:"key"`
	Label string `json:"label" yaml:"label"`
	At    string `json:"at,omitempty" yaml:"at,omitempty"`
	From  any    `json:"from,omitempty" yaml:"from,omitempty"`
	To    any    `json:"to,omitempty" yaml:"to,omitempty"`
}

// Timelines are the milestones of all orders, keyed by reference number.
type Timelines map[string][]Milestone

const KeyTrackingStarted = "tracking_started"

type watched struct {
	section, key string
	set, changed string // labels for "first got a value" and "value changed"
}

// Watched summary fields, in the order a delivery usually goes through them.
var fields = []watched{
	{"order", "status", "Status", "Status changed"},
	{"order", "vin", "VIN assigned", "VIN changed"},
	{"delivery", "routing_location", "Routing location assigned", "Routing location changed"},
	{"delivery", "window", "Delivery window announced", "Delivery window changed"},
	{"delivery", "eta_to_delivery_center", "ETA to delivery center", "ETA to delivery center changed"},
	{"delivery", "vehicle_located_at", "Vehicle located", "Vehicle location updated"},
	{"delivery", "vehicle_ready", "Vehicle ready", "Vehicle ready changed"},
	{"delivery", "appointment", "Appointment scheduled", "Appointment changed"},
}

// Start is the timeline of an order seen for the first time: what is already
// set (without a date), then "Tracking started".
func Start(cur summary.Summary, now string) []Milestone {
	var out []Milestone
	for _, f := range fields {
		if v := cur.Value(f.section, f.key); !blank(v) {
			out = append(out, Milestone{Key: f.key, Label: f.set, To: v})
		}
	}
	return append(out, Milestone{Key: KeyTrackingStarted, Label: "Tracking started", At: now})
}

// Detect returns the milestones between two summaries of the same order.
func Detect(prev, cur summary.Summary, now string) []Milestone {
	var out []Milestone
	for _, f := range fields {
		old, v := prev.Value(f.section, f.key), cur.Value(f.section, f.key)
		switch {
		case blank(old) && blank(v), reflect.DeepEqual(old, v):
		case blank(old):
			out = append(out, Milestone{Key: f.key, Label: f.set, At: now, To: v})
		default:
			out = append(out, Milestone{Key: f.key, Label: f.changed, At: now, From: old, To: v})
		}
	}
	return out
}

// Update adds the milestones of all current orders to t and reports whether
// anything was added. previous holds the summaries of the last snapshot.
func Update(t Timelines, previous, current map[string]summary.Summary, now string) bool {
	added := false
	for ref, cur := range current {
		var ms []Milestone
		if _, known := t[ref]; !known {
			ms = Start(cur, now)
		} else {
			ms = Detect(previous[ref], cur, now)
		}
		if len(ms) > 0 {
			t[ref] = append(t[ref], ms...)
			added = true
		}
	}
	return added
}

// blank treats false like a missing value, so "vehicle ready: no -> yes" is
// the moment it became ready.
func blank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	}
	return false
}
