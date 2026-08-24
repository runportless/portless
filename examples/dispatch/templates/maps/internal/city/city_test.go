package city

import "testing"

func TestLookupAndSearch(t *testing.T) {
	location, ok := Lookup(" Central-Depot ")
	if !ok || location.Name != "Central Depot" || location.Zone != "central" {
		t.Fatalf("central depot = %#v, %t", location, ok)
	}
	if _, ok := Lookup("missing"); ok {
		t.Fatal("missing location was found")
	}
	west := Search("west")
	if len(west) != 3 || west[0].Code != "industrial-park" || west[2].Code != "west-hospital" {
		t.Fatalf("west search = %#v", west)
	}
	if all := Search(""); len(all) != 12 {
		t.Fatalf("all locations = %d", len(all))
	}
}
