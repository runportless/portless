package command

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
)

// BrowserURL creates a single-use browser claim and returns its authenticated
// URL, with next used as the post-claim application path.
func (c *Context) BrowserURL(ctx context.Context, client *apiclient.Client, next string) (string, error) {
	result, err := client.CreateBrowserClaim(ctx, next)
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

// RequireIngress verifies that clean HTTP and TCP endpoints are routed to the
// current user's Portless installation.
func (c *Context) RequireIngress(ctx context.Context) error {
	if e2ePrivateIngress {
		if err := c.Local.CheckRelaySocket(ctx, c.Paths.IngressSocket); err != nil {
			return fmt.Errorf("verify private Portless ingress: %w", err)
		}
		return nil
	}
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return fmt.Errorf("inspect local endpoint networking: %w", err)
	}
	uid, _ := c.Local.UserIDs()
	ready := status.Installed && status.Healthy && status.OwnerUID == uid && status.TargetSocket == c.Paths.IngressSocket && status.DNSTargetSocket == c.Paths.DNSSocket
	if ready {
		return nil
	}
	detail := "HTTP ingress, endpoint DNS, or the loopback endpoint pool is incomplete"
	switch {
	case !status.Installed:
		detail = "the Portless relay is not installed"
	case status.OwnerUID != uid:
		detail = fmt.Sprintf("the Portless relay belongs to user ID %d instead of %d", status.OwnerUID, uid)
	case status.TargetSocket != c.Paths.IngressSocket:
		detail = fmt.Sprintf("the HTTP relay targets %s instead of %s", EmptyAs(status.TargetSocket, "an unknown socket"), c.Paths.IngressSocket)
	case status.DNSTargetSocket != c.Paths.DNSSocket:
		detail = fmt.Sprintf("the DNS relay targets %s instead of %s", EmptyAs(status.DNSTargetSocket, "an unknown socket"), c.Paths.DNSSocket)
	case !status.EndpointPoolReady:
		detail = EmptyAs(status.EndpointPoolDetail, "the loopback endpoint pool is not ready")
	case !status.ResolverPresent:
		detail = "the scoped endpoint resolver configuration is missing"
	case !status.ResolverHealthy:
		detail = EmptyAs(status.ResolverHealthError, "the system resolver cannot resolve Portless endpoint names")
	case !status.DNSHealthy:
		detail = EmptyAs(status.DNSHealthError, "the authoritative DNS relay is not healthy")
	case !status.HTTPHealthy:
		detail = EmptyAs(status.HealthError, "the clean HTTP ingress is not healthy")
	case status.Problem != "":
		detail = status.Problem
	}
	return fmt.Errorf("clean local endpoints are not configured for this Portless installation; run `portless relay install` or `portless setup`, then retry: %s", detail)
}

// LaunchBrowser asks the operating system to open targetURL in the default
// browser without waiting for that browser to exit.
func LaunchBrowser(targetURL string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", targetURL)
	case "linux":
		command = exec.Command("xdg-open", targetURL)
	default:
		return errors.New("automatic browser opening is unsupported on this platform")
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
