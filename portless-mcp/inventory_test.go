package portlessmcp

import (
	"bytes"
	"os"
	"testing"
)

func TestCanonicalInventoryMatchesEveryCapabilityCombination(t *testing.T) {
	generated, err := os.ReadFile("../portless-web/src/features/mcp/tool-inventory.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, inventoryJSON) {
		t.Fatal("web tool inventory drifted; run go generate ./portless-mcp")
	}
	for mask := 0; mask < 32; mask++ {
		config := Config{Project: "shop", AllowLifecycle: mask&1 != 0, AllowTrafficControl: mask&2 != 0, AllowSensitiveTraffic: mask&4 != 0, AllowReplay: mask&8 != 0, AllowConfiguration: mask&16 != 0}
		if config.AllowReplay && !config.AllowSensitiveTraffic {
			continue
		}
		session, closeSession := connectTestServer(t, config, testConnector{})
		listed, err := session.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := &runtime{config: config}
		expected := map[string]toolMetadata{}
		for name, tool := range toolInventory {
			if runtime.hasCapabilities(tool.Requires) {
				expected[name] = tool
			}
		}
		if len(listed.Tools) != len(expected) {
			t.Fatalf("mask %d: got %d tools, expected %d", mask, len(listed.Tools), len(expected))
		}
		for _, tool := range listed.Tools {
			meta, ok := expected[tool.Name]
			if !ok {
				t.Fatalf("unauthorized tool %s", tool.Name)
			}
			a := tool.Annotations
			if a == nil || a.ReadOnlyHint != meta.ReadOnly || a.IdempotentHint != meta.Idempotent || a.DestructiveHint == nil || *a.DestructiveHint != meta.Destructive || a.OpenWorldHint == nil || *a.OpenWorldHint != meta.OpenWorld {
				t.Fatalf("incorrect annotations for %s", tool.Name)
			}
		}
		closeSession()
	}
	if len(toolInventory) != 64 {
		t.Fatalf("release inventory=%d", len(toolInventory))
	}
}
