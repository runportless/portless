package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	portlessdns "github.com/portless-run/portless/internal/dns"
	"github.com/portless-run/portless/internal/networking"
)

const ControlOrigin = "http://portless.localhost"

type SetupRequest struct {
	Executable      string
	TargetSocket    string
	DNSTargetSocket string
	UID             int
	GID             int
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

type UninstallRequest struct {
	Executable string
	UID        int
	Force      bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type RestartRequest struct {
	Executable string
	UID        int
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type InstallationStatus struct {
	Platform             string     `json:"platform"`
	Service              string     `json:"service"`
	Installed            bool       `json:"installed"`
	Running              bool       `json:"running"`
	Healthy              bool       `json:"healthy"`
	HTTPHealthy          bool       `json:"httpHealthy"`
	HelperPresent        bool       `json:"helperPresent"`
	ConfigurationPresent bool       `json:"configurationPresent"`
	ReceiptPresent       bool       `json:"receiptPresent"`
	ResolverPresent      bool       `json:"resolverPresent"`
	ResolverHealthy      bool       `json:"resolverHealthy"`
	OwnerUID             int        `json:"ownerUid,omitempty"`
	OwnerGID             int        `json:"ownerGid,omitempty"`
	TargetSocket         string     `json:"targetSocket,omitempty"`
	DNSTargetSocket      string     `json:"dnsTargetSocket,omitempty"`
	DNSListenAddress     string     `json:"dnsListenAddress"`
	HelperPath           string     `json:"helperPath"`
	ConfigurationPath    string     `json:"configurationPath"`
	ReceiptPath          string     `json:"receiptPath"`
	ResolverPath         string     `json:"resolverPath,omitempty"`
	InstalledAt          *time.Time `json:"installedAt,omitempty"`
	HealthError          string     `json:"healthError,omitempty"`
	DNSHealthy           bool       `json:"dnsHealthy"`
	DNSHealthError       string     `json:"dnsHealthError,omitempty"`
	ResolverHealthError  string     `json:"resolverHealthError,omitempty"`
	EndpointPoolReady    bool       `json:"endpointPoolReady"`
	EndpointPoolDetail   string     `json:"endpointPoolDetail,omitempty"`
	Problem              string     `json:"problem,omitempty"`
}

func (status InstallationStatus) State() string {
	switch {
	case !status.Installed:
		return "not installed"
	case status.Healthy:
		return "ready"
	case status.Running:
		return "running; not ready"
	default:
		return "installed; service stopped"
	}
}

type platformInstallation struct {
	Name              string
	Service           string
	HelperPath        string
	ConfigurationPath string
	ReceiptPath       string
	ResolverPath      string
}

type installationReceipt struct {
	SchemaVersion     int       `json:"schemaVersion"`
	Platform          string    `json:"platform"`
	Service           string    `json:"service"`
	OwnerUID          int       `json:"ownerUid"`
	OwnerGID          int       `json:"ownerGid"`
	TargetSocket      string    `json:"targetSocket"`
	DNSTargetSocket   string    `json:"dnsTargetSocket,omitempty"`
	LoopbackAddresses []string  `json:"loopbackAddresses,omitempty"`
	HelperPath        string    `json:"helperPath"`
	ConfigurationPath string    `json:"configurationPath"`
	InstalledAt       time.Time `json:"installedAt"`
}

const installationReceiptSchema = 3

func Check(ctx context.Context) error {
	return checkAt(ctx, DefaultListenAddress, ControlOrigin)
}

// CheckSocket verifies the daemon's private ingress listener without using the
// privileged port-80 relay.
func CheckSocket(ctx context.Context, socketPath string) error {
	if !filepath.IsAbs(socketPath) {
		return errors.New("daemon ingress socket path must be absolute")
	}
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	return checkWithDial(ctx, ControlOrigin, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	})
}

func WaitUntilReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		httpErr := Check(ctx)
		dnsErr := CheckDNS(ctx)
		resolverErr := CheckResolver(ctx)
		if httpErr == nil && dnsErr == nil && resolverErr == nil {
			return nil
		}
		lastError = errors.Join(httpErr, dnsErr, resolverErr)
		if time.Now().After(deadline) {
			return fmt.Errorf("clean localhost ingress did not become ready: %w", lastError)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func CheckDNS(ctx context.Context) error {
	queryID := uint16(rand.Uint32())
	query, err := portlessdns.Query(networking.DNSZone, portlessdns.TypeA, queryID)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "udp", DefaultDNSAddress)
	if err != nil {
		return fmt.Errorf("connect to Portless DNS at %s: %w", DefaultDNSAddress, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write(query); err != nil {
		return err
	}
	response := make([]byte, portlessdns.MaxMessage)
	count, err := connection.Read(response)
	if err != nil {
		return fmt.Errorf("read Portless DNS response: %w", err)
	}
	address, rcode, err := portlessdns.ParseAResponse(response[:count], queryID)
	if err != nil {
		return err
	}
	if rcode != portlessdns.ResponseSuccess || address != portlessdns.HealthAddress {
		return fmt.Errorf("Portless DNS returned code %d and address %s", rcode, address)
	}
	return nil
}

// CheckResolver verifies the OS-level scoped resolver route used by normal
// applications. Directly reaching the DNS relay is insufficient if the host
// resolver never sends portless.test queries to it.
func CheckResolver(ctx context.Context) error {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, networking.DNSZone)
	if err != nil {
		return fmt.Errorf("resolve %s through the system resolver: %w", networking.DNSZone, err)
	}
	return validateResolverAddresses(addresses)
}

func validateResolverAddresses(addresses []net.IPAddr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("resolve %s through the system resolver: no addresses returned", networking.DNSZone)
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address.IP)
		if !ok || parsed.Unmap() != portlessdns.HealthAddress {
			return fmt.Errorf("resolve %s through the system resolver: unexpected address %s", networking.DNSZone, address.IP)
		}
	}
	return nil
}

func checkAt(ctx context.Context, address, origin string) error {
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	return checkWithDial(ctx, origin, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	})
}

func checkWithDial(ctx context.Context, origin string, dialContext func(context.Context, string, string) (net.Conn, error)) error {
	transport := &http.Transport{Proxy: nil, DialContext: dialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/health", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", origin, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", origin, response.Status)
	}
	var health struct {
		Ready      bool   `json:"ready"`
		APIVersion string `json:"apiVersion"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&health); err != nil {
		return fmt.Errorf("decode %s health response: %w", origin, err)
	}
	if !health.Ready || health.APIVersion == "" {
		return errors.New("portless ingress health response is incompatible")
	}
	return nil
}

func Install(ctx context.Context, request SetupRequest) error {
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return InstallPrivileged(ctx, request.Executable, request.TargetSocket, request.DNSTargetSocket, request.UID, request.GID)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("Portless setup requires sudo, but sudo was not found")
	}
	command := exec.CommandContext(ctx, sudo,
		request.Executable,
		"__install-ingress",
		"--socket", request.TargetSocket,
		"--dns-socket", request.DNSTargetSocket,
		"--uid", strconv.Itoa(request.UID),
		"--gid", strconv.Itoa(request.GID),
	)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("install clean localhost ingress: %w", err)
	}
	return nil
}

func InstallPrivileged(ctx context.Context, sourceExecutable, targetSocket, dnsTargetSocket string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal ingress installer must run as root")
	}
	request := SetupRequest{Executable: sourceExecutable, TargetSocket: targetSocket, DNSTargetSocket: dnsTargetSocket, UID: uid, GID: gid}
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	return installPlatform(ctx, request)
}

func Restart(ctx context.Context, request RestartRequest) error {
	if request.UID <= 0 {
		return errors.New("Portless relay restart requires a non-root requesting user ID")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	status, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed")
	}
	if err := validateOwnership(status, request.UID); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return RestartPrivileged(ctx, request.UID)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("Portless relay restart requires sudo, but sudo was not found")
	}
	command := exec.CommandContext(ctx, sudo,
		request.Executable,
		"__restart-ingress",
		"--uid", strconv.Itoa(request.UID),
	)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("restart clean localhost ingress: %w", err)
	}
	return nil
}

func RestartPrivileged(ctx context.Context, requestingUID int) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal ingress restarter must run as root")
	}
	status, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed")
	}
	if err := validateOwnership(status, requestingUID); err != nil {
		return err
	}
	return restartPlatform(ctx)
}

func Inspect(ctx context.Context) (InstallationStatus, error) {
	details := currentPlatformInstallation()
	status := InstallationStatus{
		Platform: details.Name, Service: details.Service, HelperPath: details.HelperPath,
		ConfigurationPath: details.ConfigurationPath, ReceiptPath: details.ReceiptPath,
		ResolverPath: details.ResolverPath, DNSListenAddress: DefaultDNSAddress,
	}
	helperPresent, err := pathExists(details.HelperPath)
	if err != nil {
		return status, fmt.Errorf("inspect ingress helper: %w", err)
	}
	configurationPresent, err := pathExists(details.ConfigurationPath)
	if err != nil {
		return status, fmt.Errorf("inspect ingress service configuration: %w", err)
	}
	receiptPresent, err := pathExists(details.ReceiptPath)
	if err != nil {
		return status, fmt.Errorf("inspect ingress installation receipt: %w", err)
	}
	resolverPresent, err := pathExists(details.ResolverPath)
	if err != nil {
		return status, fmt.Errorf("inspect Portless resolver configuration: %w", err)
	}
	status.HelperPresent = helperPresent
	status.ConfigurationPresent = configurationPresent
	status.ReceiptPresent = receiptPresent
	status.ResolverPresent = resolverPresent
	status.Installed = helperPresent || configurationPresent || receiptPresent || resolverPresent
	if receiptPresent {
		receipt, receiptErr := readInstallationReceipt(details)
		if receiptErr != nil {
			status.Problem = receiptErr.Error()
		} else {
			status.OwnerUID, status.OwnerGID = receipt.OwnerUID, receipt.OwnerGID
			status.TargetSocket, status.DNSTargetSocket = receipt.TargetSocket, receipt.DNSTargetSocket
			installedAt := receipt.InstalledAt
			status.InstalledAt = &installedAt
		}
	} else if configurationPresent {
		uid, gid, socket, dnsSocket, fallbackErr := platformConfigurationOwner(details.ConfigurationPath)
		if fallbackErr != nil {
			status.Problem = "installation receipt is missing and the service owner could not be determined: " + fallbackErr.Error()
		} else {
			status.OwnerUID, status.OwnerGID, status.TargetSocket, status.DNSTargetSocket = uid, gid, socket, dnsSocket
		}
	}
	running, runningErr := platformServiceRunning(ctx)
	if runningErr != nil {
		status.Problem = appendProblem(status.Problem, runningErr.Error())
	}
	status.Running = running
	status.Installed = status.Installed || running
	poolReady, poolDetail, poolErr := relayLoopbackPoolStatus()
	status.EndpointPoolReady = poolReady
	status.EndpointPoolDetail = poolDetail
	if poolErr != nil {
		status.Problem = appendProblem(status.Problem, "inspect TCP endpoint address pool: "+poolErr.Error())
	}
	if status.Installed {
		httpErr := Check(ctx)
		dnsErr := CheckDNS(ctx)
		resolverContext, cancelResolver := context.WithTimeout(ctx, 1500*time.Millisecond)
		resolverErr := CheckResolver(resolverContext)
		cancelResolver()
		if httpErr != nil {
			status.HealthError = httpErr.Error()
		} else {
			status.HTTPHealthy = true
		}
		if dnsErr == nil {
			status.DNSHealthy = true
		} else {
			status.DNSHealthError = dnsErr.Error()
		}
		if resolverErr == nil {
			status.ResolverHealthy = true
		} else {
			status.ResolverHealthError = resolverErr.Error()
		}
		status.Healthy = httpErr == nil && dnsErr == nil && resolverErr == nil && resolverPresent && poolReady && poolErr == nil
	}
	return status, nil
}

func Uninstall(ctx context.Context, request UninstallRequest) (bool, error) {
	if request.UID <= 0 && !request.Force {
		return false, errors.New("Portless ingress uninstall requires a non-root requesting user ID")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return false, err
	}
	status, err := Inspect(ctx)
	if err != nil {
		return false, err
	}
	if !status.Installed {
		return false, nil
	}
	if err := validateUninstallOwnership(status, request.UID, request.Force); err != nil {
		return false, err
	}
	if os.Geteuid() == 0 {
		return true, UninstallPrivileged(ctx, request.UID, request.Force)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return false, errors.New("Portless relay uninstall requires sudo, but sudo was not found")
	}
	arguments := []string{request.Executable, "__uninstall-ingress", "--uid", strconv.Itoa(request.UID)}
	if request.Force {
		arguments = append(arguments, "--force")
	}
	command := exec.CommandContext(ctx, sudo, arguments...)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return false, fmt.Errorf("uninstall clean localhost ingress: %w", err)
	}
	return true, nil
}

func UninstallPrivileged(ctx context.Context, requestingUID int, force bool) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal ingress uninstaller must run as root")
	}
	status, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return nil
	}
	if err := validateUninstallOwnership(status, requestingUID, force); err != nil {
		return err
	}
	removeLoopbackPool := false
	if receipt, receiptErr := readInstallationReceipt(currentPlatformInstallation()); receiptErr == nil {
		removeLoopbackPool = receipt.SchemaVersion >= 3
	}
	if err := uninstallPlatform(ctx, removeLoopbackPool); err != nil {
		return err
	}
	remaining, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if remaining.Installed {
		return errors.New("clean localhost ingress removal was incomplete; run `portless relay status` for details")
	}
	return nil
}

func validateOwnership(status InstallationStatus, requestingUID int) error {
	if requestingUID <= 0 {
		return errors.New("the relay operation requires a non-root requesting user ID")
	}
	if status.OwnerUID <= 0 {
		return errors.New("the clean-URL relay owner could not be determined; inspect `portless relay status`")
	}
	if status.OwnerUID != requestingUID {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d", status.OwnerUID)
	}
	return nil
}

func ValidateOwnership(status InstallationStatus, requestingUID int) error {
	return validateOwnership(status, requestingUID)
}

func validateUninstallOwnership(status InstallationStatus, requestingUID int, force bool) error {
	if force {
		return nil
	}
	if err := validateOwnership(status, requestingUID); err != nil {
		return fmt.Errorf("%w; repeat with --force to remove the installation", err)
	}
	return nil
}

func ValidateUninstallOwnership(status InstallationStatus, requestingUID int, force bool) error {
	return validateUninstallOwnership(status, requestingUID, force)
}

func writeInstallationReceipt(request SetupRequest) error {
	details := currentPlatformInstallation()
	receipt := installationReceipt{
		SchemaVersion: installationReceiptSchema, Platform: details.Name, Service: details.Service,
		OwnerUID: request.UID, OwnerGID: request.GID, TargetSocket: request.TargetSocket, DNSTargetSocket: request.DNSTargetSocket,
		LoopbackAddresses: managedRelayLoopbackAddresses(),
		HelperPath:        details.HelperPath, ConfigurationPath: details.ConfigurationPath, InstalledAt: time.Now().UTC(),
	}
	content, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := writeRootFileAtomically(details.ReceiptPath, content, 0o644); err != nil {
		return fmt.Errorf("write ingress installation receipt: %w", err)
	}
	return nil
}

func readInstallationReceipt(details platformInstallation) (installationReceipt, error) {
	file, err := os.Open(details.ReceiptPath)
	if err != nil {
		return installationReceipt{}, err
	}
	defer file.Close()
	var receipt installationReceipt
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return installationReceipt{}, fmt.Errorf("read ingress installation receipt: %w", err)
	}
	if receipt.SchemaVersion < 1 || receipt.SchemaVersion > installationReceiptSchema {
		return installationReceipt{}, fmt.Errorf("unsupported ingress installation receipt schema %d", receipt.SchemaVersion)
	}
	if receipt.Platform != details.Name || receipt.Service != details.Service || receipt.HelperPath != details.HelperPath || receipt.ConfigurationPath != details.ConfigurationPath {
		return installationReceipt{}, errors.New("ingress installation receipt does not match this platform")
	}
	if receipt.OwnerUID <= 0 || receipt.OwnerGID <= 0 || !filepath.IsAbs(receipt.TargetSocket) || filepath.Base(filepath.Clean(receipt.TargetSocket)) != "ingress.sock" {
		return installationReceipt{}, errors.New("ingress installation receipt contains invalid ownership or socket information")
	}
	if receipt.SchemaVersion >= 2 && (!filepath.IsAbs(receipt.DNSTargetSocket) || filepath.Base(filepath.Clean(receipt.DNSTargetSocket)) != "dns.sock") {
		return installationReceipt{}, errors.New("ingress installation receipt contains invalid DNS socket information")
	}
	if receipt.SchemaVersion >= 3 {
		expected := managedRelayLoopbackAddresses()
		if len(receipt.LoopbackAddresses) != len(expected) {
			return installationReceipt{}, errors.New("ingress installation receipt contains an invalid loopback address pool")
		}
		for index := range expected {
			if receipt.LoopbackAddresses[index] != expected[index] {
				return installationReceipt{}, errors.New("ingress installation receipt contains an invalid loopback address pool")
			}
		}
	}
	return receipt, nil
}

func managedRelayLoopbackAddresses() []string {
	dnsHost, _, _ := net.SplitHostPort(DefaultDNSAddress)
	return append([]string{dnsHost}, networking.EndpointLoopbackAddresses()...)
}

func pathExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func removeExactFile(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func removeDirectoryIfEmpty(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func appendProblem(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	return current + "; " + next
}

func relayArgumentValues(arguments []string) (int, int, string, string, error) {
	values := map[string]string{}
	for index := 0; index+1 < len(arguments); index++ {
		switch arguments[index] {
		case "--socket", "--dns-socket", "--uid", "--gid":
			values[arguments[index]] = arguments[index+1]
			index++
		}
	}
	uid, uidErr := strconv.Atoi(values["--uid"])
	gid, gidErr := strconv.Atoi(values["--gid"])
	socket := values["--socket"]
	dnsSocket := values["--dns-socket"]
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 || !filepath.IsAbs(socket) || filepath.Base(filepath.Clean(socket)) != "ingress.sock" {
		return 0, 0, "", "", errors.New("service configuration does not contain valid relay ownership arguments")
	}
	if !filepath.IsAbs(dnsSocket) || filepath.Base(filepath.Clean(dnsSocket)) != "dns.sock" {
		return 0, 0, "", "", errors.New("service configuration does not contain a valid DNS relay target")
	}
	return uid, gid, socket, dnsSocket, nil
}

func validateSetupRequest(request SetupRequest) error {
	if request.UID <= 0 || request.GID <= 0 {
		return errors.New("clean localhost ingress must run as a non-root user and group")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	if !filepath.IsAbs(request.TargetSocket) {
		return errors.New("ingress target socket must be an absolute path")
	}
	cleanSocket := filepath.Clean(request.TargetSocket)
	if cleanSocket == string(filepath.Separator) || filepath.Base(cleanSocket) != "ingress.sock" {
		return errors.New("ingress target must be a private ingress.sock path")
	}
	if strings.ContainsRune(request.TargetSocket, '\x00') {
		return errors.New("ingress target socket contains an invalid character")
	}
	if !filepath.IsAbs(request.DNSTargetSocket) || filepath.Base(filepath.Clean(request.DNSTargetSocket)) != "dns.sock" || strings.ContainsRune(request.DNSTargetSocket, '\x00') {
		return errors.New("DNS target must be a private dns.sock path")
	}
	return nil
}

func validateExecutable(executable string) error {
	if !filepath.IsAbs(executable) {
		return errors.New("Portless executable path must be absolute")
	}
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("inspect Portless executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("Portless executable must be a regular executable file")
	}
	return nil
}

func copyExecutableAtomically(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Portless executable source is not a regular file")
	}
	if info.Size() > 512<<20 {
		return errors.New("Portless executable is unexpectedly larger than 512 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".portless-ingress-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o755); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chown(0, 0); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func writeRootFileAtomically(destination string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".portless-config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chown(0, 0); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func runCommand(ctx context.Context, executable string, arguments ...string) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s failed: %w", filepath.Base(executable), err)
	}
	return fmt.Errorf("%s failed: %w: %s", filepath.Base(executable), err, detail)
}

func portAvailable() error {
	return addressAvailable(DefaultListenAddress, "port 80 on 127.0.0.1")
}

func addressAvailable(address, label string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("%s is already in use: %w", label, err)
	}
	return listener.Close()
}

func dnsAddressAvailable(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("TCP DNS address %s is already in use: %w", address, err)
	}
	defer listener.Close()
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		return fmt.Errorf("UDP DNS address %s is already in use: %w", address, err)
	}
	return packet.Close()
}

func waitForPortAvailable(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		if err := portAvailable(); err == nil {
			return nil
		} else {
			lastError = err
		}
		if time.Now().After(deadline) {
			return lastError
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForRelayAddressesAvailable(ctx context.Context, timeout time.Duration) error {
	if err := waitForPortAvailable(ctx, timeout); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		if err := dnsAddressAvailable(DefaultDNSAddress); err == nil {
			return nil
		} else {
			lastError = err
		}
		if time.Now().After(deadline) {
			return lastError
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
