package health

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func Wait(ctx context.Context, port int, check model.HealthCheck) error {
	if check.Timeout <= 0 {
		check.Timeout = 90 * time.Second
	}
	if check.Interval <= 0 {
		check.Interval = time.Second
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, check.Timeout)
	defer cancel()
	ticker := time.NewTicker(check.Interval)
	defer ticker.Stop()
	var lastError error
	for {
		if err := probe(deadlineCtx, port, check); err == nil {
			return nil
		} else {
			lastError = err
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("health check timed out: %w", lastError)
		case <-ticker.C:
		}
	}
}

func probe(ctx context.Context, port int, check model.HealthCheck) error {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if check.Kind != "http" {
		connection, err := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		return connection.Close()
	}
	path := check.Path
	if path == "" {
		path = "/"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
