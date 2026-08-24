package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

var mutationCount atomic.Int64

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "19090"
	}
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("dispatch QA API ready on http://127.0.0.1:%s", port)
	log.Fatal(server.ListenAndServe())
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"service": "api", "ready": true, "provider": "qa"})
	})
	mux.HandleFunc("GET /locations", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"locations": []map[string]any{
			{"code": "central-depot", "name": "Central Depot", "x": 5, "y": 5, "zone": "central"},
			{"code": "harbor", "name": "Harbor", "x": 9, "y": 8, "zone": "east"},
		}})
	})
	mux.HandleFunc("GET /estimates", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"pickup": request.URL.Query().Get("pickup"), "destination": request.URL.Query().Get("destination"),
			"distanceKm": 9.8, "etaMinutes": 31, "priceCents": 2240, "strategy": "qa-assisted",
		})
	})
	mux.HandleFunc("GET /deliveries", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"provider": "qa", "deliveries": []map[string]any{
			{"id": "QA-1042", "pickup": "central-depot", "destination": "harbor", "parcelSize": "medium", "priority": "standard", "distanceKm": 9.8, "etaMinutes": 31, "priceCents": 2240, "routeStrategy": "qa-assisted", "status": "assigned", "createdAt": "2026-08-23T12:00:00Z", "updatedAt": "2026-08-23T12:10:00Z"},
		}})
	})
	mux.HandleFunc("GET /stats", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"mutations": mutationCount.Load()})
	})
	mux.HandleFunc("POST /{path...}", recordMutation)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Dispatch-Service", "api")
		writer.Header().Set("X-Dispatch-Provider", "qa")
		mux.ServeHTTP(writer, request)
	})
}

func recordMutation(writer http.ResponseWriter, _ *http.Request) {
	count := mutationCount.Add(1)
	writeJSON(writer, http.StatusCreated, map[string]string{"unexpectedMutation": strconv.FormatInt(count, 10)})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
