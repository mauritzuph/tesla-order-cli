package vin

import "testing"

func TestDecode(t *testing.T) {
	for v, want := range map[string]Info{
		"LRW3F7FS1PC000001": {Factory: "Shanghai, China", FactoryCountry: "CN", ModelYear: 2023, Steering: "Right-hand drive", Serial: "000001"},
		"XP7YGCEK2RB123456": {Factory: "Berlin, Germany", FactoryCountry: "DE", ModelYear: 2024, Steering: "Left-hand drive", Serial: "123456"},
		"5YJ3E1EA1JF000001": {Factory: "Fremont, USA", FactoryCountry: "US", ModelYear: 2018, Steering: "Left-hand drive", Serial: "000001"},
		"7SAYGDEE1TA000001": {Factory: "Austin, USA", FactoryCountry: "US", ModelYear: 2026, Steering: "Left-hand drive", Serial: "000001"},
		"5yjsa1e2xff000001": {Factory: "Fremont, USA", FactoryCountry: "US", ModelYear: 2015, Steering: "Left-hand drive", Serial: "000001"},
		// unknown factory, model line and year code stay empty
		"7G2CEHED6IZ000001": {Serial: "000001"},
	} {
		got, ok := Decode(v)
		if !ok || got != want {
			t.Errorf("%s: got %+v %v, want %+v", v, got, ok, want)
		}
	}
}

func TestDecodeRejectsIncomplete(t *testing.T) {
	for _, v := range []string{"", "5YJ3E", "5YJ3E1EA1JF0000012"} {
		if _, ok := Decode(v); ok {
			t.Errorf("%q decoded", v)
		}
	}
}
