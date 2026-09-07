package portlessmcp

import "github.com/runportless/portless/portless-daemon/api/contract"

func (r *runtime) replayResult(value contract.TrafficReplayWorkspace, offset int) replayView {
	result := replayView{Workspace: value}
	if offset < 0 {
		offset = 0
	}
	capText := func(value string, limit int) string {
		text, cut := truncateUTF8(value, limit)
		result.DisplayTruncated = result.DisplayTruncated || cut
		return text
	}
	capDraft := func(draft *contract.TrafficReplayDraft) *contract.TrafficReplayDraft {
		if draft == nil {
			return nil
		}
		value := *draft
		value.Body = capText(value.Body, 8<<10)
		value.RequestTarget = capText(value.RequestTarget, 4<<10)
		var cut bool
		value.Headers, cut = capHeaders(value.Headers, 4<<10)
		result.DisplayTruncated = result.DisplayTruncated || cut
		return &value
	}
	capExchange := func(exchange *contract.TrafficExchange) *contract.TrafficExchange {
		if exchange == nil {
			return nil
		}
		value := *exchange
		value.RequestBody = capText(value.RequestBody, 8<<10)
		value.ResponseBody = capText(value.ResponseBody, 8<<10)
		var cut bool
		value.RequestHeaders, cut = capHeaders(value.RequestHeaders, 4<<10)
		result.DisplayTruncated = result.DisplayTruncated || cut
		value.ResponseHeaders, cut = capHeaders(value.ResponseHeaders, 4<<10)
		result.DisplayTruncated = result.DisplayTruncated || cut
		return &value
	}
	result.Workspace.Draft = capDraft(value.Draft)
	result.Workspace.Baseline = capExchange(value.Baseline)
	if value.Result != nil {
		latest := *value.Result
		latest.Request = *capDraft(&latest.Request)
		latest.Exchange = capExchange(latest.Exchange)
		for _, section := range []*contract.TrafficComparisonSection{&latest.Comparison.Headers, &latest.Comparison.Body} {
			changes := section.Changes
			start := min(offset, len(changes))
			end := min(start+20, len(changes))
			section.Changes = append([]contract.TrafficReplayChange{}, changes[start:end]...)
			if end < len(changes) {
				result.NextDifferenceOffset = end
				result.DisplayTruncated = true
			}
			for i := range section.Changes {
				section.Changes[i].Path = capText(section.Changes[i].Path, 512)
				section.Changes[i].Before = capText(section.Changes[i].Before, 1024)
				section.Changes[i].After = capText(section.Changes[i].After, 1024)
			}
		}
		result.Workspace.Result = &latest
	}
	if r.checkOutput(scopedResult[replayView]{Project: value.Project, Environment: value.Environment, Result: result}) != nil {
		result.DisplayTruncated = true
		result.Workspace.Baseline = nil
		result.Workspace.Draft = nil
		result.Workspace.Result = nil
	}
	return result
}
