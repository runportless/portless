package portlessmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type testConnector struct {
	client *apiclient.Client
}

type sequenceConnector struct {
	clients []*apiclient.Client
	calls   atomic.Int32
}

// Connect returns successive test clients to model a daemon endpoint handoff.
func (c *sequenceConnector) Connect(context.Context) (*apiclient.Client, DaemonIdentity, error) {
	index := int(c.calls.Add(1)) - 1
	if index >= len(c.clients) {
		index = len(c.clients) - 1
	}
	return c.clients[index], DaemonIdentity{InstanceID: "daemon-" + string(rune('a'+index))}, nil
}

// Connect returns the test daemon API client without starting a process.
func (c testConnector) Connect(context.Context) (*apiclient.Client, DaemonIdentity, error) {
	return c.client, DaemonIdentity{InstanceID: "test-daemon"}, nil
}

func TestToolInventoryIsFixedByStartupCapabilities(t *testing.T) {
	defaultTools := []string{
		"portless_get_connection", "portless_get_environment", "portless_get_fault",
		"portless_get_operation", "portless_get_recording", "portless_get_service",
		"portless_get_service_configuration", "portless_get_timeline",
		"portless_list_connections", "portless_list_environments", "portless_list_faults",
		"portless_list_operations", "portless_list_recordings", "portless_query_traffic",
		"portless_read_logs",
	}
	sensitiveTools := []string{"portless_get_traffic_detail"}
	lifecycleTools := []string{"portless_change_service_state", "portless_start_environment", "portless_stop_environment"}
	trafficControlTools := []string{
		"portless_apply_fault", "portless_disable_all_faults", "portless_disable_fault",
		"portless_start_recording", "portless_stop_recording",
	}
	sort.Strings(defaultTools)

	for mask := 0; mask < 8; mask++ {
		config := Config{
			WorkspaceRoot: "/workspace", Version: "test",
			AllowSensitiveTraffic: mask&1 != 0,
			AllowLifecycle:        mask&2 != 0,
			AllowTrafficControl:   mask&4 != 0,
		}
		want := append([]string{}, defaultTools...)
		if config.AllowSensitiveTraffic {
			want = append(want, sensitiveTools...)
		}
		if config.AllowLifecycle {
			want = append(want, lifecycleTools...)
		}
		if config.AllowTrafficControl {
			want = append(want, trafficControlTools...)
		}
		sort.Strings(want)
		t.Run(strings.Join(capabilityNames(config), "+"), func(t *testing.T) {
			names := listToolNames(t, config, testConnector{})
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("tools = %v, want %v", names, want)
			}
		})
	}
}

func TestWorkspaceScopeAndStructuredEnvironmentResult(t *testing.T) {
	var sawMCPKind bool
	daemon := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get(contract.ClientKindHeader) == string(contract.ClientKindMCP) {
			sawMCPKind = true
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/environments/resolve":
			if request.URL.Query().Get("path") != "/workspace" {
				t.Errorf("resolve path = %q", request.URL.Query().Get("path"))
			}
			_, _ = io.WriteString(writer, `{"environments":[{"project":"billing","name":"local","revision":3,"status":"stopped","services":[],"connections":[]}],"total":1}`)
		case "/api/v1/environments/billing/local":
			_, _ = io.WriteString(writer, `{"project":"billing","name":"local","revision":3,"status":"stopped","services":[],"connections":[]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer daemon.Close()
	connector := testConnector{client: apiclient.New(daemon.URL, "secret", daemon.Client())}
	clientSession, closeSession := connectTestServer(t, Config{WorkspaceRoot: "/workspace", Version: "test"}, connector)
	defer closeSession()

	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_list_environments", Arguments: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list environments returned tool error: %#v", result.Content)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"scope":"workspace:/workspace"`) || !strings.Contains(string(encoded), `"project":"billing"`) {
		t.Fatalf("unexpected structured result: %s", encoded)
	}
	if !sawMCPKind {
		t.Fatal("daemon request did not identify the MCP client kind")
	}

	denied, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_get_environment", Arguments: map[string]any{"environment": "other/local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !denied.IsError || !strings.Contains(textContent(denied), "SCOPE_DENIED") {
		t.Fatalf("out-of-scope call = %#v", denied)
	}
}

func TestIdempotencyKeyIsStableAndNamespaced(t *testing.T) {
	caller, first, err := prepareIdempotency("service-restart", "billing/local/checkout", "retry-1")
	if err != nil {
		t.Fatal(err)
	}
	_, repeated, err := prepareIdempotency("service-restart", "billing/local/checkout", "retry-1")
	if err != nil {
		t.Fatal(err)
	}
	_, otherAction, err := prepareIdempotency("service-stop", "billing/local/checkout", "retry-1")
	if err != nil {
		t.Fatal(err)
	}
	if caller != "retry-1" || first != repeated || first == otherAction || !strings.HasPrefix(first, "mcp-") {
		t.Fatalf("unexpected keys caller=%q first=%q repeated=%q other=%q", caller, first, repeated, otherAction)
	}
}

func TestCanonicalEnvironmentSelectors(t *testing.T) {
	for _, selector := range []string{"billing/local", "shop/qa-2"} {
		if _, _, err := parseEnvironmentSelector(selector); err != nil {
			t.Errorf("parseEnvironmentSelector(%q): %v", selector, err)
		}
	}
	for _, selector := range []string{"", "billing", "Billing/local", "billing/LOCAL", "portless/local", "billing/local/extra", " billing/local"} {
		if _, _, err := parseEnvironmentSelector(selector); err == nil {
			t.Errorf("parseEnvironmentSelector(%q) unexpectedly succeeded", selector)
		}
	}
}

func TestDefaultServerRejectsDisabledToolsAndUnknownArguments(t *testing.T) {
	session, closeSession := connectTestServer(t, Config{WorkspaceRoot: "/workspace", Version: "test"}, testConnector{})
	defer closeSession()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_stop_environment", Arguments: map[string]any{"environment": "billing/local"},
	})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatalf("disabled mutation result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_list_environments", Arguments: map[string]any{"unexpected": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(textContent(result), "additional properties") {
		t.Fatalf("unknown argument result = %#v", result)
	}
}

func TestDaemonErrorMetadataIsBoundedAndRedacted(t *testing.T) {
	err := codedError{
		code: "REQUEST_FAILED", message: strings.Repeat("x", 8<<10),
		subject: map[string]any{"environment": "billing/local", "token": "secret-value"},
		details: map[string]any{"nested": map[string]any{"password": "secret-value", "reason": "safe"}},
	}
	text := err.Error()
	if strings.Contains(text, "secret-value") || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("unsafe error metadata: %s", text)
	}
	var envelope errorEnvelope
	if decodeErr := json.Unmarshal([]byte(text), &envelope); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if len(envelope.Error.Message) != 4<<10 {
		t.Fatalf("message length = %d, want %d", len(envelope.Error.Message), 4<<10)
	}
}

func TestReadReconnectsOnceButMutationDoesNotRetry(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadClient := apiclient.New(dead.URL, "secret", dead.Client())
	dead.Close()
	live := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/api/v1/environments/resolve" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, `{"environments":[],"total":0}`)
	}))
	defer live.Close()
	liveClient := apiclient.New(live.URL, "secret", live.Client())

	readConnector := &sequenceConnector{clients: []*apiclient.Client{deadClient, liveClient}}
	readSession, closeRead := connectTestServer(t, Config{WorkspaceRoot: "/workspace", Version: "test"}, readConnector)
	result, err := readSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "portless_list_environments", Arguments: map[string]any{}})
	closeRead()
	if err != nil || result.IsError {
		t.Fatalf("retried read result=%#v err=%v", result, err)
	}
	if calls := readConnector.calls.Load(); calls != 2 {
		t.Fatalf("read connector calls = %d, want 2", calls)
	}

	mutationConnector := &sequenceConnector{clients: []*apiclient.Client{deadClient, liveClient}}
	mutationSession, closeMutation := connectTestServer(t, Config{
		Environment: "billing/local", Version: "test", AllowLifecycle: true,
	}, mutationConnector)
	result, err = mutationSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "portless_start_environment", Arguments: map[string]any{"environment": "billing/local", "waitSeconds": 0},
	})
	closeMutation()
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("failed mutation unexpectedly succeeded: %#v", result)
	}
	if calls := mutationConnector.calls.Load(); calls != 1 {
		t.Fatalf("mutation connector calls = %d, want 1", calls)
	}
}

func TestServeNegotiatesOverInjectedStdioAndStopsOnEOF(t *testing.T) {
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{WorkspaceRoot: "/workspace", Version: "test-version"}, testConnector{}, Streams{
			In: clientToServerReader, Out: serverToClientWriter, Err: &diagnostics,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "portless-stdio-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}, nil)
	if err != nil {
		t.Fatal(err)
	}
	initialized := session.InitializeResult()
	if initialized.ServerInfo == nil || initialized.ServerInfo.Name != "portless" || initialized.ServerInfo.Version != "test-version" {
		t.Fatalf("server info = %#v", initialized.ServerInfo)
	}
	//lint:ignore SA1019 The test keeps the deprecated SDK field from being advertised during the SDK transition.
	if initialized.Capabilities.Resources != nil || initialized.Capabilities.Prompts != nil || initialized.Capabilities.Logging != nil {
		t.Fatalf("unexpected advertised capabilities: %#v", initialized.Capabilities)
	}
	if initialized.Capabilities.Tools == nil || initialized.Capabilities.Tools.ListChanged {
		t.Fatalf("tool capability = %#v, want fixed tool list", initialized.Capabilities.Tools)
	}
	if names := listSessionToolNames(t, session); len(names) != 15 {
		t.Fatalf("default stdio tool count = %d, want 15", len(names))
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after EOF: %v; diagnostics=%s", err, diagnostics.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop after client EOF")
	}
}

func listToolNames(t *testing.T, config Config, connector Connector) []string {
	t.Helper()
	clientSession, closeSession := connectTestServer(t, config, connector)
	defer closeSession()
	names := []string{}
	for tool, err := range clientSession.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		assertToolContract(t, tool)
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func listSessionToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	names := []string{}
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		assertToolContract(t, tool)
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func assertToolContract(t *testing.T, tool *mcp.Tool) {
	t.Helper()
	if tool.Annotations == nil || tool.Description == "" {
		t.Fatalf("tool %s is missing annotations or description", tool.Name)
	}
	for label, value := range map[string]any{"input": tool.InputSchema, "output": tool.OutputSchema} {
		schema, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("tool %s %s schema type = %T", tool.Name, label, value)
		}
		if additional, exists := schema["additionalProperties"]; !exists || additional != false {
			t.Fatalf("tool %s %s schema additionalProperties = %#v", tool.Name, label, additional)
		}
	}
}

func connectTestServer(t *testing.T, config Config, connector Connector) (*mcp.ClientSession, func()) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := newRuntime(config, connector, logger).server()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "portless-test", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		serverSession.Close()
		t.Fatal(err)
	}
	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}
}

func textContent(result *mcp.CallToolResult) string {
	var combined strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			combined.WriteString(text.Text)
		}
	}
	return combined.String()
}
