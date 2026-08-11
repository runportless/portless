package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
)

type Paths struct {
	Root         string
	Database     string
	Control      string
	DaemonLog    string
	Token        string
	OwnershipKey string
	Lock         string
	Ingress      string
	Logs         string
	Temporary    string
}

func ResolvePaths(override string) (Paths, error) {
	root := override
	if root == "" {
		root = os.Getenv("PORTLESS_HOME")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		root = filepath.Join(home, ".portless")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, err
	}
	if root == string(filepath.Separator) || root == "" {
		return Paths{}, errors.New("refusing to use a broad Portless data directory")
	}
	return Paths{
		Root: root, Database: filepath.Join(root, "state.db"), Control: filepath.Join(root, "control.json"),
		DaemonLog: filepath.Join(root, "daemon.log"), Token: filepath.Join(root, "install.key"),
		OwnershipKey: filepath.Join(root, "ownership.key"), Lock: filepath.Join(root, "daemon.lock"),
		Ingress: filepath.Join(root, "ingress.sock"), Logs: filepath.Join(root, "logs"), Temporary: filepath.Join(root, "tmp"),
	}, nil
}
