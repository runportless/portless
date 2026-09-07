package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"time"
)

type deleteRecordingInput struct {
	recordingInput
	cleanupInput
}
type deleteFaultInput struct {
	faultInput
	cleanupInput
}
type enableFaultInput struct {
	faultInput
	ExpectedRevision int64 `json:"expectedRevision"`
}
type recordingDeletionView struct {
	Preview   *contract.RecordingDeletionPreview `json:"preview,omitempty"`
	Recording string                             `json:"recording"`
	Applied   bool                               `json:"applied"`
}
type faultDeletionView struct {
	Preview *contract.FaultDeletionPreview `json:"preview,omitempty"`
	Fault   string                         `json:"fault"`
	Applied bool                           `json:"applied"`
}

func (r *runtime) registerArtifactMutationTools(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_delete_recording", "Preview removal of a stopped recording and its retained events. Apply requires confirm:true and the previewed identity. Live traffic, saved mocks, source files, and volumes remain.", true), r.deleteRecording)
	registerTool(r, server, mutationTool("portless_delete_fault", "Preview removal of one named fault rule. Apply requires confirm:true and its unchanged creation identity and revision. Existing traffic and timeline history remain.", true), r.deleteFault)
	registerTool(r, server, mutationTool("portless_enable_fault", "Enable an existing exact-edge fault only while its original finite expiry remains within one hour. Never extends expiry; validates identity and revision atomically.", false), r.enableFault)
}

func (r *runtime) deleteRecording(ctx context.Context, _ *mcp.CallToolRequest, input deleteRecordingInput) (*mcp.CallToolResult, scopedResult[recordingDeletionView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (recordingDeletionView, error) {
		result := recordingDeletionView{Recording: input.Recording}
		if err := validateArtifactName(input.Recording, "recording"); err != nil {
			return result, err
		}
		apply, err := input.cleanupInput.apply()
		if err != nil {
			return result, err
		}
		if !apply {
			preview, err := selected.client.PreviewRecordingDeletion(ctx, selected.project, selected.environment, input.Recording)
			if err != nil {
				return result, err
			}
			result.Preview = &preview
			return result, nil
		}
		err = selected.client.WithResourceVersion(*input.Expected).DeleteRecording(ctx, selected.project, selected.environment, input.Recording)
		result.Applied = err == nil
		return result, err
	})
}

func (r *runtime) deleteFault(ctx context.Context, _ *mcp.CallToolRequest, input deleteFaultInput) (*mcp.CallToolResult, scopedResult[faultDeletionView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (faultDeletionView, error) {
		result := faultDeletionView{Fault: input.Fault}
		if err := validateArtifactName(input.Fault, "fault"); err != nil {
			return result, err
		}
		apply, err := input.cleanupInput.apply()
		if err != nil {
			return result, err
		}
		if !apply {
			preview, err := selected.client.PreviewFaultDeletion(ctx, selected.project, selected.environment, input.Fault)
			if err != nil {
				return result, err
			}
			result.Preview = &preview
			return result, nil
		}
		err = selected.client.WithResourceVersion(*input.Expected).DeleteFault(ctx, selected.project, selected.environment, input.Fault)
		result.Applied = err == nil
		return result, err
	})
}

func (r *runtime) enableFault(ctx context.Context, _ *mcp.CallToolRequest, input enableFaultInput) (*mcp.CallToolResult, scopedResult[contract.FaultRule], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (contract.FaultRule, error) {
		if err := validateArtifactName(input.Fault, "fault"); err != nil {
			return contract.FaultRule{}, err
		}
		preview, err := selected.client.PreviewFaultDeletion(ctx, selected.project, selected.environment, input.Fault)
		if err != nil {
			return contract.FaultRule{}, err
		}
		fault := preview.Fault
		if input.ExpectedRevision < 1 || input.ExpectedRevision != fault.Revision {
			return contract.FaultRule{}, codedError{code: "RESOURCE_CHANGED", message: "expectedRevision must match the current fault"}
		}
		if fault.Source == "" || fault.Target == "" || fault.ExpiresAt == nil || !fault.ExpiresAt.After(time.Now()) || fault.ExpiresAt.After(time.Now().Add(time.Hour)) {
			return contract.FaultRule{}, codedError{code: "FINITE_FAULT_REQUIRED", message: "only exact-edge faults with an existing unexpired expiry within one hour can be enabled"}
		}
		return selected.client.WithResourceVersion(preview.Expected).SetFaultEnabled(ctx, selected.project, selected.environment, input.Fault, true)
	})
}
