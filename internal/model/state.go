package model

func DeriveEnvironmentStatus(services []Service, activeOperation string) (EnvironmentStatus, string) {
	switch activeOperation {
	case "up":
		return EnvironmentStarting, "services are starting"
	case "down":
		return EnvironmentStopping, "services are stopping"
	}
	if len(services) == 0 {
		return EnvironmentStopped, "no services are defined"
	}
	ready := 0
	running := 0
	for _, service := range services {
		switch service.Status {
		case ServiceFailed:
			if service.Required {
				return EnvironmentFailed, service.Name + " failed"
			}
			return EnvironmentDegraded, service.Name + " failed"
		case ServiceUnknown:
			if service.Required {
				return EnvironmentUnknown, service.Name + " state cannot be verified"
			}
		case ServiceUnhealthy, ServiceExited:
			if service.Required {
				return EnvironmentDegraded, service.Name + " is not ready"
			}
		case ServiceReady:
			ready++
			running++
		case ServiceStarting, ServiceStopping:
			running++
		}
	}
	if ready == len(services) {
		return EnvironmentHealthy, ""
	}
	if running == 0 {
		return EnvironmentStopped, ""
	}
	return EnvironmentDegraded, "not all services are ready"
}
