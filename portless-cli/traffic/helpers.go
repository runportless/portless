package traffic

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/model"
)

func matchesTrafficOptions(event model.TrafficEvent, options trafficOptions) bool {
	if options.protocol == "http" && event.Protocol != model.ProtocolHTTP {
		return false
	}
	if options.protocol == "tcp" && event.Protocol == model.ProtocolHTTP {
		return false
	}
	if options.service != "" && event.Source != options.service && event.Target != options.service {
		return false
	}
	if options.edge != "" {
		source, target, _ := command.ParseEdge(options.edge)
		return event.Source == source && event.Target == target
	}
	return true
}

func printHeaderMap(writer io.Writer, title string, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintln(writer, "\n"+title+":")
	for _, key := range keys {
		fmt.Fprintf(writer, "  %-24s %s\n", key+":", headers[key])
	}
}

func writePrivateFile(path string, content []byte, force bool) error {
	if path == "" || path == "-" {
		return errors.New("an output path is required")
	}
	if _, err := os.Lstat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite it", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".portless-export-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
