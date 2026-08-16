package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
)

func listenIngress(path string) (net.Listener, error) {
	return listenPrivateSocket(path, "ingress")
}

func listenPrivateSocket(path, purpose string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket %s path %s", purpose, path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale %s socket: %w", purpose, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect %s socket: %w", purpose, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on private %s socket: %w", purpose, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		removeIngressSocket(path)
		return nil, fmt.Errorf("protect private %s socket: %w", purpose, err)
	}
	return listener, nil
}

func removeIngressSocket(path string) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
}

func listenControl(preferred int) (net.Listener, error) {
	if preferred <= 0 {
		preferred = 7331
	}
	for port := preferred; port < preferred+100; port++ {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return listener, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}
