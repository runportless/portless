package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/spf13/cobra"
)

type replayOptions struct {
	against, method, path, bodyFile, headersFile string
	headers, removedHeaders                      []string
	emptyBody, yes                               bool
}

func (c *Commands) replayCommand() *cobra.Command {
	var options replayOptions
	root := &cobra.Command{Use: "replay [sequence]", Short: "Replay one live HTTP request and compare its response", Args: command.UsageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return c.replayRequest(cmd, args[0], options)
	}}
	root.Flags().StringVar(&options.against, "against", "", "destination in project/environment form (same project)")
	root.Flags().StringVar(&options.method, "method", "", "replace the HTTP method")
	root.Flags().StringVar(&options.path, "path", "", "replace the escaped path and query")
	root.Flags().StringArrayVar(&options.headers, "header", nil, "replace a header; repeat to retain multiple values (Name: Value)")
	root.Flags().StringArrayVar(&options.removedHeaders, "remove-header", nil, "explicitly omit a captured header")
	root.Flags().StringVar(&options.headersFile, "headers-file", "", "bounded JSON object of header value arrays, or - for stdin")
	root.Flags().StringVar(&options.bodyFile, "body-file", "", "read a complete replacement text body, or - for stdin")
	root.Flags().BoolVar(&options.emptyBody, "empty-body", false, "explicitly send an empty request body")
	root.Flags().BoolVar(&options.yes, "yes", false, "confirm the reviewed write to a read-write remote provider")
	root.MarkFlagsMutuallyExclusive("body-file", "empty-body")
	root.ValidArgsFunction = c.Complete(command.CompletionTraffic)
	var createdAt, daemonStartedAt string
	show := &cobra.Command{Use: "show <number>", Short: "Inspect a replay workspace in the current daemon", Args: command.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		number, err := replayNumber(args[0])
		if err != nil {
			return err
		}
		expected, err := replayExpectedIdentity(createdAt, daemonStartedAt)
		if err != nil {
			return err
		}
		api, env, err := c.Current(cmd.Context())
		if err != nil {
			return err
		}
		workspace, err := api.TrafficReplay(cmd.Context(), env.Project, env.Name, number, expected, true)
		if err != nil {
			return err
		}
		return c.printReplay(workspace)
	}}
	show.Flags().StringVar(&createdAt, "expected-created-at", "", "require this workspace creation time from its receipt")
	show.Flags().StringVar(&daemonStartedAt, "expected-daemon-started-at", "", "require this daemon time from its receipt")
	show.MarkFlagsRequiredTogether("expected-created-at", "expected-daemon-started-at")
	root.AddCommand(show)
	return root
}

func replayNumber(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return 0, command.UsageError("replay sequence or workspace number must be a positive integer")
	}
	return number, nil
}

func replayExpectedIdentity(created, daemon string) (contract.TrafficReplayIdentity, error) {
	var expected contract.TrafficReplayIdentity
	if created == "" && daemon == "" {
		return expected, nil
	}
	var err error
	expected.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return expected, command.UsageError("--expected-created-at must be a valid RFC3339 timestamp")
	}
	expected.DaemonStartedAt, err = time.Parse(time.RFC3339Nano, daemon)
	if err != nil {
		return expected, command.UsageError("--expected-daemon-started-at must be a valid RFC3339 timestamp")
	}
	return expected, nil
}

func (c *Commands) replayRequest(cmd *cobra.Command, sequenceValue string, options replayOptions) error {
	sequence, err := replayNumber(sequenceValue)
	if err != nil {
		return err
	}
	if options.bodyFile == "-" && options.headersFile == "-" {
		return command.UsageError("use stdin for either --body-file or --headers-file, not both")
	}
	api, env, err := c.Current(cmd.Context())
	if err != nil {
		return err
	}
	destination := env.Name
	if options.against != "" {
		project, name, ok := strings.Cut(options.against, "/")
		if !ok || project != env.Project || name == "" || strings.Contains(name, "/") {
			return command.UsageError("--against must name an environment in the original project")
		}
		destination = name
	}
	baseline, err := api.TrafficExchange(cmd.Context(), env.Project, env.Name, sequence)
	if err != nil {
		return err
	}
	workspace, err := api.PrepareTrafficReplay(cmd.Context(), env.Project, env.Name, contract.PrepareTrafficReplayRequest{Sequence: sequence, StartedAt: baseline.StartedAt})
	if err != nil {
		return err
	}
	prepared := workspace
	sendAttempted := false
	defer func() {
		if !sendAttempted {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = api.DeleteTrafficReplay(cleanupCtx, env.Project, env.Name, prepared.Number, prepared.TrafficReplayIdentity)
		}
	}()
	if workspace.Draft == nil {
		return errors.New("replay preparation returned no request draft")
	}
	draft, err := editReplayDraft(*workspace.Draft, destination, options, cmd.InOrStdin())
	if err != nil {
		return err
	}
	workspace, err = api.UpdateTrafficReplayDraft(cmd.Context(), env.Project, env.Name, workspace.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: draft})
	if err != nil {
		return err
	}
	if workspace.Destination != nil && workspace.Destination.RequiresConfirmation && !options.yes {
		return command.UsageError("replay would send %s to %s/%s service %s (%s, read-write); review these inputs and repeat with --yes", draft.Method, env.Project, destination, baseline.Target, workspace.Destination.Classification)
	}
	input := contract.RunTrafficReplayRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, RunNumber: workspace.NextRunNumber, ConfirmRemoteWrite: options.yes}
	sendAttempted = true
	admitted, sendErr := api.RunTrafficReplay(cmd.Context(), env.Project, env.Name, workspace.Number, input)
	if sendErr != nil {
		var rejected *client.ClientError
		if errors.As(sendErr, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
			return sendErr
		}
		// Only inspect after an uncertain response. Repeating POST could send a write again.
		checkCtx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
		defer cancel()
		current, checkErr := api.TrafficReplay(checkCtx, env.Project, env.Name, workspace.Number, workspace.TrafficReplayIdentity, false)
		if checkErr != nil || current.Run == nil || current.Run.Number != input.RunNumber {
			return replayWaitError(workspace, sendErr)
		}
		admitted = current
	}
	if !c.JSONOutput {
		fmt.Fprintf(c.Err, "Replaying request #%d against %s/%s…\n", sequence, env.Project, destination)
	}
	result, err := waitForReplay(cmd.Context(), api, admitted)
	if err != nil {
		return replayWaitError(workspace, err)
	}
	return c.printReplay(result)
}

func editReplayDraft(original contract.TrafficReplayDraft, destination string, options replayOptions, stdin io.Reader) (contract.TrafficReplayDraft, error) {
	draft := original
	draft.Environment = destination
	draft.Headers = make(map[string][]string, len(original.Headers))
	for name, values := range original.Headers {
		draft.Headers[name] = append([]string(nil), values...)
	}
	draft.OmittedHeaders = append([]string(nil), original.OmittedHeaders...)
	if options.method != "" {
		draft.Method = strings.ToUpper(options.method)
	}
	if options.path != "" {
		draft.RequestTarget = options.path
	}
	if options.bodyFile != "" {
		content, err := readReplayInput(options.bodyFile, stdin, contract.TrafficReplayMaxBodyBytes)
		if err != nil {
			return draft, err
		}
		draft.BodyMode, draft.Body = "replacement", string(content)
	}
	if options.emptyBody {
		draft.BodyMode, draft.Body = "empty", ""
	}
	replacements := make(map[string][]string)
	if options.headersFile != "" {
		content, err := readReplayInput(options.headersFile, stdin, 64<<10)
		if err != nil {
			return draft, err
		}
		var values map[string][]string
		if json.Unmarshal(content, &values) != nil || values == nil {
			return draft, command.UsageError("--headers-file must contain one JSON object with arrays of header values")
		}
		for name, entries := range values {
			key := textproto.CanonicalMIMEHeaderKey(name)
			if _, duplicate := replacements[key]; duplicate {
				return draft, command.UsageError("--headers-file must not repeat a header name with different capitalization")
			}
			replacements[key] = append(replacements[key], entries...)
		}
	}
	for _, header := range options.headers {
		name, value, ok := strings.Cut(header, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return draft, command.UsageError("--header must use Name: Value")
		}
		name = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		replacements[name] = append(replacements[name], strings.TrimSpace(value))
	}
	for _, name := range options.removedHeaders {
		name = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if name == "" {
			return draft, command.UsageError("--remove-header requires a header name")
		}
		if _, exists := replacements[name]; exists {
			return draft, command.UsageError("a header cannot be both replaced and removed")
		}
		for key := range draft.Headers {
			if strings.EqualFold(key, name) {
				delete(draft.Headers, key)
			}
		}
		draft.OmittedHeaders = append(draft.OmittedHeaders, name)
	}
	for name, values := range replacements {
		for key := range draft.Headers {
			if strings.EqualFold(key, name) {
				delete(draft.Headers, key)
			}
		}
		draft.Headers[name] = values
	}
	return draft, nil
}

func readReplayInput(path string, stdin io.Reader, limit int64) ([]byte, error) {
	reader := stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open replay input: %w", err)
		}
		defer file.Close()
		reader = file
	}
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, errors.New("read replay input failed")
	}
	if int64(len(content)) > limit {
		return nil, command.UsageError("replay input exceeds its byte limit")
	}
	return content, nil
}

func waitForReplay(ctx context.Context, api *client.Client, workspace contract.TrafficReplayWorkspace) (contract.TrafficReplayWorkspace, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if workspace.Run != nil && workspace.Run.State != "running" {
			return api.TrafficReplay(ctx, workspace.Project, workspace.Environment, workspace.Number, workspace.TrafficReplayIdentity, true)
		}
		select {
		case <-ctx.Done():
			return workspace, ctx.Err()
		case <-ticker.C:
		}
		next, err := api.TrafficReplay(ctx, workspace.Project, workspace.Environment, workspace.Number, workspace.TrafficReplayIdentity, false)
		if err != nil {
			return workspace, err
		}
		workspace = next
	}
}

func replayWaitError(workspace contract.TrafficReplayWorkspace, cause error) error {
	return fmt.Errorf("replay result is not confirmed; the request may have reached the application; inspect with portless --env %s/%s traffic replay show %d --expected-created-at %s --expected-daemon-started-at %s: %w", workspace.Project, workspace.Environment, workspace.Number, workspace.CreatedAt.Format(time.RFC3339Nano), workspace.DaemonStartedAt.Format(time.RFC3339Nano), cause)
}

func (c *Commands) printReplay(workspace contract.TrafficReplayWorkspace) error {
	if c.JSONOutput {
		if err := command.WriteJSON(c.Out, workspace); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(c.Out, "Replay %d · %s/%s\nCreated %s · daemon %s\n", workspace.Number, workspace.Project, workspace.Environment, workspace.CreatedAt.Format(time.RFC3339Nano), workspace.DaemonStartedAt.Format(time.RFC3339Nano))
		if workspace.Baseline != nil {
			fmt.Fprintf(c.Out, "Original #%d: %s %s · %s → %s\n", workspace.Baseline.Sequence, workspace.Baseline.Method, workspace.Baseline.RequestTarget, workspace.Baseline.Source, workspace.Baseline.Target)
		}
		if workspace.Run != nil {
			fmt.Fprintf(c.Out, "Run %d: %s · %s\n", workspace.Run.Number, workspace.Run.State, workspace.Run.Outcome)
		}
		if workspace.Result != nil {
			result := workspace.Result
			diff := result.Comparison
			fmt.Fprintf(c.Out, "Destination: %s/%s (%s)\nResponse: %d → %d · duration %+d ms\nHeaders: %s · body: %s\n", workspace.Project, result.Destination.Environment, result.Destination.Provider, diff.OriginalStatus, diff.ReplayStatus, diff.DurationDeltaMS, diff.Headers.State, diff.Body.State)
			for _, section := range []contract.TrafficComparisonSection{diff.Headers, diff.Body} {
				if section.Reason != "" {
					fmt.Fprintln(c.Out, section.Reason)
				}
				for _, change := range section.Changes {
					fmt.Fprintf(c.Out, "  %s %s: %s → %s\n", change.Kind, change.Path, change.Before, change.After)
				}
			}
		}
	}
	if workspace.Run != nil && (workspace.Run.State == "failed" || workspace.Run.State == "interrupted") {
		if c.JSONOutput {
			return &command.ReportedError{}
		}
		return fmt.Errorf("replay %s (%s): %s", workspace.Run.State, workspace.Run.Outcome, workspace.Run.Error)
	}
	return nil
}
