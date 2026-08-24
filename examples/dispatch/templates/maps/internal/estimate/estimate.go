// Package estimate implements the deterministic Dispatch routing formula.
package estimate

import (
	"errors"
	"math"

	"example.com/portless-dispatch-maps/internal/city"
)

// Result is a route estimate returned to the operations API.
type Result struct {
	Pickup      string  `json:"pickup"`
	Destination string  `json:"destination"`
	DistanceKM  float64 `json:"distanceKm"`
	ETAMinutes  int     `json:"etaMinutes"`
	PriceCents  int     `json:"priceCents"`
	Strategy    string  `json:"strategy"`
}

// Calculate returns the stable standard-route estimate for two locations.
func Calculate(pickup, destination city.Location, size, priority string) (Result, error) {
	if pickup.Code == destination.Code {
		return Result{}, errors.New("pickup and destination must differ")
	}
	sizeSurcharge := map[string]int{"small": 0, "medium": 250, "large": 600}
	surcharge, ok := sizeSurcharge[size]
	if !ok {
		return Result{}, errors.New("size must be small, medium, or large")
	}
	if priority != "standard" && priority != "express" {
		return Result{}, errors.New("priority must be standard or express")
	}

	distance := math.Hypot(destination.X-pickup.X, destination.Y-pickup.Y) * 1.4
	distance = math.Round(distance*10) / 10
	eta := int(math.Ceil(distance / 25 * 60))
	price := 450 + int(math.Round(distance*120)) + surcharge
	if priority == "express" {
		eta = int(math.Ceil(float64(eta) * 0.72))
		price = int(math.Round(float64(price) * 1.35))
	}
	return Result{
		Pickup: pickup.Code, Destination: destination.Code, DistanceKM: distance,
		ETAMinutes: eta, PriceCents: price, Strategy: "standard",
	}, nil
}
