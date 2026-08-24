package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQAAPIReportsReadsAndMutations(t *testing.T) {
	mutationCount.Store(0)
	server := httptest.NewServer(handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/deliveries")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Dispatch-Provider") != "qa" {
		t.Fatalf("deliveries response = %s %v", response.Status, response.Header)
	}

	mutation, err := http.Post(server.URL+"/deliveries", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	mutation.Body.Close()
	if mutation.StatusCode != http.StatusCreated || mutationCount.Load() != 1 {
		t.Fatalf("mutation response = %s count=%d", mutation.Status, mutationCount.Load())
	}
}
