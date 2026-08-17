package contract

import "github.com/portless-run/portless/portless-daemon/model"

// Existing domain values that are already stable wire values are re-exported
// by the contract package so transport clients do not depend on domain
// implementation packages.
type Project = model.Project
type Environment = model.Environment
type Operation = model.Operation
type OperationEvent = model.OperationEvent
type ComponentBinding = model.ComponentBinding
type Service = model.Service
type ServiceConfiguration = model.ServiceConfiguration
type EffectiveConnection = model.EffectiveConnection
type LogEntry = model.LogEntry
type TimelineEvent = model.TimelineEvent
type TrafficEvent = model.TrafficEvent
type Recording = model.Recording
type FaultRule = model.FaultRule
