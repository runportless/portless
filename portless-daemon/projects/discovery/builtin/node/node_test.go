package node

import (
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestNodeDebugCapabilityPrefersExplicitDebugScript(t *testing.T) {
	manifest := packageJSON{Scripts: map[string]string{
		"start:dev":   "nest start --watch",
		"start:debug": "nest start --watch --debug 0.0.0.0:9229",
	}}
	capability := nodeDebugCapability(manifest, "npm", "start:dev")
	if capability == nil || capability.Adapter != model.DebugNodeInspector || capability.Launcher != model.DebugNestCLI {
		t.Fatalf("capability = %#v", capability)
	}
	want := []string{"npm", "exec", "--", "nest", "start", "--watch"}
	if !reflect.DeepEqual(capability.Command, want) {
		t.Fatalf("command = %#v, want %#v", capability.Command, want)
	}
}

func TestNodeDebugCapabilitySupportsDirectNodeAndRejectsShellScripts(t *testing.T) {
	direct := nodeDebugCapability(packageJSON{Scripts: map[string]string{"dev": `node "dist/main.js"`}}, "pnpm", "dev")
	if direct == nil || direct.Launcher != model.DebugNodeDirect || !reflect.DeepEqual(direct.Command, []string{"node", "dist/main.js"}) {
		t.Fatalf("direct capability = %#v", direct)
	}
	unsafe := nodeDebugCapability(packageJSON{Scripts: map[string]string{"dev": "node server.js | tee server.log"}}, "npm", "dev")
	if unsafe != nil {
		t.Fatalf("unsafe shell command was accepted: %#v", unsafe)
	}
}
