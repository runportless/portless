package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	pathmatch "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/runtime/logstore"
)

// TrafficExchanges returns the most recent in-memory traffic exchanges for an environment.
func (s *Service) TrafficExchanges(project, environment string, limit int) []model.TrafficExchange {
	return s.traffic.RecentExchanges(model.EnvironmentSelector(project, environment), limit)
}

// TrafficExchange finds one exchange in live or recorded history by sequence number.
func (s *Service) TrafficExchange(ctx context.Context, project, environment string, sequence int64) (model.TrafficExchange, error) {
	if exchange, ok := s.traffic.Exchange(model.EnvironmentSelector(project, environment), sequence); ok {
		return exchange, nil
	}
	recorded, err := s.database.RecordedTraffic(ctx, model.EnvironmentSelector(project, environment), "", 10_000)
	if err != nil {
		return model.TrafficExchange{}, err
	}
	for _, exchange := range recorded {
		if exchange.Sequence == sequence {
			return exchange, nil
		}
	}
	return model.TrafficExchange{}, database.ErrNotFound
}

// RecordedTraffic returns persisted traffic, optionally limited to one recording.
func (s *Service) RecordedTraffic(ctx context.Context, project, environment, recording string, limit int) ([]model.TrafficExchange, error) {
	return s.database.RecordedTraffic(ctx, model.EnvironmentSelector(project, environment), recording, limit)
}

// TrafficTraces returns trace projections from the live exchange buffer.
func (s *Service) TrafficTraces(project, environment string, limit int) []model.TrafficTrace {
	return s.traffic.Traces(model.EnvironmentSelector(project, environment), limit)
}

// TrafficTrace returns one full live trace projection by environment-local number.
func (s *Service) TrafficTrace(project, environment string, number int64) (model.TrafficTrace, error) {
	if trace, ok := s.traffic.Trace(model.EnvironmentSelector(project, environment), number); ok {
		return trace, nil
	}
	return model.TrafficTrace{}, database.ErrNotFound
}

// ClearTraffic removes the environment's live exchanges and derived traces.
// Durable recording contents and the sequence high-water mark are preserved.
func (s *Service) ClearTraffic(project, environment string) (int, int64) {
	return s.traffic.Clear(project, environment)
}

// Timeline returns durable environment events in reverse chronological order.
func (s *Service) Timeline(ctx context.Context, project, environment string, limit int) ([]model.TimelineEvent, error) {
	return s.database.Timeline(ctx, model.EnvironmentSelector(project, environment), limit)
}

// StartRecording validates and begins a bounded traffic capture.
func (s *Service) StartRecording(ctx context.Context, recording model.Recording, actor string) (model.Recording, error) {
	if err := model.ValidateArtifactName(recording.Name); err != nil {
		return model.Recording{}, err
	}
	if recording.Project == "" || recording.Environment == "" {
		return model.Recording{}, errors.New("project and environment are required")
	}
	definition, err := s.database.EnvironmentModel(ctx, recording.Project, recording.Environment)
	if err != nil {
		return model.Recording{}, err
	}
	if err := validateExperimentScope(definition, recording.Source, recording.Target, true); err != nil {
		return model.Recording{}, err
	}
	if recording.MaxEvents < 0 || recording.MaxEvents > 100_000 {
		return model.Recording{}, errors.New("maxEvents must be between 1 and 100000")
	}
	if recording.MaxPayloadBytes < 0 || recording.MaxPayloadBytes > 1<<20 {
		return model.Recording{}, errors.New("maxPayloadBytes must not exceed 1048576")
	}
	if recording.ExpiresAt == nil {
		expires := time.Now().UTC().Add(15 * time.Minute)
		recording.ExpiresAt = &expires
	}
	if !recording.ExpiresAt.After(time.Now()) || recording.ExpiresAt.After(time.Now().Add(time.Hour)) {
		return model.Recording{}, errors.New("recording expiry must be in the future and no more than one hour away")
	}
	created, err := s.database.CreateRecording(ctx, recording)
	if err != nil {
		return model.Recording{}, err
	}
	scope := model.EnvironmentSelector(recording.Project, recording.Environment)
	_, _ = s.timeline(ctx, scope, actor, "recording.started", recording.Name, "info", "Recording "+recording.Name+" started", nil)
	s.broker.Publish(events.Event{Type: "recording.state", Project: recording.Project, Environment: recording.Environment, Data: created})
	return created, nil
}

// StopRecording completes an active traffic recording.
func (s *Service) StopRecording(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.database.StopRecording(ctx, scope, name, "stopped"); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "recording.stopped", name, "info", "Recording "+name+" stopped", nil)
	s.broker.Publish(events.Event{Type: "recording.state", Project: project, Environment: environment, Data: map[string]any{"name": name, "status": "completed"}})
	return nil
}

// Recordings lists retained recordings for an environment.
func (s *Service) Recordings(ctx context.Context, project, environment string) ([]model.Recording, error) {
	return s.database.Recordings(ctx, model.EnvironmentSelector(project, environment))
}

// Recording returns one retained recording by name.
func (s *Service) Recording(ctx context.Context, project, environment, name string) (model.Recording, error) {
	return s.database.Recording(ctx, model.EnvironmentSelector(project, environment), name)
}

// DeleteRecording removes a recording and all of its captured traffic.
func (s *Service) DeleteRecording(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.database.DeleteRecording(ctx, scope, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "recording.deleted", name, "warning", "Recording "+name+" deleted", nil)
	return nil
}

// CreateFault validates and enables a scoped traffic fault rule.
func (s *Service) CreateFault(ctx context.Context, fault model.FaultRule, actor string) (model.FaultRule, error) {
	if err := model.ValidateArtifactName(fault.Name); err != nil {
		return model.FaultRule{}, err
	}
	if fault.Probability == 0 {
		fault.Probability = 1
	}
	if fault.Probability < 0 || fault.Probability > 1 {
		return model.FaultRule{}, errors.New("probability must be between 0 and 1")
	}
	if fault.Project == "" || fault.Environment == "" {
		return model.FaultRule{}, errors.New("project and environment are required")
	}
	definition, err := s.database.EnvironmentModel(ctx, fault.Project, fault.Environment)
	if err != nil {
		return model.FaultRule{}, err
	}
	if err := validateExperimentScope(definition, fault.Source, fault.Target, false); err != nil {
		return model.FaultRule{}, err
	}
	if fault.LatencyMS < 0 || fault.JitterMS < 0 || fault.LatencyMS+fault.JitterMS > 60_000 {
		return model.FaultRule{}, errors.New("latency plus jitter must be between 0 and 60000 milliseconds")
	}
	if fault.StatusCode != 0 && (fault.StatusCode < 400 || fault.StatusCode > 599) {
		return model.FaultRule{}, errors.New("synthetic status must be between 400 and 599")
	}
	if fault.LatencyMS == 0 && fault.JitterMS == 0 && fault.StatusCode == 0 && !fault.Abort {
		return model.FaultRule{}, errors.New("fault must define latency, jitter, a synthetic status, or an abort")
	}
	if strings.ContainsAny(fault.Method, " \t\r\n") {
		return model.FaultRule{}, errors.New("HTTP method filter must be a single token")
	}
	fault.Method = strings.ToUpper(fault.Method)
	if fault.Path != "" {
		if _, err := pathmatch.Match(fault.Path, "/validation"); err != nil {
			return model.FaultRule{}, fmt.Errorf("invalid path glob: %w", err)
		}
	}
	if fault.ExpiresAt != nil && !fault.ExpiresAt.After(time.Now()) {
		return model.FaultRule{}, errors.New("fault expiry must be in the future")
	}
	created, err := s.database.CreateFault(ctx, fault)
	if err != nil {
		return model.FaultRule{}, err
	}
	scope := model.EnvironmentSelector(fault.Project, fault.Environment)
	_, _ = s.timeline(ctx, scope, actor, "fault.enabled", fault.Name, "warning", created.ScopeSummary, nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: fault.Project, Environment: fault.Environment, Data: created})
	return created, nil
}

// Faults lists fault rules for an environment.
func (s *Service) Faults(ctx context.Context, project, environment string) ([]model.FaultRule, error) {
	return s.database.Faults(ctx, model.EnvironmentSelector(project, environment), false)
}

// Fault returns one fault rule by name.
func (s *Service) Fault(ctx context.Context, project, environment, name string) (model.FaultRule, error) {
	return s.database.Fault(ctx, model.EnvironmentSelector(project, environment), name)
}

// EnableFault reactivates a non-expired fault rule.
func (s *Service) EnableFault(ctx context.Context, project, environment, name, actor string) (model.FaultRule, error) {
	scope := model.EnvironmentSelector(project, environment)
	fault, err := s.database.Fault(ctx, scope, name)
	if err != nil {
		return model.FaultRule{}, err
	}
	if fault.ExpiresAt != nil && !fault.ExpiresAt.After(time.Now()) {
		return model.FaultRule{}, fmt.Errorf("fault %s has expired; delete it and create a new rule", name)
	}
	if fault.Enabled {
		return fault, nil
	}
	definition, err := s.database.EnvironmentModel(ctx, project, environment)
	if err != nil {
		return model.FaultRule{}, err
	}
	if err := validateExperimentScope(definition, fault.Source, fault.Target, false); err != nil {
		return model.FaultRule{}, err
	}
	if err := s.database.EnableFault(ctx, scope, name); err != nil {
		return model.FaultRule{}, err
	}
	fault, err = s.database.Fault(ctx, scope, name)
	if err != nil {
		return model.FaultRule{}, err
	}
	_, _ = s.timeline(ctx, scope, actor, "fault.enabled", name, "warning", "Fault "+name+" enabled", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: fault})
	return fault, nil
}

// DisableFault deactivates a fault rule without deleting it.
func (s *Service) DisableFault(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.database.DisableFault(ctx, scope, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "fault.disabled", name, "info", "Fault "+name+" disabled", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: map[string]any{"name": name, "enabled": false}})
	return nil
}

// DeleteFault permanently removes a fault rule.
func (s *Service) DeleteFault(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.database.DeleteFault(ctx, scope, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "fault.deleted", name, "warning", "Fault "+name+" deleted", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: map[string]any{"name": name, "enabled": false, "deleted": true}})
	return nil
}

// DisableAllFaults deactivates every enabled fault in an environment.
func (s *Service) DisableAllFaults(ctx context.Context, project, environment, actor string) (int64, error) {
	scope := model.EnvironmentSelector(project, environment)
	count, err := s.database.DisableAllFaults(ctx, scope)
	if err != nil {
		return 0, err
	}
	_, _ = s.timeline(ctx, scope, actor, "faults.disabled_all", scope, "info", fmt.Sprintf("Disabled %d active faults", count), nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: map[string]any{"enabled": false, "count": count}})
	return count, nil
}

// Logs reads recent managed-process logs for one service or the entire environment.
func (s *Service) Logs(ctx context.Context, project, environment, service string, limit int, since time.Time) ([]model.LogEntry, error) {
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	services := make([]string, 0, len(current.Services))
	for _, candidate := range current.Services {
		if service == "" || strings.EqualFold(candidate.Name, service) {
			services = append(services, candidate.Name)
		}
	}
	if service != "" && len(services) == 0 {
		return nil, database.ErrNotFound
	}
	privateKey, err := s.database.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(s.dataDirectory, "environments", privateKey, "logs")
	return logstore.Read(root, services, limit, since)
}

// ExportProject serializes a project's reusable topology as formatted JSON.
func (s *Service) ExportProject(ctx context.Context, project string) ([]byte, error) {
	definition, err := s.database.ProjectModel(ctx, project)
	if err != nil {
		return nil, err
	}
	definition.SuggestedName = project
	return json.MarshalIndent(struct {
		SchemaVersion int `json:"schemaVersion"`
		model.ProjectModel
	}{SchemaVersion: 1, ProjectModel: definition}, "", "  ")
}
