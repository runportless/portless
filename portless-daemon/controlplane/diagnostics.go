package controlplane

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/runtime/logstore"
)

// ManagedInventory summarizes active runtime resources owned by this daemon.
type ManagedInventory struct {
	Processes          int
	Containers         int
	ProxyListeners     int
	ActiveEnvironments int
	Problems           []string
}

// ReconciliationStatus describes the most recent startup runtime recovery.
type ReconciliationStatus struct {
	Result      string
	CompletedAt *time.Time
	Duration    time.Duration
	Recovered   int
	Problems    []string
}

// StorageStatus summarizes retained daemon data and its configured bounds.
type StorageStatus struct {
	DatabaseBytes                      int64
	RecordingCount                     int64
	RecordedEventCount                 int64
	RecordedBytes                      int64
	LiveTrafficExchanges               int
	LiveTrafficBytes                   int64
	ServiceLogBytes                    int64
	DaemonLogBytes                     int64
	TrafficExchangeLimitPerEnvironment int
	TrafficPayloadLimitPerEnvironment  int64
	RecordingDefaultEventLimit         int64
	RecordingMaximumEventLimit         int64
	RecordingDefaultPayloadLimit       int64
	RecordingMaximumPayloadLimit       int64
	ServiceLogGenerationLimit          int
	ServiceLogStreamLimitBytes         int64
	TrafficPrunedAt                    *time.Time
	ServiceLogsPrunedAt                *time.Time
	Problems                           []string
}

// OperationalDiagnostics is the control-plane-owned portion of one daemon
// diagnostics snapshot.
type OperationalDiagnostics struct {
	Inventory ManagedInventory
	Recovery  ReconciliationStatus
	Storage   *StorageStatus
}

// Diagnostics collects bounded operational metadata without probing runtime
// handoff safety or decoding retained application payloads.
func (s *Service) Diagnostics(ctx context.Context, includeStorage bool) OperationalDiagnostics {
	result := OperationalDiagnostics{Inventory: s.managedInventory(ctx), Recovery: s.reconciliationStatus()}
	if includeStorage {
		storage := s.storageStatus(ctx)
		result.Storage = &storage
	}
	return result
}

func (s *Service) managedInventory(ctx context.Context) ManagedInventory {
	result := ManagedInventory{ProxyListeners: s.proxy.ListenerCount(), Problems: []string{}}
	inventory, err := s.database.RuntimeInventory(ctx)
	if err != nil {
		result.Problems = append(result.Problems, "runtime inventory is unavailable: "+err.Error())
		return result
	}
	result.ActiveEnvironments = len(activeRuntimeEnvironments(inventory))
	for _, environment := range inventory {
		for _, runtime := range environment.Services {
			if s.daemonInstanceID != "" && runtime.OwnerInstanceID != s.daemonInstanceID {
				continue
			}
			switch {
			case runtime.ContainerName != "":
				result.Containers++
			case runtime.SupervisorSocket != "" && runtime.PrivateRunKey != "":
				result.Processes++
			}
		}
	}
	return result
}

func (s *Service) recordReconciliation(startedAt time.Time, report ReconciliationReport, err error) {
	completedAt := time.Now().UTC()
	result := "healthy"
	problems := append([]string(nil), report.Unverifiable...)
	if err != nil {
		result = "failed"
		problems = append(problems, err.Error())
	} else if len(problems) > 0 {
		result = "degraded"
	}
	s.recoveryMu.Lock()
	s.recovery = ReconciliationStatus{
		Result: result, CompletedAt: &completedAt, Duration: completedAt.Sub(startedAt),
		Recovered: len(report.Recovered), Problems: problems,
	}
	s.recoveryMu.Unlock()
}

func (s *Service) reconciliationStatus() ReconciliationStatus {
	s.recoveryMu.RLock()
	defer s.recoveryMu.RUnlock()
	result := s.recovery
	result.Problems = append([]string(nil), result.Problems...)
	if result.Problems == nil {
		result.Problems = []string{}
	}
	if result.CompletedAt != nil {
		value := *result.CompletedAt
		result.CompletedAt = &value
	}
	if result.Result == "" {
		result.Result = "not-run"
	}
	return result
}

func (s *Service) storageStatus(ctx context.Context) StorageStatus {
	traffic := s.traffic.RetentionStats()
	logs := logstore.Retention()
	result := StorageStatus{
		TrafficExchangeLimitPerEnvironment: traffic.ExchangeLimit,
		TrafficPayloadLimitPerEnvironment:  traffic.PayloadLimitBytes,
		RecordingDefaultEventLimit:         database.DefaultRecordingEventLimit,
		RecordingMaximumEventLimit:         maximumRecordingEventLimit,
		RecordingDefaultPayloadLimit:       database.DefaultRecordingPayloadLimit,
		RecordingMaximumPayloadLimit:       maximumRecordingPayloadLimit,
		ServiceLogGenerationLimit:          logs.RetainedRuns,
		ServiceLogStreamLimitBytes:         logs.MaxStreamBytes,
		LiveTrafficExchanges:               traffic.Exchanges, LiveTrafficBytes: traffic.PayloadBytes,
		TrafficPrunedAt: cloneTime(traffic.LastPrunedAt), ServiceLogsPrunedAt: cloneTime(logs.LastPrunedAt),
		Problems: []string{},
	}
	if recording, err := s.database.RecordingStorage(ctx); err != nil {
		result.Problems = append(result.Problems, "recording storage is unavailable: "+err.Error())
	} else {
		result.RecordingCount = recording.Recordings
		result.RecordedEventCount = recording.Events
		result.RecordedBytes = recording.Bytes
	}
	databaseBytes, err := fixedFileBytes(ctx,
		filepath.Join(s.dataDirectory, "portless.db"),
		filepath.Join(s.dataDirectory, "portless.db-wal"),
		filepath.Join(s.dataDirectory, "portless.db-shm"),
	)
	if err != nil {
		result.Problems = append(result.Problems, "database footprint is unavailable: "+err.Error())
	} else {
		result.DatabaseBytes = databaseBytes
	}
	if result.DaemonLogBytes, err = fixedFileBytes(ctx, filepath.Join(s.dataDirectory, "daemon.log")); err != nil {
		result.Problems = append(result.Problems, "daemon log footprint is unavailable: "+err.Error())
	}
	if result.ServiceLogBytes, err = environmentLogBytes(ctx, filepath.Join(s.dataDirectory, "environments")); err != nil {
		result.Problems = append(result.Problems, "service log footprint is unavailable: "+err.Error())
	}
	return result
}

func fixedFileBytes(ctx context.Context, paths ...string) (int64, error) {
	var total int64
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, errors.New("storage path is not a regular file")
		}
		total += info.Size()
	}
	return total, nil
}

func environmentLogBytes(ctx context.Context, root string) (int64, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		value, err := directoryBytes(ctx, filepath.Join(root, entry.Name(), "logs"))
		if err != nil {
			return 0, err
		}
		total += value
	}
	return total, nil
}

func directoryBytes(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) && path == root {
				return fs.SkipDir
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
