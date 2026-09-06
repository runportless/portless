package controlplane

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestRemoteWritePolicyChangeClosesAnOpenWebSocket(t *testing.T) {
	ctx := t.Context()
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := New(db, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(context.Background())
	root := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	if _, _, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: root}}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(204)
			return
		}
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\nready")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, rw.Reader)
	}))
	defer upstream.Close()
	binding := model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite, HealthPath: "/health"}}
	operation, err := app.ChangeBinding(ctx, "billing", "local", "checkout", binding, "test", "remote-rw")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("provider operation %#v", operation)
	}
	operation, err = app.Up(ctx, "billing", "local", "test", "start", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("up %#v", operation)
	}
	ingress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.ServeIngress(w, r, "billing/local", "checkout") }))
	defer ingress.Close()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, ingress.URL+"/ws", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 101 {
		t.Fatal(response.Status)
	}
	if _, err := io.ReadFull(response.Body, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	remote := *binding.Remote
	remote.WritePolicy = model.WriteReadOnly
	binding.Remote = &remote
	operation, err = app.ChangeBinding(ctx, "billing", "local", "checkout", binding, "test", "remote-ro")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("policy operation %#v", operation)
	}
	if _, err := response.Body.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("old connection did not close at policy change: %v", err)
	}
	denied, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatalf("new upgrade returned %d", denied.StatusCode)
	}
}
