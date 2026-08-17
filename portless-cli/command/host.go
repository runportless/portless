package command

import (
	"os"
	"path/filepath"
	"strconv"
)

// ResolvedExecutable returns the current executable path after resolving any
// symbolic links.
func ResolvedExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(executable)
}

// RequestingUserIDs returns the invoking user even when Portless was started
// through sudo for a privileged relay operation.
func RequestingUserIDs() (int, int) {
	uid, gid := os.Getuid(), os.Getgid()
	if os.Geteuid() != 0 {
		return uid, gid
	}
	if sudoUID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && sudoUID > 0 {
		uid = sudoUID
	}
	if sudoGID, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil && sudoGID > 0 {
		gid = sudoGID
	}
	return uid, gid
}
