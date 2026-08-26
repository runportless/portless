package daemon

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	replacementDrainTimeout   = 500 * time.Millisecond
	replacementCleanupTimeout = 500 * time.Millisecond
	ordinaryShutdownTimeout   = 10 * time.Second
)

func shutdownHTTPServers(stopServing context.CancelFunc, timeout time.Duration, servers ...*http.Server) error {
	stopServing()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	errorsByServer := make(chan error, len(servers))
	var wait sync.WaitGroup
	for _, server := range servers {
		wait.Add(1)
		go func(server *http.Server) {
			defer wait.Done()
			errorsByServer <- server.Shutdown(ctx)
		}(server)
	}
	wait.Wait()
	close(errorsByServer)
	var result error
	for err := range errorsByServer {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	}
	if ctx.Err() == nil {
		return result
	}
	for _, server := range servers {
		if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}
