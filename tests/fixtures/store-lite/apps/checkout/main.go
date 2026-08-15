package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 5 * time.Second}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	inventory := strings.TrimRight(os.Getenv("INVENTORY_URL"), "/")
	orders := strings.TrimRight(os.Getenv("ORDERS_URL"), "/")

	http.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"service": "checkout", "ready": true})
	})
	http.HandleFunc("/checkout", func(writer http.ResponseWriter, request *http.Request) {
		sku := request.URL.Query().Get("sku")
		if sku == "" {
			sku = "coffee-mug"
		}
		quantity := request.URL.Query().Get("quantity")
		if quantity == "" {
			quantity = "1"
		}
		log.Printf("checkout requested sku=%s quantity=%s", sku, quantity)

		stock, err := getJSON(inventory + "/inventory/" + url.PathEscape(sku) + "?quantity=" + url.QueryEscape(quantity))
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "inventory: " + err.Error()})
			return
		}
		if available, _ := stock["available"].(bool); !available {
			writeJSON(writer, http.StatusConflict, map[string]any{"checkout": "rejected", "inventory": stock})
			return
		}
		order, err := getJSON(orders + "/orders?sku=" + url.QueryEscape(sku) + "&quantity=" + url.QueryEscape(quantity))
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "orders: " + err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"checkout": "accepted", "inventory": stock, "order": order})
	})

	log.Printf("checkout ready on %s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}

func getJSON(endpoint string) (map[string]any, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-E2E-Caller", "checkout")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("returned %s", response.Status)
	}
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-E2E-Service", "checkout")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
