package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	http.HandleFunc("/api/orders", echoRequest)
	http.HandleFunc("/auth/login", echoRequest)
	http.HandleFunc("/browser-policy", browserPolicy)
	http.HandleFunc("/browser-policy/frame", browserPolicy)
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

		stock, err := getJSON(inventory+"/inventory/"+url.PathEscape(sku)+"?quantity="+url.QueryEscape(quantity), request.Header)
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "inventory: " + err.Error()})
			return
		}
		if available, _ := stock["available"].(bool); !available {
			writeJSON(writer, http.StatusConflict, map[string]any{"checkout": "rejected", "inventory": stock})
			return
		}
		order, err := getJSON(orders+"/orders?sku="+url.QueryEscape(sku)+"&quantity="+url.QueryEscape(quantity), request.Header)
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "orders: " + err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"checkout": "accepted", "inventory": stock, "order": order})
	})

	log.Printf("checkout ready on %s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}

func echoRequest(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 4096))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"service": "checkout", "method": request.Method, "path": request.URL.Path,
		"query": request.URL.RawQuery, "body": string(body), "header": request.Header.Get("X-E2E-Application"),
	})
}

func browserPolicy(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; frame-src 'self'; frame-ancestors 'self'")
	writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
	writer.Header().Set("Referrer-Policy", "origin")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.URL.Path == "/browser-policy/frame" {
		_, _ = io.WriteString(writer, "<!doctype html><html lang=\"en\"><title>Application frame</title><p>Application frame loaded</p></html>")
		return
	}
	_, _ = io.WriteString(writer, `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Application browser policy</title>
<p id="script-result">Waiting for application script</p>
<script>document.getElementById('script-result').textContent = 'Application script ran'</script>
<iframe title="Application frame" src="/browser-policy/frame"></iframe>
</html>`)
}

func getJSON(endpoint string, incoming http.Header) (map[string]any, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-E2E-Caller", "checkout")
	for _, name := range []string{
		"Traceparent", "Tracestate", "Baggage", "B3",
		"X-B3-TraceId", "X-B3-SpanId", "X-B3-ParentSpanId", "X-B3-Sampled", "X-B3-Flags",
		"X-Datadog-Trace-Id", "X-Datadog-Parent-Id", "X-Datadog-Sampling-Priority", "X-Datadog-Origin", "X-Datadog-Tags",
	} {
		for _, value := range incoming.Values(name) {
			request.Header.Add(name, value)
		}
	}
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
