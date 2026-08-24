package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingHandlerPropagatesTraceContext(t *testing.T) {
	var traceparents []string
	geocoder := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		traceparents = append(traceparents, request.Header.Get("traceparent"))
		code := strings.TrimPrefix(request.URL.Path, "/locations/")
		coordinate := "5"
		if code == "harbor" {
			coordinate = "9"
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":"` + code + `","name":"Location","x":` + coordinate + `,"y":5,"zone":"central"}`))
	}))
	defer geocoder.Close()

	request := httptest.NewRequest(http.MethodGet, "/estimates?pickup=central-depot&destination=harbor&size=small&priority=standard", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	handler(geocoderClient{baseURL: geocoder.URL, client: geocoder.Client()}).ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"strategy":"standard"`) {
		t.Fatalf("estimate response: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(traceparents) != 2 || traceparents[0] != request.Header.Get("traceparent") || traceparents[1] != request.Header.Get("traceparent") {
		t.Fatalf("traceparents = %#v", traceparents)
	}
}
