package contract

// RuntimeName identifies a supported container-runtime selection.
type RuntimeName string

const (
	// RuntimeAuto selects the first available supported runtime.
	RuntimeAuto RuntimeName = "auto"
	// RuntimeDocker selects Docker explicitly.
	RuntimeDocker RuntimeName = "docker"
	// RuntimePodman selects Podman explicitly.
	RuntimePodman RuntimeName = "podman"
)

// RuntimeProbe describes the availability of one container-runtime candidate.
type RuntimeProbe struct {
	Name    RuntimeName `json:"name"`
	State   string      `json:"state"`
	Version string      `json:"version,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

// RuntimeStatus describes runtime preference, selection, and probe results.
type RuntimeStatus struct {
	Preference RuntimeName    `json:"preference"`
	Selected   RuntimeName    `json:"selected,omitempty"`
	State      string         `json:"state"`
	Version    string         `json:"version,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Candidates []RuntimeProbe `json:"candidates"`
}

// UseRuntimeRequest changes the persisted container-runtime preference.
type UseRuntimeRequest struct {
	Preference string `json:"preference"`
}

// RuntimeResetResult counts resources removed from one container runtime.
type RuntimeResetResult struct {
	Runtime    RuntimeName `json:"runtime"`
	Containers int         `json:"containers"`
	Volumes    int         `json:"volumes"`
	Networks   int         `json:"networks"`
}

// ResetPlan previews persistent and active state affected by a reset.
type ResetPlan struct {
	Projects                  int      `json:"projects"`
	Environments              int      `json:"environments"`
	ManagedVolumeEnvironments int      `json:"managedVolumeEnvironments"`
	ActiveEnvironments        []string `json:"activeEnvironments"`
	TopologyIncompatible      bool     `json:"topologyIncompatible"`
}

// PrepareResetRequest controls whether active runtime state may be terminated.
type PrepareResetRequest struct {
	Force bool `json:"force"`
}

// PrepareResetResponse summarizes runtime resources stopped before state removal.
type PrepareResetResponse struct {
	Processes int                  `json:"processes"`
	Runtimes  []RuntimeResetResult `json:"runtimes"`
}
