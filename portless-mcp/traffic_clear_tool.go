package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type clearTrafficInput struct {
	environmentInput
	cleanupInput
	ThroughSequence *int64 `json:"throughSequence,omitempty"`
}
type trafficClearView struct {
	Preview *contract.TrafficClearPreview  `json:"preview,omitempty"`
	Cleared *contract.TrafficClearResponse `json:"cleared,omitempty"`
	Applied bool                           `json:"applied"`
}

func (r *runtime) registerTrafficClearTool(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_clear_traffic", "Preview live traffic removal through a fixed sequence watermark. Apply requires confirm:true, throughSequence, and the reviewed environment/daemon identity. Newer exchanges, active requests, durable recordings, and admitted replay receipts remain.", true), r.clearTraffic)
}

func (r *runtime) clearTraffic(ctx context.Context, _ *mcp.CallToolRequest, input clearTrafficInput) (*mcp.CallToolResult, scopedResult[trafficClearView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (trafficClearView, error) {
		var result trafficClearView
		apply, err := input.cleanupInput.apply()
		if err != nil {
			return result, err
		}
		if !apply {
			preview, err := selected.client.PreviewTrafficClear(ctx, selected.project, selected.environment)
			if err != nil {
				return result, err
			}
			result.Preview = &preview
			return result, nil
		}
		if input.ThroughSequence == nil || *input.ThroughSequence < 0 {
			return result, codedError{code: "PREVIEW_REQUIRED", message: "apply requires the throughSequence returned by the preview"}
		}
		cleared, err := selected.client.WithResourceVersion(*input.Expected).ClearTrafficThrough(ctx, selected.project, selected.environment, *input.ThroughSequence)
		if err != nil {
			return result, err
		}
		result.Applied = true
		result.Cleared = &cleared
		return result, nil
	})
}
