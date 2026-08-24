package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeocoderHandler(t *testing.T) {
	response := httptest.NewRecorder()
	handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/locations?query=harbor", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":"harbor"`) || response.Header().Get("X-Dispatch-Service") != "geocoder" {
		t.Fatalf("search response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}

	missing := httptest.NewRecorder()
	handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/locations/missing", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "LOCATION_NOT_FOUND") {
		t.Fatalf("missing response: status=%d body=%s", missing.Code, missing.Body.String())
	}
}
