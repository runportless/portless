package portlessmcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:generate cp tool-inventory.json ../portless-web/src/features/mcp/tool-inventory.generated.json
//go:embed tool-inventory.json
var inventoryJSON []byte

type toolMetadata struct {
	Name        string   `json:"name"`
	Requires    []string `json:"requires"`
	ReadOnly    bool     `json:"readOnly"`
	Destructive bool     `json:"destructive"`
	Idempotent  bool     `json:"idempotent"`
	OpenWorld   bool     `json:"openWorld"`
}

var toolInventory = func() map[string]toolMetadata {
	var items []toolMetadata
	if err := json.Unmarshal(inventoryJSON, &items); err != nil {
		panic(err)
	}
	result := make(map[string]toolMetadata, len(items))
	for _, item := range items {
		if _, duplicate := result[item.Name]; duplicate {
			panic("duplicate MCP tool " + item.Name)
		}
		result[item.Name] = item
	}
	return result
}()

func (r *runtime) hasCapabilities(required []string) bool {
	for _, name := range required {
		enabled := false
		switch name {
		case "lifecycle":
			enabled = r.config.AllowLifecycle
		case "trafficControl":
			enabled = r.config.AllowTrafficControl
		case "sensitiveTraffic":
			enabled = r.config.AllowSensitiveTraffic
		case "replay":
			enabled = r.config.AllowReplay
		case "configuration":
			enabled = r.config.AllowConfiguration
		default:
			panic("unknown MCP capability " + name)
		}
		if !enabled {
			return false
		}
	}
	return true
}
func registerTool[Input, Output any](r *runtime, server *mcp.Server, tool *mcp.Tool, handler func(context.Context, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, Output, error)) {
	metadata, exists := toolInventory[tool.Name]
	if !exists {
		panic(fmt.Sprintf("MCP tool %q missing inventory metadata", tool.Name))
	}
	if !r.hasCapabilities(metadata.Requires) {
		return
	}
	tool.Annotations = &mcp.ToolAnnotations{Title: humanTitle(tool.Name), ReadOnlyHint: metadata.ReadOnly, DestructiveHint: &metadata.Destructive, IdempotentHint: metadata.Idempotent, OpenWorldHint: &metadata.OpenWorld}
	mcp.AddTool(server, tool, handler)
}
