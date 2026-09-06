package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestTrafficSnapshotAndClearPreserveProjectionWatermarks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			if request.URL.Query().Get("edge") != "checkout:orders" || request.URL.Query().Get("limit") != "1" {
				t.Errorf("trace query = %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(writer, `{"traces":[],"revision":85,"throughSequence":73}`)
		case http.MethodDelete:
			_, _ = io.WriteString(writer, `{"cleared":0,"revision":86,"throughSequence":73}`)
		default:
			t.Errorf("unexpected method: %s", request.Method)
		}
	}))
	defer server.Close()
	client := New(server.URL, "test", server.Client())
	snapshot, err := client.TrafficTraces(t.Context(), "store", "local", contract.TrafficTraceQuery{Edge: "checkout:orders", Limit: 1})
	if err != nil || len(snapshot.Traces) != 0 || snapshot.Revision != 85 || snapshot.ThroughSequence != 73 {
		t.Fatalf("filtered snapshot = %#v %v", snapshot, err)
	}
	cleared, err := client.ClearTraffic(t.Context(), "store", "local")
	if err != nil || cleared.Revision != 86 || cleared.ThroughSequence != 73 {
		t.Fatalf("clear = %#v %v", cleared, err)
	}
}
