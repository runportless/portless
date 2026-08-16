package daemon

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/portless-run/portless/internal/installation"
)

func watchExecutable(ctx context.Context, executable, currentBuildID string, canHandoff func(context.Context) (bool, []string), replacement chan<- struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	reportedBuild := ""
	lastInfo, _ := os.Stat(executable)
	pendingBuild := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		currentInfo, statErr := os.Stat(executable)
		if statErr != nil {
			continue
		}
		if pendingBuild == "" && sameExecutableFile(lastInfo, currentInfo) {
			continue
		}
		if !sameExecutableFile(lastInfo, currentInfo) {
			observedBuild, err := installation.BuildIDForPath(executable)
			if err != nil {
				continue
			}
			lastInfo = currentInfo
			if observedBuild == currentBuildID {
				pendingBuild = ""
				reportedBuild = ""
				continue
			}
			pendingBuild = observedBuild
		}
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ready, problems := canHandoff(probeCtx)
		cancel()
		if !ready {
			if reportedBuild != pendingBuild {
				slog.Warn("Portless executable changed but runtime handoff is unsafe", "problems", problems)
				reportedBuild = pendingBuild
			}
			continue
		}
		select {
		case replacement <- struct{}{}:
		case <-ctx.Done():
		}
		return
	}
}

func sameExecutableFile(left, right os.FileInfo) bool {
	if left == nil || right == nil {
		return false
	}
	return os.SameFile(left, right) && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
