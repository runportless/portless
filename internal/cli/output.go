package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/model"
	relayinstall "github.com/portless-run/portless/internal/relay/install"
)

func (c *CLI) printStatus(environment model.Environment) {
	ready := 0
	for _, service := range environment.Services {
		if service.Status == model.ServiceReady {
			ready++
		}
	}
	fmt.Fprintf(c.Out, "%s  %s  %d/%d ready\n\n", c.heading(c.Out, environment.Project+"/"+environment.Name), c.state(c.Out, string(environment.Status)), ready, len(environment.Services))
	fmt.Fprintln(c.Out, c.muted(c.Out, "SERVICE                 PROVIDER    MODE         KIND        STATE          ENDPOINT"))
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
		fmt.Fprintf(c.Out, "%-23s %-11s %-12s %-11s %s %s\n", service.Name, provider, serviceMode(environment, service), kind, c.state(c.Out, fmt.Sprintf("%-14s", service.Status)), c.accent(c.Out, statusEndpoint(service)))
	}
	fmt.Fprintln(c.Out, "\nDashboard:", c.accent(c.Out, environment.DashboardURL))
}

func (c *CLI) printDebugGuidance(environment model.Environment) {
	var debugging []model.Service
	for _, service := range environment.Services {
		if service.LaunchMode == model.LaunchDebug && service.Debugger != nil {
			debugging = append(debugging, service)
		}
	}
	if len(debugging) == 0 {
		return
	}
	fmt.Fprintln(c.Out, "\n"+c.heading(c.Out, "Debuggers"))
	for _, service := range debugging {
		fmt.Fprintf(c.Out, "  %-18s %s at %s:%d\n", service.Name, service.Debugger.Adapter, service.Debugger.Host, service.Debugger.Port)
	}
	fmt.Fprintln(c.Out, "\nUse your IDE's Attach to Process action and choose the matching Node or JVM process. No run configuration or environment file is required.")
}

func serviceMode(environment model.Environment, service model.Service) string {
	if providerFor(environment, service.Name) != model.ProviderLocal || service.Kind != model.ServiceProcess {
		return "—"
	}
	if service.LaunchMode == "" {
		return string(model.LaunchManaged)
	}
	return string(service.LaunchMode)
}

func statusEndpoint(service model.Service) string {
	if endpoint := primaryServiceEndpoint(service); endpoint != nil {
		return endpoint.URL
	}
	return ""
}

func primaryServiceEndpoint(service model.Service) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic {
			return &service.Endpoints[index]
		}
	}
	return nil
}

func serviceEndpointForProtocol(service model.Service, protocol model.Protocol) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic && service.Endpoints[index].Protocol == protocol {
			return &service.Endpoints[index]
		}
	}
	return nil
}

func (c *CLI) printOperation(operation model.Operation) {
	fmt.Fprintf(c.Out, "%s operation %d %s\n", operation.Type, operation.Number, c.state(c.Out, operation.State))
}

func (c *CLI) printError(err error) {
	var clientErr *apiclient.ClientError
	if c.jsonOutput {
		detail := errorDetail{Code: "COMMAND_FAILED", Message: err.Error()}
		var usage *commandUsageError
		if errors.As(err, &usage) || isCobraSyntaxError(err) {
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
		_ = writeJSON(c.Err, errorOutput{Error: detail})
		return
	}
	if errors.As(err, &clientErr) {
		fmt.Fprintf(c.Err, "%s %s\n", c.failure(c.Err, "portless:"), clientErr.Message)
		if clientErr.Code != "" {
			fmt.Fprintf(c.Err, "%s %s\n", c.muted(c.Err, "code:"), clientErr.Code)
		}
		for _, remediation := range clientErr.Remediation {
			if remediation.Command != "" {
				fmt.Fprintln(c.Err, c.accent(c.Err, "next:"), remediation.Command)
			}
			if remediation.URL != "" {
				fmt.Fprintln(c.Err, c.accent(c.Err, "inspect:"), remediation.URL)
			}
		}
		return
	}
	fmt.Fprintln(c.Err, c.failure(c.Err, "portless:"), err)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSONLine(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeRelayStatusJSON(writer io.Writer, status relayinstall.InstallationStatus) error {
	return writeJSON(writer, relayStatusOutput{State: status.State(), InstallationStatus: status})
}

func (c *CLI) printEnvironmentListHeader() {
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-32s %-14s %s", "ENVIRONMENT", "STATE", "SERVICES")))
}

func (c *CLI) printWarnings(warnings []string) {
	if c.jsonOutput {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintln(c.Err, c.warning(c.Err, "warning:"), warning)
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func emptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
