package networking

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/portless-run/portless/portless-daemon/model"
)

const (
	// DNSZone is the authoritative suffix for clean TCP endpoint names.
	DNSZone = "portless.test"

	// EndpointPoolSize is deliberately bounded. Linux accepts the entire IPv4
	// loopback range by default; macOS requires each address to be provisioned
	// on lo0 by the installed relay before it drops privileges.
	EndpointPoolSize = 64

	// AllocationPublic identifies a host-accessible service endpoint.
	AllocationPublic = "public"
	// AllocationConnection identifies a source-scoped dependency endpoint.
	AllocationConnection = "connection"
)

// EndpointLoopbackIP returns the stable loopback address at index, or an empty
// string when index is outside the managed pool.
func EndpointLoopbackIP(index int) string {
	if index < 0 || index >= EndpointPoolSize {
		return ""
	}
	return fmt.Sprintf("127.77.0.%d", index+2)
}

// EndpointLoopbackAddresses returns every address in the managed endpoint pool.
func EndpointLoopbackAddresses() []string {
	addresses := make([]string, EndpointPoolSize)
	for index := range addresses {
		addresses[index] = EndpointLoopbackIP(index)
	}
	return addresses
}

// AllocationSpec describes one stable DNS name, loopback address allocation,
// and advertised port requirement.
type AllocationSpec struct {
	Kind     string
	Source   string
	Target   string
	Protocol model.Protocol
	DNSName  string
	Port     int
}

// AllocationSpecs returns every stable TCP endpoint owned by an environment.
// HTTP continues to use the host-aware port-80 ingress and therefore does not
// consume an address from the loopback allocation pool.
func AllocationSpecs(project, environment string, definition model.ProjectModel) ([]AllocationSpec, error) {
	services := make(map[string]model.ServiceDefinition, len(definition.Services))
	for _, service := range definition.Services {
		services[strings.ToLower(service.Name)] = service
	}
	result := make([]AllocationSpec, 0, len(definition.Connections)*2)
	public := make(map[string]model.Protocol)
	seenNames := make(map[string]struct{})
	for _, service := range definition.Services {
		if protocol, ok := serviceTCPProtocol(service); ok {
			public[strings.ToLower(service.Name)] = protocol
		}
	}
	for _, connection := range definition.Connections {
		if connection.Protocol == model.ProtocolHTTP {
			continue
		}
		target, ok := services[strings.ToLower(connection.Target)]
		if !ok {
			return nil, fmt.Errorf("connection target %s is not defined", connection.Target)
		}
		port, err := AdvertisedPort(target, connection.Protocol)
		if err != nil {
			return nil, fmt.Errorf("%s:%s: %w", connection.Source, connection.Target, err)
		}
		if previous, exists := public[strings.ToLower(connection.Target)]; exists && previous != connection.Protocol {
			return nil, fmt.Errorf("service %s is reached with both %s and %s; one public TCP service may expose only one protocol", connection.Target, previous, connection.Protocol)
		}
		public[strings.ToLower(connection.Target)] = connection.Protocol
		name := ConnectionDNSName(connection.Source, connection.Target, environment, project)
		if _, duplicate := seenNames[name]; duplicate {
			continue
		}
		seenNames[name] = struct{}{}
		result = append(result, AllocationSpec{
			Kind: AllocationConnection, Source: connection.Source, Target: connection.Target,
			Protocol: connection.Protocol, DNSName: name, Port: port,
		})
	}
	for _, service := range definition.Services {
		protocol, ok := public[strings.ToLower(service.Name)]
		if !ok {
			continue
		}
		port, err := AdvertisedPort(service, protocol)
		if err != nil {
			return nil, err
		}
		result = append(result, AllocationSpec{
			Kind: AllocationPublic, Target: service.Name, Protocol: protocol,
			DNSName: PublicDNSName(service.Name, environment, project), Port: port,
		})
	}
	return result, nil
}

// serviceTCPProtocol identifies services that should be reachable directly
// from host tools even when no application connection currently targets them.
func serviceTCPProtocol(service model.ServiceDefinition) (model.Protocol, bool) {
	if service.Kind == model.ServiceResource || service.Port > 0 {
		return model.ProtocolTCP, true
	}
	return "", false
}

// AdvertisedPort validates and returns a service's client-facing TCP port.
func AdvertisedPort(service model.ServiceDefinition, protocol model.Protocol) (int, error) {
	port := service.Port
	if port == 0 {
		switch protocol {
		case model.ProtocolTCP:
			return 0, fmt.Errorf("generic TCP service %s must declare a client-facing port", service.Name)
		default:
			return 0, fmt.Errorf("protocol %s does not use a stable TCP endpoint", protocol)
		}
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("service %s client-facing port must be between 1 and 65535", service.Name)
	}
	return port, nil
}

// PublicDNSName returns the canonical host-accessible TCP name for a service.
func PublicDNSName(service, environment, project string) string {
	return strings.ToLower(strings.Join([]string{service, environment, project, DNSZone}, "."))
}

// ConnectionDNSName returns the canonical source-scoped TCP dependency name.
func ConnectionDNSName(source, target, environment, project string) string {
	return strings.ToLower(strings.Join([]string{target, "via-" + source, environment, project, DNSZone}, "."))
}

// EndpointURL formats a host and port as an HTTP or TCP endpoint URL.
func EndpointURL(protocol model.Protocol, host string, port int) string {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	switch protocol {
	case model.ProtocolHTTP:
		return "http://" + address
	default:
		return "tcp://" + address
	}
}

// HTTPURL returns the clean localhost URL for an application service.
func HTTPURL(service, environment, project string) string {
	return "http://" + strings.ToLower(strings.Join([]string{service, environment, project, "localhost"}, "."))
}

// ValidateDNSName verifies lowercase-compatible DNS length and label rules.
func ValidateDNSName(name string) error {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || len(name) > 253 {
		return errors.New("DNS name must contain between 1 and 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid DNS label %q", label)
		}
		for _, character := range label {
			if character < 'a' || character > 'z' {
				if character < '0' || character > '9' {
					if character != '-' {
						return fmt.Errorf("invalid DNS label %q", label)
					}
				}
			}
		}
	}
	return nil
}
