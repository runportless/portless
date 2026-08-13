package model

import "time"

type EnvironmentStatus string

const (
	EnvironmentStarting   EnvironmentStatus = "starting"
	EnvironmentRecovering EnvironmentStatus = "recovering"
	EnvironmentHealthy    EnvironmentStatus = "healthy"
	EnvironmentDegraded   EnvironmentStatus = "degraded"
	EnvironmentFailed     EnvironmentStatus = "failed"
	EnvironmentStopping   EnvironmentStatus = "stopping"
	EnvironmentStopped    EnvironmentStatus = "stopped"
	EnvironmentUnknown    EnvironmentStatus = "unknown"
)

type ServiceStatus string

const (
	ServicePlanned    ServiceStatus = "planned"
	ServiceStarting   ServiceStatus = "starting"
	ServiceRecovering ServiceStatus = "recovering"
	ServiceReady      ServiceStatus = "ready"
	ServiceUnhealthy  ServiceStatus = "unhealthy"
	ServiceExited     ServiceStatus = "exited"
	ServiceFailed     ServiceStatus = "failed"
	ServiceStopping   ServiceStatus = "stopping"
	ServiceStopped    ServiceStatus = "stopped"
	ServiceUnknown    ServiceStatus = "unknown"
)

type ServiceKind string

const (
	ServiceProcess   ServiceKind = "process"
	ServiceContainer ServiceKind = "container"
)

type ProviderKind string

const (
	ProviderLocal     ProviderKind = "local"
	ProviderContainer ProviderKind = "container"
	ProviderRemote    ProviderKind = "remote"
)

type RemoteClassification string

const (
	RemoteDevelopment RemoteClassification = "development"
	RemoteQA          RemoteClassification = "qa"
	RemoteStaging     RemoteClassification = "staging"
	RemoteUnknown     RemoteClassification = "unknown"
)

type WritePolicy string

const (
	WriteReadOnly  WritePolicy = "read-only"
	WriteReadWrite WritePolicy = "read-write"
)

type Protocol string

const (
	ProtocolHTTP     Protocol = "http"
	ProtocolTCP      Protocol = "tcp"
	ProtocolPostgres Protocol = "postgres"
	ProtocolRedis    Protocol = "redis"
)

type HealthCheck struct {
	Kind     string        `json:"kind"`
	Path     string        `json:"path,omitempty"`
	Timeout  time.Duration `json:"timeout"`
	Interval time.Duration `json:"interval"`
}

type ServiceDefinition struct {
	Name             string            `json:"name"`
	Kind             ServiceKind       `json:"kind"`
	Framework        string            `json:"framework,omitempty"`
	Template         string            `json:"template,omitempty"`
	Version          string            `json:"version,omitempty"`
	Command          []string          `json:"command,omitempty"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	PortEnvironment  string            `json:"portEnvironment,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	Required         bool              `json:"required"`
	Health           HealthCheck       `json:"health"`
	Evidence         []Evidence        `json:"evidence,omitempty"`
}

type Evidence struct {
	File        string `json:"file"`
	Explanation string `json:"explanation"`
	Confidence  string `json:"confidence"`
}

type Connection struct {
	Source      string   `json:"source"`
	Target      string   `json:"target"`
	Protocol    Protocol `json:"protocol"`
	Environment string   `json:"environment,omitempty"`
	Required    bool     `json:"required"`
}

type EffectiveConnection struct {
	Connection
	TargetProvider ProviderKind  `json:"targetProvider"`
	TargetStatus   ServiceStatus `json:"targetStatus"`
	ProxyAddress   string        `json:"proxyAddress,omitempty"`
	TargetEndpoint string        `json:"targetEndpoint,omitempty"`
	InjectedEnvVar string        `json:"injectedEnvVar,omitempty"`
	InjectedValue  string        `json:"injectedValue,omitempty"`
}

type ConnectionReference struct {
	Source      string   `json:"source"`
	TargetHint  string   `json:"targetHint"`
	Protocol    Protocol `json:"protocol"`
	Environment string   `json:"environment,omitempty"`
	Required    bool     `json:"required"`
}

type ProjectModel struct {
	SuggestedName  string                `json:"suggestedName"`
	PrimaryService string                `json:"primaryService,omitempty"`
	Services       []ServiceDefinition   `json:"services"`
	Connections    []Connection          `json:"connections"`
	References     []ConnectionReference `json:"references,omitempty"`
}

type ProjectSource struct {
	Name     string   `json:"name"`
	Services []string `json:"services,omitempty"`
}

type SourceBinding struct {
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	Status     string       `json:"status"`
	Warnings   []string     `json:"warnings,omitempty"`
	ScannedAt  time.Time    `json:"scannedAt"`
	Definition ProjectModel `json:"-"`
}

type RemoteTarget struct {
	URL            string               `json:"url"`
	Classification RemoteClassification `json:"classification"`
	WritePolicy    WritePolicy          `json:"writePolicy"`
	HealthPath     string               `json:"healthPath,omitempty"`
}

type ComponentBinding struct {
	Service  string        `json:"service"`
	Provider ProviderKind  `json:"provider"`
	Source   string        `json:"source,omitempty"`
	Remote   *RemoteTarget `json:"remote,omitempty"`
}

type ConfigurationIssue struct {
	Code        string `json:"code"`
	Subject     string `json:"subject,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

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

type Service struct {
	ServiceDefinition
	Status        ServiceStatus `json:"status"`
	Reason        string        `json:"reason,omitempty"`
	Generation    int64         `json:"generation"`
	PID           int           `json:"pid,omitempty"`
	UpstreamPort  int           `json:"upstreamPort,omitempty"`
	IngressURL    string        `json:"ingressUrl,omitempty"`
	StartedAt     *time.Time    `json:"startedAt,omitempty"`
	RestartCount  int64         `json:"restartCount"`
	RecentRequest int64         `json:"recentRequests"`
	P95Millis     int64         `json:"p95Millis,omitempty"`
}

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

type OperationEvent struct {
	Sequence  int64          `json:"sequence"`
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"`
	Subject   string         `json:"subject,omitempty"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type TrafficEvent struct {
	Project              string               `json:"project"`
	Environment          string               `json:"environment"`
	Sequence             int64                `json:"sequence"`
	Protocol             Protocol             `json:"protocol"`
	Source               string               `json:"source"`
	Target               string               `json:"target"`
	TargetProvider       ProviderKind         `json:"targetProvider,omitempty"`
	RemoteClassification RemoteClassification `json:"remoteClassification,omitempty"`
	StartedAt            time.Time            `json:"startedAt"`
	CompletedAt          time.Time            `json:"completedAt"`
	Method               string               `json:"method,omitempty"`
	Host                 string               `json:"host,omitempty"`
	Path                 string               `json:"path,omitempty"`
	Status               int                  `json:"status,omitempty"`
	DurationMS           int64                `json:"durationMs"`
	RequestBytes         int64                `json:"requestBytes"`
	ResponseBytes        int64                `json:"responseBytes"`
	Fault                string               `json:"fault,omitempty"`
	Recording            string               `json:"recording,omitempty"`
	Error                string               `json:"error,omitempty"`
	RequestHeaders       map[string]string    `json:"requestHeaders,omitempty"`
	ResponseHeaders      map[string]string    `json:"responseHeaders,omitempty"`
}

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

type ConfigurationValue struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	Classification string `json:"classification"`
	Source         string `json:"source"`
}

type ServiceConfiguration struct {
	Service          string               `json:"service"`
	Command          []string             `json:"command"`
	WorkingDirectory string               `json:"workingDirectory,omitempty"`
	PortEnvironment  string               `json:"portEnvironment,omitempty"`
	Environment      []ConfigurationValue `json:"environment"`
	Health           HealthCheck          `json:"health"`
}

type LogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Service    string    `json:"service"`
	Stream     string    `json:"stream"`
	Generation int64     `json:"generation"`
	Message    string    `json:"message"`
}

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
