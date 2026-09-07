package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type recordingExportInput struct {
	recordingInput
	Cursor   string `json:"cursor,omitempty"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"decoded export bytes per chunk; default and maximum 131072"`
}

func (r *runtime) registerRecordingExportTool(server *mcp.Server) {
	registerTool(r, server, readTool("portless_export_recording", "Read lossless base64 chunks of a complete schema-4 recording snapshot, including payloads captured by the daemon. Decode and concatenate data in order until complete:true. A cursor never broadens environment scope; deletion, replacement, or daemon restart invalidates continuation."), r.exportRecording)
}

func (r *runtime) exportRecording(ctx context.Context, _ *mcp.CallToolRequest, input recordingExportInput) (*mcp.CallToolResult, scopedResult[contract.RecordingExportChunk], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (contract.RecordingExportChunk, error) {
		var result contract.RecordingExportChunk
		if err := requireSensitive(r, true); err != nil {
			return result, err
		}
		if err := validateArtifactName(input.Recording, "recording"); err != nil {
			return result, err
		}
		maxBytes, err := bounded(input.MaxBytes, 128<<10, 128<<10, "maxBytes")
		if err != nil {
			return result, err
		}
		if len(input.Cursor) > 4096 {
			return result, codedError{code: "INVALID_ARGUMENT", message: "export cursor exceeds its size limit"}
		}
		return selected.client.RecordingExportChunk(ctx, selected.project, selected.environment, input.Recording, contract.RecordingExportChunkQuery{Cursor: input.Cursor, MaxBytes: maxBytes})
	})
}
