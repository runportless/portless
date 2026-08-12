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
	starting := false
	recovering := false
	stopping := false
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
		case ServiceStarting:
			starting = true
			running++
		case ServiceRecovering:
			recovering = true
			running++
		case ServiceStopping:
			stopping = true
			running++
		}
	}
	if ready == len(services) {
		return EnvironmentHealthy, ""
	}
	if recovering {
		return EnvironmentRecovering, "services are being recovered"
	}
	if starting {
		return EnvironmentStarting, "services are starting"
	}
	if stopping {
		return EnvironmentStopping, "services are stopping"
	}
	if running == 0 {
		return EnvironmentStopped, ""
	}
	return EnvironmentDegraded, "not all services are ready"
}
