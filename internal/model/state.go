package model

func DeriveProjectStatus(services []Service, activeOperation string) (ProjectStatus, string) {
	switch activeOperation {
	case "up":
		return ProjectStarting, "services are starting"
	case "down":
		return ProjectStopping, "services are stopping"
	}
	if len(services) == 0 {
		return ProjectStopped, "no services are defined"
	}
	ready := 0
	running := 0
	for _, service := range services {
		switch service.Status {
		case ServiceFailed:
			if service.Required {
				return ProjectFailed, service.Name + " failed"
			}
			return ProjectDegraded, service.Name + " failed"
		case ServiceUnknown:
			if service.Required {
				return ProjectUnknown, service.Name + " state cannot be verified"
			}
		case ServiceUnhealthy, ServiceExited:
			if service.Required {
				return ProjectDegraded, service.Name + " is not ready"
			}
		case ServiceReady:
			ready++
			running++
		case ServiceStarting, ServiceStopping:
			running++
		}
	}
	if ready == len(services) {
		return ProjectHealthy, ""
	}
	if running == 0 {
		return ProjectStopped, ""
	}
	return ProjectDegraded, "not all services are ready"
}
