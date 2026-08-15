package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}
	http.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"service": "orders", "ready": true})
	})
	http.HandleFunc("/orders", func(writer http.ResponseWriter, request *http.Request) {
		quantity, _ := strconv.Atoi(request.URL.Query().Get("quantity"))
		sku := request.URL.Query().Get("sku")
		log.Printf("order created sku=%s quantity=%d", sku, quantity)
		writeJSON(writer, map[string]any{"number": 42, "state": "created", "sku": sku, "quantity": quantity})
	})
	log.Printf("orders ready on %s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-E2E-Service", "orders")
	_ = json.NewEncoder(writer).Encode(value)
}
