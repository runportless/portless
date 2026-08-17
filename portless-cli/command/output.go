package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-relay"
)

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

func ServiceMode(environment model.Environment, service model.Service) string {
	if ProviderFor(environment, service.Name) != model.ProviderLocal || service.Kind != model.ServiceProcess {
		return "—"
	}
	if service.LaunchMode == "" {
		return string(model.LaunchManaged)
	}
	return string(service.LaunchMode)
}

func StatusEndpoint(service model.Service) string {
	if endpoint := PrimaryServiceEndpoint(service); endpoint != nil {
		return endpoint.URL
	}
	return ""
}

func PrimaryServiceEndpoint(service model.Service) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic {
			return &service.Endpoints[index]
		}
	}
	return nil
}

func ServiceEndpointForProtocol(service model.Service, protocol model.Protocol) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic && service.Endpoints[index].Protocol == protocol {
			return &service.Endpoints[index]
		}
	}
	return nil
}

func ProviderFor(environment model.Environment, service string) model.ProviderKind {
	for _, binding := range environment.Bindings {
		if strings.EqualFold(binding.Service, service) {
			return binding.Provider
		}
	}
	return model.ProviderLocal
}

func (c *Context) PrintOperation(operation model.Operation) {
	fmt.Fprintf(c.Out, "%s operation %d %s\n", operation.Type, operation.Number, c.State(c.Out, operation.State))
}

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

func WriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

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

func WriteRelayStatusJSON(writer io.Writer, status relay.InstallationStatus) error {
	return WriteJSON(writer, relayStatusOutput{State: status.State(), InstallationStatus: status})
}

func (c *Context) PrintEnvironmentListHeader() {
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-32s %-14s %s", "ENVIRONMENT", "STATE", "SERVICES")))
}

func (c *Context) PrintWarnings(warnings []string) {
	if c.JSONOutput {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintln(c.Err, c.Warning(c.Err, "warning:"), warning)
	}
}

func NonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func ErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func EmptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
