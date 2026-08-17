package model

import "time"

// EnvironmentStatus describes the aggregate lifecycle state of an environment.
type EnvironmentStatus string

const (
	// EnvironmentStarting indicates that required services are being started.
	EnvironmentStarting EnvironmentStatus = "starting"
	// EnvironmentRecovering indicates that Portless is restoring an interrupted environment.
	EnvironmentRecovering EnvironmentStatus = "recovering"
	// EnvironmentHealthy indicates that every required service is ready.
	EnvironmentHealthy EnvironmentStatus = "healthy"
	// EnvironmentDegraded indicates that the environment is usable but not fully healthy.
	EnvironmentDegraded EnvironmentStatus = "degraded"
	// EnvironmentFailed indicates that a required service could not be started or recovered.
	EnvironmentFailed EnvironmentStatus = "failed"
	// EnvironmentStopping indicates that services are being stopped.
	EnvironmentStopping EnvironmentStatus = "stopping"
	// EnvironmentStopped indicates that no services are running.
	EnvironmentStopped EnvironmentStatus = "stopped"
	// EnvironmentUnknown indicates that the daemon cannot verify the environment state.
	EnvironmentUnknown EnvironmentStatus = "unknown"
)

// ServiceStatus describes the observed lifecycle state of a service.
type ServiceStatus string

const (
	// ServicePlanned indicates that a service has not been started yet.
	ServicePlanned ServiceStatus = "planned"
	// ServiceStarting indicates that a service launch is in progress.
	ServiceStarting ServiceStatus = "starting"
	// ServiceRecovering indicates that Portless is adopting or restarting a service.
	ServiceRecovering ServiceStatus = "recovering"
	// ServiceReady indicates that the service passed its readiness check.
	ServiceReady ServiceStatus = "ready"
	// ServiceUnhealthy indicates that a running service failed its readiness check.
	ServiceUnhealthy ServiceStatus = "unhealthy"
	// ServiceExited indicates that the service process terminated.
	ServiceExited ServiceStatus = "exited"
	// ServiceFailed indicates that the service could not be launched or recovered.
	ServiceFailed ServiceStatus = "failed"
	// ServiceStopping indicates that shutdown is in progress.
	ServiceStopping ServiceStatus = "stopping"
	// ServiceStopped indicates that the service is not running.
	ServiceStopped ServiceStatus = "stopped"
	// ServiceUnknown indicates that the service state cannot be verified.
	ServiceUnknown ServiceStatus = "unknown"
)

// LaunchMode identifies how Portless started a process service.
type LaunchMode string

const (
	// LaunchManaged starts a service without a debugger.
	LaunchManaged LaunchMode = "managed"
	// LaunchDebug starts a service with a supported debugger enabled.
	LaunchDebug LaunchMode = "debug"
)

// DebugAdapter identifies the debugger protocol exposed by a service.
type DebugAdapter string

const (
	// DebugNodeInspector selects the Node.js Inspector protocol.
	DebugNodeInspector DebugAdapter = "node-inspector"
	// DebugJDWP selects the Java Debug Wire Protocol.
	DebugJDWP DebugAdapter = "jdwp"
)

// DebugLauncher identifies a safe, framework-specific debug command transformation.
type DebugLauncher string

const (
	// DebugNodeDirect launches a Node.js entry point directly.
	DebugNodeDirect DebugLauncher = "node-direct"
	// DebugNestCLI launches a NestJS application through its CLI.
	DebugNestCLI DebugLauncher = "nest-cli"
	// DebugSpringGradle launches Spring Boot through Gradle.
	DebugSpringGradle DebugLauncher = "spring-gradle"
	// DebugSpringMaven launches Spring Boot through Maven.
	DebugSpringMaven DebugLauncher = "spring-maven"
)

// ServiceKind distinguishes executable services from managed dependencies.
type ServiceKind string

const (
	// ServiceProcess is a locally executable application process.
	ServiceProcess ServiceKind = "process"
	// ServiceResource is an infrastructure dependency managed by a provider.
	ServiceResource ServiceKind = "resource"
)

// ProviderKind identifies where an environment obtains a service.
type ProviderKind string

const (
	// ProviderLocal runs a service from a bound local source.
	ProviderLocal ProviderKind = "local"
	// ProviderContainer runs a service in the selected container runtime.
	ProviderContainer ProviderKind = "container"
	// ProviderRemote routes the service to an external environment.
	ProviderRemote ProviderKind = "remote"
)

// RemoteClassification records the safety classification of a remote target.
type RemoteClassification string

const (
	// RemoteDevelopment identifies a development target.
	RemoteDevelopment RemoteClassification = "development"
	// RemoteQA identifies a quality-assurance target.
	RemoteQA RemoteClassification = "qa"
	// RemoteStaging identifies a staging target.
	RemoteStaging RemoteClassification = "staging"
	// RemoteUnknown identifies a target whose environment is not known.
	RemoteUnknown RemoteClassification = "unknown"
)

// WritePolicy records whether Portless may send mutating requests to a remote target.
type WritePolicy string

const (
	// WriteReadOnly blocks requests that may mutate remote state.
	WriteReadOnly WritePolicy = "read-only"
	// WriteReadWrite permits both read and write traffic.
	WriteReadWrite WritePolicy = "read-write"
)

// Protocol identifies a connection's application protocol.
type Protocol string

const (
	// ProtocolHTTP represents HTTP traffic.
	ProtocolHTTP Protocol = "http"
	// ProtocolTCP represents raw TCP traffic.
	ProtocolTCP Protocol = "tcp"
)

// EndpointKind identifies an endpoint's role in local routing.
type EndpointKind string

const (
	// EndpointPublic is the user-facing endpoint for an application service.
	EndpointPublic EndpointKind = "public"
	// EndpointConnection is the source-scoped endpoint for a service dependency.
	EndpointConnection EndpointKind = "connection"
)

// Endpoint is a stable, user-facing address owned by Portless. Address is the
// concrete loopback listener used by the proxy; clients should use URL (or
// Host and Port) so the listener identity can remain an implementation detail.
type Endpoint struct {
	Kind     EndpointKind `json:"kind"`
	Protocol Protocol     `json:"protocol"`
	Host     string       `json:"host"`
	Port     int          `json:"port"`
	URL      string       `json:"url"`
	Address  string       `json:"address,omitempty"`
}

// HealthCheck defines how Portless determines whether a service is ready.
type HealthCheck struct {
	Kind     string        `json:"kind"`
	Path     string        `json:"path,omitempty"`
	Timeout  time.Duration `json:"timeout"`
	Interval time.Duration `json:"interval"`
}

// ResourceDefinition identifies the resource plugin which owns provisioning
// and connection semantics for a logical resource service.
type ResourceDefinition struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

// DebugCapability is a discovery-owned, declarative recipe. Command is an
// already-tokenized base command; the selected launcher is solely responsible
// for inserting a private debugger address without invoking a shell.
type DebugCapability struct {
	Adapter  DebugAdapter  `json:"adapter"`
	Launcher DebugLauncher `json:"launcher"`
	Command  []string      `json:"command"`
}

// DebuggerRuntime describes a debugger listener created for a running service.
type DebuggerRuntime struct {
	Adapter DebugAdapter `json:"adapter"`
	Host    string       `json:"host"`
	Port    int          `json:"port"`
	State   string       `json:"state"`
}

// ServiceDefinition is the discovered, environment-independent definition of a service.
type ServiceDefinition struct {
	Name             string              `json:"name"`
	Kind             ServiceKind         `json:"kind"`
	Framework        string              `json:"framework,omitempty"`
	Resource         *ResourceDefinition `json:"resource,omitempty"`
	Debug            *DebugCapability    `json:"debug,omitempty"`
	Command          []string            `json:"command,omitempty"`
	WorkingDirectory string              `json:"workingDirectory,omitempty"`
	// ServiceDirectory identifies the physical source directory for CWD-based
	// service selection. It can differ from WorkingDirectory in monorepos whose
	// build command runs from a shared root.
	ServiceDirectory string `json:"serviceDirectory,omitempty"`
	PortEnvironment  string `json:"portEnvironment,omitempty"`
	// Port is the stable client-facing port for a TCP service. Discovery copies
	// the registered resource plugin's port into the model so persistence and
	// endpoint allocation remain independent of executable plugin code.
	Port        int               `json:"port,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Required    bool              `json:"required"`
	Health      HealthCheck       `json:"health"`
	Evidence    []Evidence        `json:"evidence,omitempty"`
}

// Evidence explains which source artifact produced a discovery conclusion.
type Evidence struct {
	File        string `json:"file"`
	Explanation string `json:"explanation"`
	Confidence  string `json:"confidence"`
}

// Connection describes a resolved dependency edge between two services.
type Connection struct {
	Source      string   `json:"source"`
	Target      string   `json:"target"`
	Protocol    Protocol `json:"protocol"`
	Binding     string   `json:"binding,omitempty"`
	Environment string   `json:"environment,omitempty"`
	Required    bool     `json:"required"`
}

// EffectiveConnection combines a declared edge with its environment-specific target.
type EffectiveConnection struct {
	Connection
	TargetProvider      ProviderKind      `json:"targetProvider"`
	TargetStatus        ServiceStatus     `json:"targetStatus"`
	Endpoint            *Endpoint         `json:"endpoint,omitempty"`
	RuntimeTarget       string            `json:"runtimeTarget,omitempty"`
	InjectedEnvironment map[string]string `json:"injectedEnvironment,omitempty"`
}

// ConnectionReference is an unresolved dependency hint emitted during discovery.
type ConnectionReference struct {
	Source      string   `json:"source"`
	TargetHint  string   `json:"targetHint"`
	Protocol    Protocol `json:"protocol"`
	Binding     string   `json:"binding,omitempty"`
	Environment string   `json:"environment,omitempty"`
	Required    bool     `json:"required"`
}

// ProjectModel is the reusable service topology discovered across project sources.
type ProjectModel struct {
	SuggestedName  string                `json:"suggestedName"`
	PrimaryService string                `json:"primaryService,omitempty"`
	Services       []ServiceDefinition   `json:"services"`
	Connections    []Connection          `json:"connections"`
	References     []ConnectionReference `json:"references,omitempty"`
}

// ProjectSource names a registered source and the services discovered from it.
type ProjectSource struct {
	Name     string   `json:"name"`
	Services []string `json:"services,omitempty"`
}

// SourceBinding records the filesystem source selected for an environment.
type SourceBinding struct {
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	Status     string       `json:"status"`
	Warnings   []string     `json:"warnings,omitempty"`
	ScannedAt  time.Time    `json:"scannedAt"`
	Definition ProjectModel `json:"-"`
}

// RemoteTarget describes an explicitly classified external service endpoint.
type RemoteTarget struct {
	URL            string               `json:"url"`
	Classification RemoteClassification `json:"classification"`
	WritePolicy    WritePolicy          `json:"writePolicy"`
	HealthPath     string               `json:"healthPath,omitempty"`
}

// ComponentBinding selects a provider and source for one environment service.
type ComponentBinding struct {
	Service  string        `json:"service"`
	Provider ProviderKind  `json:"provider"`
	Source   string        `json:"source,omitempty"`
	Remote   *RemoteTarget `json:"remote,omitempty"`
}

// ConfigurationIssue describes an invalid or incomplete environment setting.
type ConfigurationIssue struct {
	Code        string `json:"code"`
	Subject     string `json:"subject,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

// EnvironmentSummary contains list-view status for a project environment.
type EnvironmentSummary struct {
	Project      string            `json:"project"`
	Name         string            `json:"name"`
	Revision     int64             `json:"revision"`
	Status       EnvironmentStatus `json:"status"`
	Reason       string            `json:"reason,omitempty"`
	ServiceCount int               `json:"serviceCount"`
	ReadyCount   int               `json:"readyCount"`
	RemoteCount  int               `json:"remoteCount"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	DashboardURL string            `json:"dashboardUrl,omitempty"`
}

// Project is the complete public representation of a logical application.
type Project struct {
	Name           string               `json:"name"`
	Revision       int64                `json:"revision"`
	PrimaryService string               `json:"primaryService,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
	DashboardURL   string               `json:"dashboardUrl,omitempty"`
	Sources        []ProjectSource      `json:"sources,omitempty"`
	Services       []ServiceDefinition  `json:"services,omitempty"`
	Connections    []Connection         `json:"connections,omitempty"`
	Environments   []EnvironmentSummary `json:"environments,omitempty"`
}

// Environment is the effective topology, bindings, and runtime state for a project variant.
type Environment struct {
	Project        string               `json:"project"`
	Name           string               `json:"name"`
	Revision       int64                `json:"revision"`
	Status         EnvironmentStatus    `json:"status"`
	Reason         string               `json:"reason,omitempty"`
	PrimaryService string               `json:"primaryService,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
	DashboardURL   string               `json:"dashboardUrl,omitempty"`
	Sources        []SourceBinding      `json:"sources,omitempty"`
	Bindings       []ComponentBinding   `json:"bindings,omitempty"`
	Services       []Service            `json:"services,omitempty"`
	Connections    []Connection         `json:"connections,omitempty"`
	Issues         []ConfigurationIssue `json:"issues,omitempty"`
}

// Service combines a service definition with its current environment runtime state.
type Service struct {
	ServiceDefinition
	LaunchMode    LaunchMode       `json:"launchMode"`
	Debugger      *DebuggerRuntime `json:"debugger,omitempty"`
	Status        ServiceStatus    `json:"status"`
	Reason        string           `json:"reason,omitempty"`
	Generation    int64            `json:"generation"`
	PID           int              `json:"pid,omitempty"`
	UpstreamPort  int              `json:"upstreamPort,omitempty"`
	Endpoints     []Endpoint       `json:"endpoints"`
	StartedAt     *time.Time       `json:"startedAt,omitempty"`
	RestartCount  int64            `json:"restartCount"`
	RecentRequest int64            `json:"recentRequests"`
	P95Millis     int64            `json:"p95Millis,omitempty"`
}

// Operation records a durable, multi-step environment or service action.
type Operation struct {
	Project     string           `json:"project"`
	Environment string           `json:"environment"`
	Number      int64            `json:"number"`
	Type        string           `json:"type"`
	State       string           `json:"state"`
	Actor       string           `json:"actor"`
	StartedAt   time.Time        `json:"startedAt"`
	CompletedAt *time.Time       `json:"completedAt,omitempty"`
	Error       string           `json:"error,omitempty"`
	Events      []OperationEvent `json:"events,omitempty"`
}

// OperationEvent records one ordered update within an operation.
type OperationEvent struct {
	Sequence  int64          `json:"sequence"`
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"`
	Subject   string         `json:"subject,omitempty"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// TrafficRequestKind classifies the role of an HTTP request without depending
// on browser-specific resource-type APIs.
type TrafficRequestKind string

const (
	// TrafficRequestNavigation is a top-level document navigation.
	TrafficRequestNavigation TrafficRequestKind = "navigation"
	// TrafficRequestSubresource is browser background activity such as an image,
	// stylesheet, script, or favicon request.
	TrafficRequestSubresource TrafficRequestKind = "subresource"
	// TrafficRequestFetch is a browser fetch or XMLHttpRequest-style request.
	TrafficRequestFetch TrafficRequestKind = "fetch"
	// TrafficRequestService is a request made by one application service to another.
	TrafficRequestService TrafficRequestKind = "service"
	// TrafficRequestUnknown means the available metadata could not classify the request.
	TrafficRequestUnknown TrafficRequestKind = "unknown"
)

// TrafficCorrelation describes how confidently Portless related an exchange
// to the other exchanges in a trace.
type TrafficCorrelation string

const (
	// TrafficCorrelationExact means propagated trace context established the relationship.
	TrafficCorrelationExact TrafficCorrelation = "exact"
	// TrafficCorrelationInferred means a unique topology and timing relationship was found.
	TrafficCorrelationInferred TrafficCorrelation = "inferred"
	// TrafficCorrelationPartial means part of the trace was unavailable or could not be linked.
	TrafficCorrelationPartial TrafficCorrelation = "partial"
	// TrafficCorrelationAmbiguous means more than one parent was plausible and Portless did not guess.
	TrafficCorrelationAmbiguous TrafficCorrelation = "ambiguous"
)

// TrafficExchange records one completed HTTP or TCP exchange observed by Portless.
type TrafficExchange struct {
	Project               string               `json:"project"`
	Environment           string               `json:"environment"`
	Sequence              int64                `json:"sequence"`
	Protocol              Protocol             `json:"protocol"`
	Source                string               `json:"source"`
	Target                string               `json:"target"`
	TargetProvider        ProviderKind         `json:"targetProvider,omitempty"`
	RemoteClassification  RemoteClassification `json:"remoteClassification,omitempty"`
	StartedAt             time.Time            `json:"startedAt"`
	CompletedAt           time.Time            `json:"completedAt"`
	Method                string               `json:"method,omitempty"`
	Host                  string               `json:"host,omitempty"`
	Path                  string               `json:"path,omitempty"`
	RequestTarget         string               `json:"requestTarget,omitempty"`
	RequestKind           TrafficRequestKind   `json:"requestKind,omitempty"`
	Status                int                  `json:"status,omitempty"`
	DurationMS            int64                `json:"durationMs"`
	RequestBytes          int64                `json:"requestBytes"`
	ResponseBytes         int64                `json:"responseBytes"`
	RequestCapturedBytes  int64                `json:"requestCapturedBytes,omitempty"`
	ResponseCapturedBytes int64                `json:"responseCapturedBytes,omitempty"`
	Fault                 string               `json:"fault,omitempty"`
	Recording             string               `json:"recording,omitempty"`
	Error                 string               `json:"error,omitempty"`
	TraceID               string               `json:"traceId,omitempty"`
	SpanID                string               `json:"spanId,omitempty"`
	ParentSpanID          string               `json:"parentSpanId,omitempty"`
	RequestHeaders        map[string][]string  `json:"requestHeaders,omitempty"`
	ResponseHeaders       map[string][]string  `json:"responseHeaders,omitempty"`
	RequestBody           string               `json:"requestBody,omitempty"`
	ResponseBody          string               `json:"responseBody,omitempty"`
	RequestBodyTruncated  bool                 `json:"requestBodyTruncated,omitempty"`
	ResponseBodyTruncated bool                 `json:"responseBodyTruncated,omitempty"`
}

// TrafficTraceSpan places one exchange within a trace tree and waterfall.
type TrafficTraceSpan struct {
	Exchange       TrafficExchange    `json:"exchange"`
	ParentSequence int64              `json:"parentSequence,omitempty"`
	Depth          int                `json:"depth"`
	StartOffsetMS  int64              `json:"startOffsetMs"`
	Correlation    TrafficCorrelation `json:"correlation"`
}

// TrafficTrace is a rebuildable projection of related exchanges. Number is
// local to an environment and is the earliest observed exchange sequence in
// the trace rather than an opaque global identifier.
type TrafficTrace struct {
	Project       string             `json:"project"`
	Environment   string             `json:"environment"`
	Number        int64              `json:"number"`
	LastSequence  int64              `json:"lastSequence"`
	TraceID       string             `json:"traceId,omitempty"`
	RootSequence  int64              `json:"rootSequence,omitempty"`
	StartedAt     time.Time          `json:"startedAt"`
	CompletedAt   time.Time          `json:"completedAt"`
	DurationMS    int64              `json:"durationMs"`
	Method        string             `json:"method,omitempty"`
	RequestTarget string             `json:"requestTarget,omitempty"`
	Source        string             `json:"source"`
	Target        string             `json:"target"`
	Status        int                `json:"status,omitempty"`
	Error         bool               `json:"error"`
	Faulted       bool               `json:"faulted"`
	Background    bool               `json:"background"`
	SpanCount     int                `json:"spanCount"`
	Correlation   TrafficCorrelation `json:"correlation"`
	Spans         []TrafficTraceSpan `json:"spans,omitempty"`
}

// TrafficActivity describes a live connection or request phase for topology animation.
type TrafficActivity struct {
	Project           string    `json:"project"`
	Environment       string    `json:"environment"`
	Protocol          Protocol  `json:"protocol"`
	Source            string    `json:"source"`
	Target            string    `json:"target"`
	ObservedAt        time.Time `json:"observedAt"`
	Phase             string    `json:"phase"`
	ActiveConnections int64     `json:"activeConnections"`
	RequestBytes      int64     `json:"requestBytes,omitempty"`
	ResponseBytes     int64     `json:"responseBytes,omitempty"`
	Fault             string    `json:"fault,omitempty"`
}

// ConfigurationValue is an effective service setting with its source and sensitivity.
type ConfigurationValue struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	Classification string `json:"classification"`
	Source         string `json:"source"`
}

// ServiceConfiguration describes the effective launch configuration for a service.
type ServiceConfiguration struct {
	Service          string               `json:"service"`
	Command          []string             `json:"command"`
	WorkingDirectory string               `json:"workingDirectory,omitempty"`
	PortEnvironment  string               `json:"portEnvironment,omitempty"`
	Environment      []ConfigurationValue `json:"environment"`
	Health           HealthCheck          `json:"health"`
}

// LogEntry is one timestamped line from a managed service stream.
type LogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Service    string    `json:"service"`
	Stream     string    `json:"stream"`
	Generation int64     `json:"generation"`
	Message    string    `json:"message"`
}

// Recording describes a bounded traffic-capture session and its retained event count.
type Recording struct {
	Project       string     `json:"project"`
	Environment   string     `json:"environment"`
	Name          string     `json:"name"`
	Source        string     `json:"source,omitempty"`
	Target        string     `json:"target,omitempty"`
	CaptureBodies bool       `json:"captureBodies"`
	MaxEvents     int64      `json:"maxEvents"`
	MaxBodyBytes  int64      `json:"maxBodyBytes"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"startedAt"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	EventCount    int64      `json:"eventCount"`
}

// FaultRule defines a scoped traffic failure and its current activation state.
type FaultRule struct {
	Project      string     `json:"project"`
	Environment  string     `json:"environment"`
	Name         string     `json:"name"`
	Source       string     `json:"source"`
	Target       string     `json:"target"`
	Method       string     `json:"method,omitempty"`
	Path         string     `json:"path,omitempty"`
	Probability  float64    `json:"probability"`
	LatencyMS    int64      `json:"latencyMs,omitempty"`
	JitterMS     int64      `json:"jitterMs,omitempty"`
	StatusCode   int        `json:"statusCode,omitempty"`
	Abort        bool       `json:"abort,omitempty"`
	Enabled      bool       `json:"enabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	MatchCount   int64      `json:"matchCount"`
	Revision     int64      `json:"revision"`
	ScopeSummary string     `json:"scopeSummary"`
}

// TimelineEvent records a durable user-visible environment history entry.
type TimelineEvent struct {
	Project     string         `json:"project"`
	Environment string         `json:"environment"`
	Sequence    int64          `json:"sequence"`
	Timestamp   time.Time      `json:"timestamp"`
	Actor       string         `json:"actor"`
	Type        string         `json:"type"`
	Subject     string         `json:"subject,omitempty"`
	Severity    string         `json:"severity"`
	Summary     string         `json:"summary"`
	Details     map[string]any `json:"details,omitempty"`
}
