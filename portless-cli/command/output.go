package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-relay"
)

// PrintStatus writes the human-readable status table for an environment.
func (c *Context) PrintStatus(environment model.Environment) {
	ready := 0
	for _, service := range environment.Services {
		if service.Status == model.ServiceReady {
			ready++
		}
	}
	fmt.Fprintf(c.Out, "%s  %s  %d/%d ready\n\n", c.Heading(c.Out, environment.Project+"/"+environment.Name), c.State(c.Out, string(environment.Status)), ready, len(environment.Services))
	fmt.Fprintln(c.Out, c.Muted(c.Out, "SERVICE                 PROVIDER    MODE         KIND        STATE          ENDPOINT"))
	for _, service := range environment.Services {
		kind := string(service.Kind)
		if service.Framework != "" {
			kind = service.Framework
		} else if service.Resource != nil {
			kind = service.Resource.Type
		}
		provider := "local"
		for _, binding := range environment.Bindings {
			if strings.EqualFold(binding.Service, service.Name) {
				provider = string(binding.Provider)
				break
			}
		}
		fmt.Fprintf(c.Out, "%-23s %-11s %-12s %-11s %s %s\n", service.Name, provider, ServiceMode(environment, service), kind, c.State(c.Out, fmt.Sprintf("%-14s", service.Status)), c.Accent(c.Out, StatusEndpoint(service)))
	}
	fmt.Fprintln(c.Out, "\nDashboard:", c.Accent(c.Out, environment.DashboardURL))
}

// PrintDebugGuidance writes debugger endpoints and IDE attachment guidance for
// services currently running in debug mode.
func (c *Context) PrintDebugGuidance(environment model.Environment) {
	var debugging []model.Service
	for _, service := range environment.Services {
		if service.LaunchMode == model.LaunchDebug && service.Debugger != nil {
			debugging = append(debugging, service)
		}
	}
	if len(debugging) == 0 {
		return
	}
	fmt.Fprintln(c.Out, "\n"+c.Heading(c.Out, "Debuggers"))
	for _, service := range debugging {
		fmt.Fprintf(c.Out, "  %-18s %s at %s:%d\n", service.Name, service.Debugger.Adapter, service.Debugger.Host, service.Debugger.Port)
	}
	fmt.Fprintln(c.Out, "\nUse your IDE's Attach to Process action and choose the matching Node or JVM process. No run configuration or environment file is required.")
}

// ServiceMode returns the displayed launch mode for a local process service.
// Non-local and non-process services have no launch mode and return an em dash.
func ServiceMode(environment model.Environment, service model.Service) string {
	if ProviderFor(environment, service.Name) != model.ProviderLocal || service.Kind != model.ServiceProcess {
		return "—"
	}
	if service.LaunchMode == "" {
		return string(model.LaunchManaged)
	}
	return string(service.LaunchMode)
}

// StatusEndpoint returns the service's primary public endpoint URL, or an
// empty string when no public endpoint is available.
func StatusEndpoint(service model.Service) string {
	if endpoint := PrimaryServiceEndpoint(service); endpoint != nil {
		return endpoint.URL
	}
	return ""
}

// PrimaryServiceEndpoint returns the first public endpoint exposed by service.
func PrimaryServiceEndpoint(service model.Service) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic {
			return &service.Endpoints[index]
		}
	}
	return nil
}

// ServiceEndpointForProtocol returns the first public endpoint for protocol.
func ServiceEndpointForProtocol(service model.Service, protocol model.Protocol) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic && service.Endpoints[index].Protocol == protocol {
			return &service.Endpoints[index]
		}
	}
	return nil
}

// ProviderFor returns the configured provider for service, defaulting to the
// local provider when no explicit binding exists.
func ProviderFor(environment model.Environment, service string) model.ProviderKind {
	for _, binding := range environment.Bindings {
		if strings.EqualFold(binding.Service, service) {
			return binding.Provider
		}
	}
	return model.ProviderLocal
}

// PrintOperation writes a compact human-readable operation result.
func (c *Context) PrintOperation(operation model.Operation) {
	fmt.Fprintf(c.Out, "%s operation %d %s\n", operation.Type, operation.Number, c.State(c.Out, operation.State))
}

// PrintError writes err using the active human-readable or JSON error format.
func (c *Context) PrintError(err error) {
	var clientErr *apiclient.ClientError
	if c.JSONOutput {
		detail := errorDetail{Code: "COMMAND_FAILED", Message: err.Error()}
		var usage *UsageFailure
		if errors.As(err, &usage) || IsCobraSyntaxError(err) {
			detail.Code = "USAGE_ERROR"
		}
		if errors.As(err, &clientErr) {
			detail = errorDetail{
				Code: clientErr.Code, Message: clientErr.Message, Status: clientErr.Status,
				Subject: clientErr.Subject, Details: clientErr.Details, Remediation: clientErr.Remediation,
			}
			if detail.Code == "" {
				detail.Code = "API_ERROR"
			}
		}
		_ = WriteJSON(c.Err, errorOutput{Error: detail})
		return
	}
	if errors.As(err, &clientErr) {
		fmt.Fprintf(c.Err, "%s %s\n", c.Failure(c.Err, "portless:"), clientErr.Message)
		if clientErr.Code != "" {
			fmt.Fprintf(c.Err, "%s %s\n", c.Muted(c.Err, "code:"), clientErr.Code)
		}
		for _, remediation := range clientErr.Remediation {
			if remediation.Command != "" {
				fmt.Fprintln(c.Err, c.Accent(c.Err, "next:"), remediation.Command)
			}
			if remediation.URL != "" {
				fmt.Fprintln(c.Err, c.Accent(c.Err, "inspect:"), remediation.URL)
			}
		}
		return
	}
	fmt.Fprintln(c.Err, c.Failure(c.Err, "portless:"), err)
}

// WriteJSON emits value as indented JSON followed by a newline.
func WriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// WriteJSONLine emits value as one compact JSON document followed by a newline.
func WriteJSONLine(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

// WriteErrorOutput emits the stable JSON error envelope used when a command
// has already printed a partial machine-readable result.
func WriteErrorOutput(writer io.Writer, code, message string) error {
	return WriteJSON(writer, errorOutput{Error: errorDetail{Code: code, Message: message}})
}

// WriteRelayStatusJSON emits relay installation details together with their
// computed aggregate state.
func WriteRelayStatusJSON(writer io.Writer, status relay.InstallationStatus) error {
	return WriteJSON(writer, relayStatusOutput{State: status.State(), InstallationStatus: status})
}

// PrintEnvironmentListHeader writes the heading shared by environment lists.
func (c *Context) PrintEnvironmentListHeader() {
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-32s %-14s %s", "ENVIRONMENT", "STATE", "SERVICES")))
}

// PrintWarnings writes warnings to stderr unless machine-readable output is
// active.
func (c *Context) PrintWarnings(warnings []string) {
	if c.JSONOutput {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintln(c.Err, c.Warning(c.Err, "warning:"), warning)
	}
}

// NonNilStrings converts a nil string slice to an empty slice so JSON output
// consistently emits an array rather than null.
func NonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// ErrorString returns err's message, or an empty string when err is nil.
func ErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// EmptyAs returns fallback when value is empty.
func EmptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
