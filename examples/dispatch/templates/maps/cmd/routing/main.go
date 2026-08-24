package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"example.com/portless-dispatch-maps/internal/city"
	"example.com/portless-dispatch-maps/internal/estimate"
	"example.com/portless-dispatch-maps/internal/web"
)

type geocoderClient struct {
	baseURL string
	client  *http.Client
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4101"
	}
	geocoderURL := strings.TrimRight(os.Getenv("GEOCODER_URL"), "/")
	if geocoderURL == "" {
		geocoderURL = "http://127.0.0.1:4102"
	}
	server := &http.Server{
		Addr:              "127.0.0.1:" + port,
		Handler:           handler(geocoderClient{baseURL: geocoderURL, client: &http.Client{Timeout: 3 * time.Second}}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf(`service=routing event=ready port=%s`, port)
	log.Fatal(server.ListenAndServe())
}

func handler(geocoder geocoderClient) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		web.JSON(writer, http.StatusOK, map[string]any{"service": "routing", "ready": true, "strategy": "standard"})
	})
	mux.HandleFunc("GET /estimates", func(writer http.ResponseWriter, request *http.Request) {
		pickupCode := request.URL.Query().Get("pickup")
		destinationCode := request.URL.Query().Get("destination")
		pickup, err := geocoder.lookup(request.Context(), request, pickupCode)
		if err != nil {
			web.WriteError(writer, http.StatusBadGateway, "GEOCODER_UNAVAILABLE", err.Error())
			return
		}
		destination, err := geocoder.lookup(request.Context(), request, destinationCode)
		if err != nil {
			web.WriteError(writer, http.StatusBadGateway, "GEOCODER_UNAVAILABLE", err.Error())
			return
		}
		result, err := estimate.Calculate(pickup, destination, request.URL.Query().Get("size"), request.URL.Query().Get("priority"))
		if err != nil {
			web.WriteError(writer, http.StatusUnprocessableEntity, "INVALID_ESTIMATE", err.Error())
			return
		}
		log.Printf(`service=routing event=estimated pickup=%s destination=%s distance_km=%.1f`, pickup.Code, destination.Code, result.DistanceKM)
		writer.Header().Set("X-Route-Strategy", result.Strategy)
		web.JSON(writer, http.StatusOK, result)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Dispatch-Service", "routing")
		mux.ServeHTTP(writer, request)
	})
}

func (client geocoderClient) lookup(ctx context.Context, inbound *http.Request, code string) (city.Location, error) {
	endpoint := client.baseURL + "/locations/" + url.PathEscape(code)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return city.Location{}, fmt.Errorf("build geocoder request: %w", err)
	}
	request.Header = web.TraceHeaders(inbound)
	response, err := client.client.Do(request)
	if err != nil {
		return city.Location{}, fmt.Errorf("lookup %s: %w", code, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return city.Location{}, fmt.Errorf("lookup %s returned %s", code, response.Status)
	}
	var location city.Location
	if err := json.NewDecoder(response.Body).Decode(&location); err != nil {
		return city.Location{}, fmt.Errorf("decode location %s: %w", code, err)
	}
	return location, nil
}
