package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"strings"
)

type traceListInput struct {
	Environment       string `json:"environment"`
	Service           string `json:"service,omitempty"`
	Edge              string `json:"edge,omitempty"`
	IncludeBackground bool   `json:"includeBackground,omitempty"`
	Limit             int    `json:"limit,omitempty" jsonschema:"default 50, maximum 200"`
}

type traceInput struct {
	Environment      string `json:"environment"`
	Number           int64  `json:"number"`
	ExpectedRevision uint64 `json:"expectedRevision,omitempty"`
	Offset           int    `json:"offset,omitempty"`
	Limit            int    `json:"limit,omitempty" jsonschema:"default 50, maximum 200"`
}

type traceSpanView struct {
	Exchange         trafficSummaryView `json:"exchange"`
	ParentSequence   int64              `json:"parentSequence,omitempty"`
	Depth            int                `json:"depth"`
	StartOffsetMS    int64              `json:"startOffsetMs"`
	Correlation      string             `json:"correlation"`
	TransactionGroup int                `json:"transactionGroup,omitempty"`
}

type traceDetailView struct {
	Trace      contract.TrafficTrace `json:"trace"`
	Spans      []traceSpanView       `json:"spans"`
	NextOffset int                   `json:"nextOffset,omitempty"`
}

func (r *runtime) registerTraceTools(server *mcp.Server) {
	registerTool(r, server, readTool("portless_list_traces", "List safe trace summaries and projection watermarks. Correlation is computed by the daemon; payloads and query values are excluded."), r.listTraces)
	registerTool(r, server, readTool("portless_get_trace", "Inspect a revision-bound page of safe trace spans, preserving parent relationships, correlation, and transaction grouping."), r.getTrace)
}

func safeTrafficPath(value string) string {
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	value, _ = truncateUTF8(value, 4096)
	return value
}

func traceSummary(value contract.TrafficTrace) contract.TrafficTrace {
	value.Spans = nil
	value.RequestTarget = safeTrafficPath(value.RequestTarget)
	return value
}

func (r *runtime) listTraces(ctx context.Context, _ *mcp.CallToolRequest, input traceListInput) (*mcp.CallToolResult, scopedResult[contract.TrafficTraceList], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (contract.TrafficTraceList, error) {
		var result contract.TrafficTraceList
		limit, err := bounded(input.Limit, 50, 200, "limit")
		if err != nil {
			return result, err
		}
		if input.Service != "" {
			if err := validateConnectionSource(input.Service); err != nil {
				return result, err
			}
		}
		if err := validateEdge(input.Edge); err != nil {
			return result, err
		}
		result, err = selected.client.TrafficTraces(ctx, selected.project, selected.environment, contract.TrafficTraceQuery{Service: input.Service, Edge: input.Edge, IncludeBackground: input.IncludeBackground, Limit: limit})
		for i := range result.Traces {
			result.Traces[i] = traceSummary(result.Traces[i])
		}
		return result, err
	})
}

func (r *runtime) getTrace(ctx context.Context, _ *mcp.CallToolRequest, input traceInput) (*mcp.CallToolResult, scopedResult[traceDetailView], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (traceDetailView, error) {
		var result traceDetailView
		limit, err := bounded(input.Limit, 50, 200, "limit")
		if err != nil {
			return result, err
		}
		if input.Number < 1 || input.Offset < 0 || (input.Offset > 0 && input.ExpectedRevision == 0) {
			return result, codedError{code: "INVALID_ARGUMENT", message: "number must be positive; continuation requires a non-negative offset and expectedRevision"}
		}
		page, err := selected.client.TrafficTracePage(ctx, selected.project, selected.environment, input.Number, contract.TrafficTracePageQuery{Offset: input.Offset, Limit: limit, ExpectedRevision: input.ExpectedRevision})
		if err != nil {
			return result, err
		}
		result = traceDetailView{Trace: traceSummary(page.Trace), Spans: []traceSpanView{}, NextOffset: page.NextOffset}
		for _, span := range page.Trace.Spans {
			result.Spans = append(result.Spans, traceSpanView{Exchange: trafficSummaryResult(span.Exchange), ParentSequence: span.ParentSequence, Depth: span.Depth, StartOffsetMS: span.StartOffsetMS, Correlation: string(span.Correlation), TransactionGroup: span.TransactionGroup})
		}
		for len(result.Spans) > 1 && r.checkOutput(scopedResult[traceDetailView]{Result: result}) != nil {
			result.Spans = result.Spans[:len(result.Spans)/2]
			result.NextOffset = input.Offset + len(result.Spans)
		}
		return result, nil
	})
}
