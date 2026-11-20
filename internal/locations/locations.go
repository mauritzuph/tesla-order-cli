// Package locations looks up Tesla location IDs (the vehicle routing location)
// in locations.json in the data folder. There is no built-in list: Tesla's
// locations API blocks scripts, and the lists floating around have no license.
//
// The file maps an ID to a name, or to an object in the shape Tesla's
// locations API uses:
//
//	{"1234": {"display_name": "Berlin-Schönefeld", "address": {"address_1": "...", "city": "...", "postal_code": "..."}, "phone": "..."}}
package locations

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FileName is the optional lookup file in the data folder.
const FileName = "locations.json"

type Location struct {
	Name    string
	Address string // "street, postal code city"
	Phone   string
}

type entry struct {
	DisplayName string `json:"display_name"`
	Address     struct {
		Street     string `json:"address_1"`
		City       string `json:"city"`
		PostalCode string `json:"postal_code"`
	} `json:"address"`
	Phone string `json:"phone"`
}

var known map[string]Location

// Lookup returns the location with the given ID (a string or JSON number).
func Lookup(id any) (Location, bool) {
	var key string
	switch x := id.(type) {
	case string:
		key = strings.TrimSpace(x)
	case float64:
		key = fmt.Sprintf("%.0f", x)
	default:
		return Location{}, false
	}
	l, ok := known[key]
	return l, ok
}

// Load reads path. A missing file is fine and clears an earlier one.
func Load(path string) error {
	known = nil
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	known = make(map[string]Location, len(file))
	for id, v := range file {
		var name string
		if json.Unmarshal(v, &name) == nil {
			known[id] = Location{Name: name}
			continue
		}
		var e entry
		if err := json.Unmarshal(v, &e); err != nil {
			known = nil
			return fmt.Errorf("%s: location %s: %w", path, id, err)
		}
		city := strings.TrimSpace(e.Address.PostalCode + " " + e.Address.City)
		known[id] = Location{Name: e.DisplayName, Address: join(", ", e.Address.Street, city), Phone: e.Phone}
	}
	return nil
}

func join(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
