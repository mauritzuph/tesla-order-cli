// Package options turns Tesla option codes (order.mktOptions) into readable
// labels. The built-in list only has codes whose meaning is well known; others
// stay as codes. Users can add or correct labels in option-codes.json in the
// data folder.
package options

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FileName is the optional override file in the data folder: {"CODE": "label"}.
const FileName = "option-codes.json"

var builtin = map[string]string{
	"MDL3": "Model 3",
	"MDLY": "Model Y",
	"MDLS": "Model S",
	"MDLX": "Model X",

	"MTY47": "Model Y Long Range Dual Motor",

	"PPSW": "Paint: Pearl White Multi-Coat",
	"PBSB": "Paint: Solid Black",
	"PMNG": "Paint: Midnight Silver Metallic",
	"PPSB": "Paint: Deep Blue Metallic",
	"PPMR": "Paint: Red Multi-Coat",
	"PN00": "Paint: Quicksilver",
	"PN01": "Paint: Stealth Grey",
	"PR01": "Paint: Ultra Red",

	"IPW8": "Interior: Black and White",

	"W38B":  `Wheels: 18" Aero`,
	"W39B":  `Wheels: 19" Sport`,
	"WY19B": `Wheels: 19" Gemini`,
	"WY19P": `Wheels: 19" Crossflow`,
	"WY20P": `Wheels: 20" Induction`,
	"WY21P": `Wheels: 21" Überturbine`,

	"STY5S": "Seating: 5 seats",
	"STY7S": "Seating: 7 seats",

	"APBS": "Autopilot",
	"APPB": "Enhanced Autopilot",
	"APF2": "Full Self-Driving Capability",

	"SC04": "Pay-per-use Supercharging",
	"CPF0": "Standard Connectivity",
	"CPF1": "Premium Connectivity",
	"TW01": "Tow hitch",
}

var overrides map[string]string

// Label returns the label for code, or "" if it is not known.
func Label(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if l, ok := overrides[code]; ok {
		return l
	}
	return builtin[code]
}

// LoadOverrides reads labels from path; they win over the built-in ones. A
// missing file is fine and clears earlier overrides.
func LoadOverrides(path string) error {
	overrides = nil
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var labels map[string]string
	if err := json.Unmarshal(raw, &labels); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	overrides = make(map[string]string, len(labels))
	for code, label := range labels {
		overrides[strings.ToUpper(strings.TrimSpace(code))] = label
	}
	return nil
}
