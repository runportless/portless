package controlplane

import (
	"context"
	"github.com/runportless/portless/portless-daemon/model"
)

// PreviewRecordingDeletion reports exact retained counts without stopping capture.
func (s *Service) PreviewRecordingDeletion(ctx context.Context, project, environment, name string) (model.RecordingDeletionPreview, error) {
	var result model.RecordingDeletionPreview
	env, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return result, err
	}
	recording, err := s.database.Recording(ctx, model.EnvironmentSelector(project, environment), name)
	if err != nil {
		return result, err
	}
	result = model.RecordingDeletionPreview{Recording: recording, Expected: model.ResourceVersion{CreatedAt: recording.StartedAt, ParentCreatedAt: env.CreatedAt}, Blocked: []string{}, Retained: []string{"Live traffic, saved mock scenarios, source files, and provider volumes remain."}}
	if recording.Status == "active" {
		result.Blocked = append(result.Blocked, "Stop the recording before deleting it.")
	}
	return result, nil
}

// PreviewFaultDeletion reports the rule and its current creation/revision identity.
func (s *Service) PreviewFaultDeletion(ctx context.Context, project, environment, name string) (model.FaultDeletionPreview, error) {
	var result model.FaultDeletionPreview
	env, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return result, err
	}
	fault, err := s.database.Fault(ctx, model.EnvironmentSelector(project, environment), name)
	if err != nil {
		return result, err
	}
	return model.FaultDeletionPreview{Fault: fault, Expected: model.ResourceVersion{CreatedAt: fault.CreatedAt, Revision: fault.Revision, ParentCreatedAt: env.CreatedAt}, Blocked: []string{}, Retained: []string{"Timeline history, recordings, live traffic, and provider volumes remain."}}, nil
}
