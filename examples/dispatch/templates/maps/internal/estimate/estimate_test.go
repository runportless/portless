package estimate

import (
	"testing"

	"example.com/portless-dispatch-maps/internal/city"
)

func TestCalculateIsDeterministic(t *testing.T) {
	pickup, _ := city.Lookup("central-depot")
	destination, _ := city.Lookup("harbor")
	standard, err := Calculate(pickup, destination, "medium", "standard")
	if err != nil {
		t.Fatal(err)
	}
	if standard.DistanceKM != 7 || standard.ETAMinutes != 17 || standard.PriceCents != 1540 || standard.Strategy != "standard" {
		t.Fatalf("standard estimate = %#v", standard)
	}
	express, err := Calculate(pickup, destination, "medium", "express")
	if err != nil {
		t.Fatal(err)
	}
	if express.ETAMinutes != 13 || express.PriceCents != 2079 {
		t.Fatalf("express estimate = %#v", express)
	}
}

func TestCalculateRejectsInvalidInputs(t *testing.T) {
	location, _ := city.Lookup("central-depot")
	other, _ := city.Lookup("harbor")
	for _, test := range []struct{ size, priority string }{{"tiny", "standard"}, {"small", "urgent"}} {
		if _, err := Calculate(location, other, test.size, test.priority); err == nil {
			t.Fatalf("Calculate(%q, %q) succeeded", test.size, test.priority)
		}
	}
	if _, err := Calculate(location, location, "small", "standard"); err == nil {
		t.Fatal("same-location estimate succeeded")
	}
}
