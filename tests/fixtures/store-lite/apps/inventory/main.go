package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"service": "inventory", "ready": true})
	})
	http.HandleFunc("/inventory/", func(writer http.ResponseWriter, request *http.Request) {
		sku := path.Base(request.URL.Path)
		quantity, _ := strconv.Atoi(request.URL.Query().Get("quantity"))
		available := sku != "sold-out" && quantity <= 10
		log.Printf("inventory checked sku=%s quantity=%d available=%t", sku, quantity, available)
		writeJSON(writer, map[string]any{"sku": sku, "quantity": quantity, "available": available})
	})
	log.Printf("inventory ready on %s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-E2E-Service", "inventory")
	_ = json.NewEncoder(writer).Encode(value)
}
