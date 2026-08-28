package portlessmcp

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

type environmentInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
}

type serviceInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Service     string `json:"service" jsonschema:"public service name"`
}

type connectionInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Source      string `json:"source" jsonschema:"public source service name or external"`
	Target      string `json:"target" jsonschema:"public target service name"`
}

type environmentListInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum visible environments, default 100 and maximum 500"`
}

type connectionListInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum connections, default 100 and maximum 500"`
}

type logsInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Service     string `json:"service,omitempty" jsonschema:"optional public service name"`
	Since       string `json:"since,omitempty" jsonschema:"optional duration or timestamp understood by Portless"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum entries, default 200 and maximum 1000"`
}

type trafficInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Protocol    string `json:"protocol,omitempty" jsonschema:"optional protocol: http or tcp"`
	Service     string `json:"service,omitempty" jsonschema:"optional source or target service"`
	Edge        string `json:"edge,omitempty" jsonschema:"optional directed source:target edge"`
	After       int64  `json:"after,omitempty" jsonschema:"return exchanges after this environment-local sequence"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum exchanges, default 100 and maximum 500"`
}

type artifactListInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum artifacts, default 100 and maximum 500"`
}

type recordingInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Recording   string `json:"recording" jsonschema:"public recording name"`
}

type faultInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Fault       string `json:"fault" jsonschema:"public fault name"`
}

type operationInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Number      int64  `json:"number" jsonschema:"environment-local durable operation number"`
}

type operationListInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum operations, default 50 and maximum 100"`
}

type timelineInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum timeline events, default 100 and maximum 500"`
}

type trafficDetailInput struct {
	Environment string `json:"environment" jsonschema:"target environment in project/environment form"`
	Sequence    int64  `json:"sequence" jsonschema:"environment-local traffic exchange sequence"`
}

type startEnvironmentInput struct {
	Environment    string   `json:"environment" jsonschema:"target environment in project/environment form"`
	DebugServices  []string `json:"debugServices,omitempty" jsonschema:"explicit local services to start under a discovered debugger"`
	Managed        bool     `json:"managed,omitempty" jsonschema:"return debug services to normal managed mode"`
	WaitSeconds    *int     `json:"waitSeconds,omitempty" jsonschema:"seconds to wait, default 30, zero returns immediately, and maximum 120"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty" jsonschema:"optional visible ASCII retry key of at most 120 characters"`
}

type stopEnvironmentInput struct {
	Environment    string `json:"environment" jsonschema:"target environment in project/environment form"`
	WaitSeconds    *int   `json:"waitSeconds,omitempty" jsonschema:"seconds to wait, default 30, zero returns immediately, and maximum 120"`
	IdempotencyKey string `json:"idempotencyKey,omitempty" jsonschema:"optional visible ASCII retry key of at most 120 characters"`
}

type serviceStateInput struct {
	Environment    string `json:"environment" jsonschema:"target environment in project/environment form"`
	Service        string `json:"service" jsonschema:"public service name"`
	Action         string `json:"action" jsonschema:"one of start, stop, restart, debug, or manage"`
	WaitSeconds    *int   `json:"waitSeconds,omitempty" jsonschema:"seconds to wait, default 30, zero returns immediately, and maximum 120"`
	IdempotencyKey string `json:"idempotencyKey,omitempty" jsonschema:"optional visible ASCII retry key of at most 120 characters"`
}

type lifecycleOutput struct {
	Project         string             `json:"project"`
	Environment     string             `json:"environment"`
	UntrustedData   bool               `json:"untrustedData"`
	Operation       contract.Operation `json:"operation"`
	IdempotencyKey  string             `json:"idempotencyKey"`
	TimedOutWaiting bool               `json:"timedOutWaiting"`
}

type startRecordingInput struct {
	Environment     string `json:"environment" jsonschema:"target environment in project/environment form"`
	Recording       string `json:"recording" jsonschema:"public recording name"`
	Source          string `json:"source,omitempty" jsonschema:"optional source service"`
	Target          string `json:"target,omitempty" jsonschema:"optional target service"`
	DurationSeconds int    `json:"durationSeconds" jsonschema:"finite duration from 1 through 3600 seconds"`
	MaxEvents       int64  `json:"maxEvents,omitempty" jsonschema:"maximum retained exchanges, default 10000 and maximum 100000"`
}

type applyFaultInput struct {
	Environment     string   `json:"environment" jsonschema:"target environment in project/environment form"`
	Fault           string   `json:"fault" jsonschema:"public fault name"`
	Source          string   `json:"source" jsonschema:"source service or external"`
	Target          string   `json:"target" jsonschema:"target service"`
	LatencyMS       int64    `json:"latencyMs,omitempty" jsonschema:"fixed added latency in milliseconds"`
	JitterMS        int64    `json:"jitterMs,omitempty" jsonschema:"latency jitter in milliseconds"`
	StatusCode      int      `json:"statusCode,omitempty" jsonschema:"synthetic HTTP status from 400 through 599"`
	Abort           bool     `json:"abort,omitempty" jsonschema:"abort matching traffic"`
	Probability     *float64 `json:"probability,omitempty" jsonschema:"match probability greater than zero through one, default one"`
	Method          string   `json:"method,omitempty" jsonschema:"optional HTTP method filter"`
	Path            string   `json:"path,omitempty" jsonschema:"optional HTTP path glob"`
	DurationSeconds int      `json:"durationSeconds" jsonschema:"finite duration from 1 through 3600 seconds"`
}

type disabledFaultsOutput struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Disabled    int64  `json:"disabled"`
}

type environmentListOutput struct {
	Scope         string                 `json:"scope"`
	Capabilities  []string               `json:"capabilities"`
	UntrustedData bool                   `json:"untrustedData"`
	Environments  []environmentView      `json:"environments"`
	Total         int                    `json:"total"`
	Remediation   []contract.Remediation `json:"remediation"`
}

type environmentOutput struct {
	UntrustedData bool            `json:"untrustedData"`
	Environment   environmentView `json:"environment"`
}

type serviceOutput struct {
	Project       string           `json:"project"`
	Environment   string           `json:"environment"`
	UntrustedData bool             `json:"untrustedData"`
	Service       serviceView      `json:"service"`
	Incoming      []connectionView `json:"incoming"`
	Outgoing      []connectionView `json:"outgoing"`
}

type configurationOutput struct {
	Project       string                   `json:"project"`
	Environment   string                   `json:"environment"`
	Configuration serviceConfigurationView `json:"configuration"`
}

type connectionsOutput struct {
	Project     string           `json:"project"`
	Environment string           `json:"environment"`
	Connections []connectionView `json:"connections"`
}

type connectionOutput struct {
	Project     string         `json:"project"`
	Environment string         `json:"environment"`
	Connection  connectionView `json:"connection"`
}

type logsOutput struct {
	Project       string         `json:"project"`
	Environment   string         `json:"environment"`
	Service       string         `json:"service,omitempty"`
	UntrustedData bool           `json:"untrustedData"`
	Entries       []logEntryView `json:"entries"`
}

type trafficOutput struct {
	Project       string               `json:"project"`
	Environment   string               `json:"environment"`
	UntrustedData bool                 `json:"untrustedData"`
	Exchanges     []trafficSummaryView `json:"exchanges"`
}

type recordingsOutput struct {
	Project     string               `json:"project"`
	Environment string               `json:"environment"`
	Recordings  []contract.Recording `json:"recordings"`
}

type recordingOutput struct {
	Project     string             `json:"project"`
	Environment string             `json:"environment"`
	Recording   contract.Recording `json:"recording"`
}

type faultsOutput struct {
	Project     string               `json:"project"`
	Environment string               `json:"environment"`
	Faults      []contract.FaultRule `json:"faults"`
}

type faultOutput struct {
	Project     string             `json:"project"`
	Environment string             `json:"environment"`
	Fault       contract.FaultRule `json:"fault"`
}

type operationsOutput struct {
	Project       string               `json:"project"`
	Environment   string               `json:"environment"`
	UntrustedData bool                 `json:"untrustedData"`
	Operations    []contract.Operation `json:"operations"`
}

type operationOutput struct {
	Project       string             `json:"project"`
	Environment   string             `json:"environment"`
	UntrustedData bool               `json:"untrustedData"`
	Operation     contract.Operation `json:"operation"`
}

type timelineOutput struct {
	Project       string                   `json:"project"`
	Environment   string                   `json:"environment"`
	UntrustedData bool                     `json:"untrustedData"`
	Timeline      []contract.TimelineEvent `json:"timeline"`
}

type trafficDetailOutput struct {
	Project              string                   `json:"project"`
	Environment          string                   `json:"environment"`
	UntrustedData        bool                     `json:"untrustedData"`
	Exchange             contract.TrafficExchange `json:"exchange"`
	HeadersTruncated     bool                     `json:"headersTruncated"`
	RequestMCPTruncated  bool                     `json:"requestMcpTruncated"`
	ResponseMCPTruncated bool                     `json:"responseMcpTruncated"`
}

type environmentView struct {
	Project        string               `json:"project"`
	Name           string               `json:"name"`
	Revision       int64                `json:"revision"`
	Status         string               `json:"status"`
	Reason         string               `json:"reason,omitempty"`
	PrimaryService string               `json:"primaryService,omitempty"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
	DashboardURL   string               `json:"dashboardUrl,omitempty"`
	Sources        []sourceView         `json:"sources"`
	Bindings       []bindingView        `json:"bindings"`
	Services       []serviceView        `json:"services"`
	Connections    []declaredConnection `json:"connections"`
	Issues         []issueView          `json:"issues"`
}

type sourceView struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Status    string    `json:"status"`
	Warnings  []string  `json:"warnings"`
	ScannedAt time.Time `json:"scannedAt"`
}

type bindingView struct {
	Service              string `json:"service"`
	Provider             string `json:"provider"`
	Source               string `json:"source,omitempty"`
	RemoteURL            string `json:"remoteUrl,omitempty"`
	RemoteClassification string `json:"remoteClassification,omitempty"`
	WritePolicy          string `json:"writePolicy,omitempty"`
	HealthPath           string `json:"healthPath,omitempty"`
}

type serviceView struct {
	Name            string         `json:"name"`
	Kind            string         `json:"kind"`
	Framework       string         `json:"framework,omitempty"`
	ResourceType    string         `json:"resourceType,omitempty"`
	ResourceVersion string         `json:"resourceVersion,omitempty"`
	Required        bool           `json:"required"`
	LaunchMode      string         `json:"launchMode,omitempty"`
	Debugger        *debuggerView  `json:"debugger,omitempty"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	Generation      int64          `json:"generation"`
	Endpoints       []endpointView `json:"endpoints"`
	StartedAt       *time.Time     `json:"startedAt,omitempty"`
	RestartCount    int64          `json:"restartCount"`
	RecentRequests  int64          `json:"recentRequests"`
	P95Millis       int64          `json:"p95Millis,omitempty"`
	Health          healthView     `json:"health"`
}

type debuggerView struct {
	Adapter string `json:"adapter"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	State   string `json:"state"`
}

type endpointView struct {
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	URL      string `json:"url"`
}

type healthView struct {
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	Timeout  string `json:"timeout"`
	Interval string `json:"interval"`
}

type serviceConfigurationView struct {
	Service          string                   `json:"service"`
	Command          []string                 `json:"command"`
	WorkingDirectory string                   `json:"workingDirectory,omitempty"`
	PortEnvironment  string                   `json:"portEnvironment,omitempty"`
	Environment      []configurationValueView `json:"environment"`
	Health           healthView               `json:"health"`
}

type configurationValueView struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	Classification string `json:"classification"`
	Source         string `json:"source"`
}

type declaredConnection struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Protocol    string `json:"protocol"`
	Binding     string `json:"binding,omitempty"`
	Environment string `json:"environment,omitempty"`
	Required    bool   `json:"required"`
}

type connectionView struct {
	Source              string            `json:"source"`
	Target              string            `json:"target"`
	Protocol            string            `json:"protocol"`
	Binding             string            `json:"binding,omitempty"`
	Required            bool              `json:"required"`
	TargetProvider      string            `json:"targetProvider"`
	TargetStatus        string            `json:"targetStatus"`
	Endpoint            *endpointView     `json:"endpoint,omitempty"`
	InjectedEnvironment map[string]string `json:"injectedEnvironment"`
}

type issueView struct {
	Code        string `json:"code"`
	Subject     string `json:"subject,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type logEntryView struct {
	Timestamp  time.Time `json:"timestamp"`
	Service    string    `json:"service"`
	Stream     string    `json:"stream"`
	Generation int64     `json:"generation"`
	Message    string    `json:"message"`
	Truncated  bool      `json:"truncated"`
}

type trafficSummaryView struct {
	Sequence             int64     `json:"sequence"`
	Protocol             string    `json:"protocol"`
	Source               string    `json:"source"`
	Target               string    `json:"target"`
	TargetProvider       string    `json:"targetProvider,omitempty"`
	RemoteClassification string    `json:"remoteClassification,omitempty"`
	StartedAt            time.Time `json:"startedAt"`
	CompletedAt          time.Time `json:"completedAt"`
	Method               string    `json:"method,omitempty"`
	Path                 string    `json:"path,omitempty"`
	Status               int       `json:"status,omitempty"`
	DurationMS           int64     `json:"durationMs"`
	RequestBytes         int64     `json:"requestBytes"`
	ResponseBytes        int64     `json:"responseBytes"`
	Fault                string    `json:"fault,omitempty"`
	Recording            string    `json:"recording,omitempty"`
	Error                string    `json:"error,omitempty"`
}

func environmentResult(value contract.Environment) environmentView {
	result := environmentView{
		Project: value.Project, Name: value.Name, Revision: value.Revision,
		Status: string(value.Status), Reason: value.Reason, PrimaryService: value.PrimaryService,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, DashboardURL: value.DashboardURL,
		Sources: []sourceView{}, Bindings: []bindingView{}, Services: []serviceView{},
		Connections: []declaredConnection{}, Issues: []issueView{},
	}
	for _, source := range value.Sources {
		result.Sources = append(result.Sources, sourceView{
			Name: source.Name, Path: source.Path, Status: source.Status,
			Warnings: nonNilStrings(source.Warnings), ScannedAt: source.ScannedAt,
		})
	}
	for _, binding := range value.Bindings {
		view := bindingView{Service: binding.Service, Provider: string(binding.Provider), Source: binding.Source}
		if binding.Remote != nil {
			view.RemoteURL = binding.Remote.URL
			view.RemoteClassification = string(binding.Remote.Classification)
			view.WritePolicy = string(binding.Remote.WritePolicy)
			view.HealthPath = binding.Remote.HealthPath
		}
		result.Bindings = append(result.Bindings, view)
	}
	for _, service := range value.Services {
		result.Services = append(result.Services, serviceResult(service))
	}
	for _, connection := range value.Connections {
		result.Connections = append(result.Connections, declaredConnection{
			Source: connection.Source, Target: connection.Target, Protocol: string(connection.Protocol),
			Binding: connection.Binding, Environment: connection.Environment, Required: connection.Required,
		})
	}
	for _, issue := range value.Issues {
		result.Issues = append(result.Issues, issueView{Code: issue.Code, Subject: issue.Subject, Message: issue.Message, Remediation: issue.Remediation})
	}
	return result
}

func serviceResult(value contract.Service) serviceView {
	result := serviceView{
		Name: value.Name, Kind: string(value.Kind), Framework: value.Framework,
		Required: value.Required, LaunchMode: string(value.LaunchMode), Status: string(value.Status),
		Reason: value.Reason, Generation: value.Generation, Endpoints: []endpointView{},
		StartedAt: value.StartedAt, RestartCount: value.RestartCount,
		RecentRequests: value.RecentRequest, P95Millis: value.P95Millis,
		Health: healthView{Kind: value.Health.Kind, Path: value.Health.Path, Timeout: value.Health.Timeout.String(), Interval: value.Health.Interval.String()},
	}
	if value.Resource != nil {
		result.ResourceType = value.Resource.Type
		result.ResourceVersion = value.Resource.Version
	}
	if value.Debugger != nil {
		result.Debugger = &debuggerView{Adapter: string(value.Debugger.Adapter), Host: value.Debugger.Host, Port: value.Debugger.Port, State: value.Debugger.State}
	}
	for _, endpoint := range value.Endpoints {
		result.Endpoints = append(result.Endpoints, endpointResult(string(endpoint.Kind), string(endpoint.Protocol), endpoint.Host, endpoint.Port, endpoint.URL))
	}
	return result
}

func connectionResult(value contract.EffectiveConnection) connectionView {
	result := connectionView{
		Source: value.Source, Target: value.Target, Protocol: string(value.Protocol),
		Binding: value.Binding, Required: value.Required, TargetProvider: string(value.TargetProvider),
		TargetStatus: string(value.TargetStatus), InjectedEnvironment: nonNilMap(value.InjectedEnvironment),
	}
	if value.Endpoint != nil {
		endpoint := endpointResult(string(value.Endpoint.Kind), string(value.Endpoint.Protocol), value.Endpoint.Host, value.Endpoint.Port, value.Endpoint.URL)
		result.Endpoint = &endpoint
	}
	return result
}

func configurationResult(value contract.ServiceConfiguration) serviceConfigurationView {
	result := serviceConfigurationView{
		Service: value.Service, Command: nonNilStrings(value.Command),
		WorkingDirectory: value.WorkingDirectory, PortEnvironment: value.PortEnvironment,
		Environment: []configurationValueView{},
		Health:      healthView{Kind: value.Health.Kind, Path: value.Health.Path, Timeout: value.Health.Timeout.String(), Interval: value.Health.Interval.String()},
	}
	for _, item := range value.Environment {
		result.Environment = append(result.Environment, configurationValueView{
			Key: item.Key, Value: item.Value, Classification: item.Classification, Source: item.Source,
		})
	}
	return result
}

func endpointResult(kind, protocol, host string, port int, url string) endpointView {
	return endpointView{Kind: kind, Protocol: protocol, Host: host, Port: port, URL: url}
}

func trafficSummaryResult(value contract.TrafficExchange) trafficSummaryView {
	return trafficSummaryView{
		Sequence: value.Sequence, Protocol: string(value.Protocol), Source: value.Source, Target: value.Target,
		TargetProvider: string(value.TargetProvider), RemoteClassification: string(value.RemoteClassification),
		StartedAt: value.StartedAt, CompletedAt: value.CompletedAt, Method: value.Method, Path: value.Path,
		Status: value.Status, DurationMS: value.DurationMS, RequestBytes: value.RequestBytes,
		ResponseBytes: value.ResponseBytes, Fault: value.Fault, Recording: value.Recording, Error: value.Error,
	}
}

func bounded(value, defaultValue, maximum int, label string) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 0 || value > maximum {
		return 0, codedError{code: "INVALID_ARGUMENT", message: fmt.Sprintf("%s must be between 1 and %d", label, maximum)}
	}
	return value, nil
}

func validateServiceName(name string) error {
	if err := contract.ValidateServiceName(name); err != nil {
		return codedError{code: "INVALID_ARGUMENT", message: err.Error()}
	}
	return nil
}

func validateArtifactName(name, label string) error {
	if err := contract.ValidateArtifactName(name); err != nil {
		return codedError{code: "INVALID_ARGUMENT", message: label + " " + err.Error()}
	}
	return nil
}

func validateConnectionSource(name string) error {
	if name == "external" {
		return nil
	}
	return validateServiceName(name)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func truncateUTF8(value string, maximum int) (string, bool) {
	if len(value) <= maximum {
		return value, false
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

func capHeaders(values map[string][]string, maximum int) (map[string][]string, bool) {
	if values == nil {
		return map[string][]string{}, false
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make(map[string][]string)
	used := 0
	truncated := false
	for _, name := range names {
		for _, value := range values[name] {
			remaining := maximum - used - len(name)
			if remaining <= 0 {
				truncated = true
				break
			}
			capped, cut := truncateUTF8(value, remaining)
			result[name] = append(result[name], capped)
			used += len(name) + len(capped)
			truncated = truncated || cut
		}
		if used >= maximum {
			truncated = true
			break
		}
	}
	return result, truncated
}

func capabilityNames(config Config) []string {
	result := []string{"inspection"}
	if config.AllowSensitiveTraffic {
		result = append(result, "sensitive-traffic")
	}
	if config.AllowLifecycle {
		result = append(result, "lifecycle")
	}
	if config.AllowTrafficControl {
		result = append(result, "traffic-control")
	}
	return result
}

func scopeName(config Config) string {
	switch {
	case config.Environment != "":
		return "environment:" + config.Environment
	case config.AllEnvironments:
		return "all-environments"
	default:
		return "workspace:" + config.WorkspaceRoot
	}
}

func validProtocol(value string) bool {
	return value == "" || value == "http" || value == "tcp"
}

func validateEdge(value string) error {
	if value == "" {
		return nil
	}
	source, target, ok := strings.Cut(value, ":")
	if !ok || strings.Contains(target, ":") {
		return codedError{code: "INVALID_ARGUMENT", message: "edge must use source:target"}
	}
	if err := validateConnectionSource(source); err != nil {
		return err
	}
	return validateServiceName(target)
}
