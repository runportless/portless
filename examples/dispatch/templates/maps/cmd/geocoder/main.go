package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"example.com/portless-dispatch-maps/internal/city"
	"example.com/portless-dispatch-maps/internal/web"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4102"
	}
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf(`service=geocoder event=ready port=%s`, port)
	log.Fatal(server.ListenAndServe())
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		web.JSON(writer, http.StatusOK, map[string]any{"service": "geocoder", "ready": true})
	})
	mux.HandleFunc("GET /locations", func(writer http.ResponseWriter, request *http.Request) {
		locations := city.Search(request.URL.Query().Get("query"))
		log.Printf(`service=geocoder event=search query=%q results=%d`, request.URL.Query().Get("query"), len(locations))
		web.JSON(writer, http.StatusOK, map[string]any{"locations": locations})
	})
	mux.HandleFunc("GET /locations/{code}", func(writer http.ResponseWriter, request *http.Request) {
		code := strings.TrimSpace(request.PathValue("code"))
		location, ok := city.Lookup(code)
		if !ok {
			web.WriteError(writer, http.StatusNotFound, "LOCATION_NOT_FOUND", "No dispatch location has code "+code)
			return
		}
		web.JSON(writer, http.StatusOK, location)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Dispatch-Service", "geocoder")
		mux.ServeHTTP(writer, request)
	})
}
