package installation

import (
	"errors"
	"os"
	"path/filepath"
)

type Layout struct {
	Root          string
	Database      string
	Control       string
	DaemonLog     string
	AuthToken     string
	OwnershipKey  string
	StartupLock   string
	InstanceLock  string
	IngressSocket string
	DNSSocket     string
	Logs          string
	Temporary     string
}

func ResolveLayout(override string) (Layout, error) {
	root := override
	if root == "" {
		root = os.Getenv("PORTLESS_HOME")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Layout{}, err
		}
		root = filepath.Join(home, ".portless")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, err
	}
	if root == string(filepath.Separator) || root == "" {
		return Layout{}, errors.New("refusing to use a broad Portless data directory")
	}
	return Layout{
		Root: root, Database: filepath.Join(root, "portless.db"), Control: filepath.Join(root, "control.json"),
		DaemonLog: filepath.Join(root, "daemon.log"), AuthToken: filepath.Join(root, "install.key"),
		OwnershipKey: filepath.Join(root, "ownership.key"), StartupLock: filepath.Join(root, "daemon.lock"),
		InstanceLock:  filepath.Join(root, "daemon.instance.lock"),
		IngressSocket: filepath.Join(root, "ingress.sock"), DNSSocket: filepath.Join(root, "dns.sock"),
		Logs: filepath.Join(root, "logs"), Temporary: filepath.Join(root, "tmp"),
	}, nil
}
