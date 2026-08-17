package contract

import "github.com/portless-run/portless/portless-daemon/model"

// Existing domain values that are already stable wire values are re-exported
// by the contract package so transport clients do not depend on domain
// implementation packages.

// Project is the stable project wire model.
type Project = model.Project

// Environment is the stable environment wire model.
type Environment = model.Environment

// Operation is the stable asynchronous-operation wire model.
type Operation = model.Operation

// OperationEvent is the stable operation-event wire model.
type OperationEvent = model.OperationEvent

// ComponentBinding is the stable provider-binding wire model.
type ComponentBinding = model.ComponentBinding

// Service is the stable service wire model.
type Service = model.Service

// ServiceConfiguration is the stable effective-configuration wire model.
type ServiceConfiguration = model.ServiceConfiguration

// EffectiveConnection is the stable service-connection wire model.
type EffectiveConnection = model.EffectiveConnection

// LogEntry is the stable runtime-log wire model.
type LogEntry = model.LogEntry

// TimelineEvent is the stable environment-timeline wire model.
type TimelineEvent = model.TimelineEvent

// TrafficEvent is the stable captured-traffic wire model.
type TrafficEvent = model.TrafficEvent

// Recording is the stable traffic-recording wire model.
type Recording = model.Recording

// FaultRule is the stable traffic-fault wire model.
type FaultRule = model.FaultRule
