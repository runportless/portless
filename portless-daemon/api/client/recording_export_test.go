package client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordingExportStreamsBeyondBufferedLimitAndReportsTruncation(t *testing.T) {
	body := []byte(`{"data":"` + strings.Repeat("a", 17<<20) + `"}`)
	for _, truncated := range []bool{false, true} {
		t.Run(fmt.Sprint(truncated), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" || r.URL.Path != "/api/v1/environments/shop/local/recordings/capture/export" {
					t.Errorf("unexpected export request: %s", r.URL)
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				if truncated {
					_, _ = w.Write(body[:100])
					return
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client := New(server.URL, "token", server.Client())
			var output bytes.Buffer
			err := client.WriteRecordingExport(t.Context(), "shop", "local", "capture", &output)
			if truncated {
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("truncated stream=%v", err)
				}
			} else if err != nil || !bytes.Equal(output.Bytes(), body) {
				t.Fatalf("stream incomplete: %d %v", output.Len(), err)
			}
		})
	}
}
