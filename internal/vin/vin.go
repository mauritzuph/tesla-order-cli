// Package vin decodes the parts of a Tesla VIN that are stable across models
// and years. Codes that are not known are left empty instead of guessed.
//
// Layout: 1-3 manufacturer, 4 model line, 5 body type (includes steering side),
// 6-8 restraints/fuel/drive unit, 9 check digit, 10 model year, 11 factory,
// 12-17 serial number.
package vin

import "strings"

// Info is what could be decoded; unknown parts are zero.
type Info struct {
	Factory        string
	FactoryCountry string // ISO code, e.g. "DE"
	ModelYear      int
	Steering       string
	Serial         string
}

type factory struct{ name, country string }

var factories = map[byte]factory{
	'A': {"Austin, USA", "US"}, 'B': {"Berlin, Germany", "DE"}, 'C': {"Shanghai, China", "CN"}, 'F': {"Fremont, USA", "US"},
}

// Model year letters (position 10) repeat every 30 years; this is the
// 2010-2039 cycle.
const yearCodes = "ABCDEFGHJKLMNPRSTVWXY123456789"

// Body type (position 5) per model line (position 4), left- and right-hand drive.
var bodyTypes = map[byte][2]byte{'S': {'A', 'B'}, 'X': {'C', 'D'}, '3': {'E', 'F'}, 'Y': {'G', 'H'}}

// Decode returns the decoded parts; ok is false for anything that is not a
// 17-character VIN.
func Decode(v string) (info Info, ok bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if len(v) != 17 {
		return Info{}, false
	}
	f := factories[v[10]]
	info.Factory, info.FactoryCountry = f.name, f.country
	if i := strings.IndexByte(yearCodes, v[9]); i >= 0 {
		info.ModelYear = 2010 + i
	}
	if body, ok := bodyTypes[v[3]]; ok {
		switch v[4] {
		case body[0]:
			info.Steering = "Left-hand drive"
		case body[1]:
			info.Steering = "Right-hand drive"
		}
	}
	info.Serial = v[11:]
	return info, true
}
