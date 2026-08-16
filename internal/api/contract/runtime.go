package contract

type RuntimeName string

const (
	RuntimeAuto   RuntimeName = "auto"
	RuntimeDocker RuntimeName = "docker"
	RuntimePodman RuntimeName = "podman"
)

type RuntimeProbe struct {
	Name    RuntimeName `json:"name"`
	State   string      `json:"state"`
	Version string      `json:"version,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

type RuntimeStatus struct {
	Preference RuntimeName    `json:"preference"`
	Selected   RuntimeName    `json:"selected,omitempty"`
	State      string         `json:"state"`
	Version    string         `json:"version,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Candidates []RuntimeProbe `json:"candidates"`
}

type UseRuntimeRequest struct {
	Preference string `json:"preference"`
}

type RuntimeResetResult struct {
	Runtime    RuntimeName `json:"runtime"`
	Containers int         `json:"containers"`
	Volumes    int         `json:"volumes"`
	Networks   int         `json:"networks"`
}

type ResetPlan struct {
	Projects                  int      `json:"projects"`
	Environments              int      `json:"environments"`
	ManagedVolumeEnvironments int      `json:"managedVolumeEnvironments"`
	ActiveEnvironments        []string `json:"activeEnvironments"`
	TopologyIncompatible      bool     `json:"topologyIncompatible"`
}

type PrepareResetRequest struct {
	Force bool `json:"force"`
}

type PrepareResetResponse struct {
	Processes int                  `json:"processes"`
	Runtimes  []RuntimeResetResult `json:"runtimes"`
}
