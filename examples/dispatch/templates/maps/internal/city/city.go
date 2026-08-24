// Package city owns the deterministic Dispatch example location catalog.
package city

import (
	"sort"
	"strings"
)

// Location is one address-like point in the example city.
type Location struct {
	Code string  `json:"code"`
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zone string  `json:"zone"`
}

var locations = []Location{
	{Code: "airport", Name: "Airport", X: 9, Y: 2, Zone: "east"},
	{Code: "central-depot", Name: "Central Depot", X: 5, Y: 5, Zone: "central"},
	{Code: "convention-center", Name: "Convention Center", X: 6, Y: 7, Zone: "central"},
	{Code: "harbor", Name: "Harbor", X: 9, Y: 8, Zone: "east"},
	{Code: "industrial-park", Name: "Industrial Park", X: 2, Y: 8, Zone: "west"},
	{Code: "market-square", Name: "Market Square", X: 5, Y: 6, Zone: "central"},
	{Code: "museum", Name: "Museum", X: 4, Y: 4, Zone: "central"},
	{Code: "north-station", Name: "North Station", X: 5, Y: 1, Zone: "north"},
	{Code: "riverside", Name: "Riverside", X: 8, Y: 6, Zone: "east"},
	{Code: "south-terminal", Name: "South Terminal", X: 5, Y: 10, Zone: "south"},
	{Code: "university", Name: "University", X: 3, Y: 3, Zone: "west"},
	{Code: "west-hospital", Name: "West Hospital", X: 1, Y: 5, Zone: "west"},
}

// All returns a defensive copy of the location catalog.
func All() []Location {
	return append([]Location(nil), locations...)
}

// Lookup finds a location by stable code.
func Lookup(code string) (Location, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, location := range locations {
		if location.Code == code {
			return location, true
		}
	}
	return Location{}, false
}

// Search returns locations whose code, name, or zone contains query.
func Search(query string) []Location {
	query = strings.ToLower(strings.TrimSpace(query))
	var result []Location
	for _, location := range locations {
		candidate := strings.ToLower(location.Code + " " + location.Name + " " + location.Zone)
		if query == "" || strings.Contains(candidate, query) {
			result = append(result, location)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
