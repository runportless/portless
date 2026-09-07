package contract

import "github.com/runportless/portless/portless-daemon/model"

// ResourceVersion is a reviewed identity and revision used by conditional mutations.
type ResourceVersion = model.ResourceVersion

// MockDeletionPreview describes exact mock deletion effects and eligibility.
type MockDeletionPreview = model.MockDeletionPreview

// RecordingDeletionPreview reports the reviewed recording and deletion effects.
type RecordingDeletionPreview = model.RecordingDeletionPreview

// FaultDeletionPreview reports the reviewed fault and deletion effects.
type FaultDeletionPreview = model.FaultDeletionPreview

// ConfigurationPreview describes exact configuration cleanup effects and identity.
type ConfigurationPreview = model.ConfigurationPreview
