package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/runtime/logstore"
)

func TestProjectAndEnvironmentAPIsAndHostsAreSeparated(t *testing.T) {
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(context.Background(), "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	checkoutSource := t.TempDir()
	sources := []model.SourceBinding{{Name: "checkout", Path: checkoutSource, Status: "ready", ScannedAt: time.Now().UTC(), Definition: definition}}
	if _, err := controlStore.CreateEnvironment(context.Background(), "billing", "local", definition, sources, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	app := controlplane.New(controlStore, events.NewBroker(), controlplane.Config{DataDirectory: data, InstallationKey: "test-installation"})
	defer app.Close(context.Background())
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>portless</html>"), Mode: fs.FileMode(0o600)}}
	daemonControl := &fakeDaemonControl{identity: contract.DaemonStatus{
		State: "ready", PID: 33083, StartedAt: time.Now().UTC().Add(-time.Minute),
		InstanceID: "instance-current", BuildID: "build-current", ProtocolVersion: "2.0.0", APIVersion: contract.APIVersion,
		RecoveryProblems: []string{}, ActiveEnvironments: []string{"billing/local"},
	}, logs: contract.DaemonLogSnapshot{Content: "time=2026-08-25T12:00:00Z level=INFO msg=\"Portless daemon ready\"\n", Truncated: true}, handoff: contract.DaemonHandoffStatus{State: "ready", VerifiedAt: time.Now().UTC(), Problems: []string{}, ActiveEnvironments: []string{"billing/local"}}}
	server, err := New(Dependencies{Application: app, Auth: authManager, Assets: assets, DaemonControl: daemonControl})
	if err != nil {
		t.Fatal(err)
	}
	server.inspectRelay = func(context.Context) (contract.RelayStatus, error) {
		return contract.RelayStatus{Platform: "launchd", Service: "dev.portless.relay", Installed: true, Running: true, Healthy: true, HTTPHealthy: true, HelperPresent: true, HelperCurrent: true, HelperBuildID: "build-current", CurrentBuildID: "build-current", DNSHealthy: true, ResolverPresent: true, ResolverHealthy: true, EndpointPoolReady: true, EndpointPoolDetail: "64/64 addresses configured on lo0", DNSListenAddress: "127.77.0.1:1053", ResolverPath: "/etc/resolver/portless.test", LocalhostResolverPath: "/etc/resolver/portless.localhost"}, nil
	}

	unauthenticated := request(server, authManager, http.MethodGet, "/api/v1/projects", "", false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control request returned %d", unauthenticated.Code)
	}
	unauthenticatedLogs := request(server, authManager, http.MethodGet, "/api/v1/daemon/logs", "", false)
	if unauthenticatedLogs.Code != http.StatusUnauthorized || daemonControl.logCalls != 0 {
		t.Fatalf("unauthenticated daemon logs response code=%d calls=%d", unauthenticatedLogs.Code, daemonControl.logCalls)
	}
	projects := request(server, authManager, http.MethodGet, "/api/v1/projects", "", true)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body.String(), `"name":"billing"`) || !strings.Contains(projects.Body.String(), `"environments":[`) || !strings.Contains(projects.Body.String(), `"total":1`) {
		t.Fatalf("projects response code=%d body=%s", projects.Code, projects.Body.String())
	}
	mcpSession := requestClientKind(server, authManager, http.MethodGet, "/api/v1/session", "", string(contract.ClientKindMCP))
	if mcpSession.Code != http.StatusOK || !strings.Contains(mcpSession.Body.String(), `"actor":"MCP"`) {
		t.Fatalf("MCP session response code=%d body=%s", mcpSession.Code, mcpSession.Body.String())
	}
	invalidClient := requestClientKind(server, authManager, http.MethodGet, "/api/v1/session", "", "automation")
	if invalidClient.Code != http.StatusBadRequest || !strings.Contains(invalidClient.Body.String(), `"code":"INVALID_CLIENT_KIND"`) {
		t.Fatalf("invalid client kind response code=%d body=%s", invalidClient.Code, invalidClient.Body.String())
	}
	environment := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local", "", true)
	if environment.Code != http.StatusOK || !strings.Contains(environment.Body.String(), `"project":"billing"`) || !strings.Contains(environment.Body.String(), `"name":"local"`) ||
		!strings.Contains(environment.Body.String(), `"dashboardUrl":"http://portless.localhost/environments/billing/local"`) ||
		!strings.Contains(environment.Body.String(), `"url":"http://checkout.local.billing.localhost"`) || !strings.Contains(environment.Body.String(), `"createdAt":`) ||
		!strings.Contains(environment.Body.String(), `"modifiedAt":`) || strings.Contains(environment.Body.String(), ".localhost:7331") {
		t.Fatalf("environment API did not use clean scoped URLs: %s", environment.Body.String())
	}
	checkoutInUse := request(server, authManager, http.MethodDelete, "/api/v1/environments/billing/local/sources/checkout", "", true)
	if checkoutInUse.Code != http.StatusConflict || !strings.Contains(checkoutInUse.Body.String(), `"code":"CHECKOUT_IN_USE"`) || !strings.Contains(checkoutInUse.Body.String(), `"services":["checkout"]`) {
		t.Fatalf("checkout in-use response code=%d body=%s", checkoutInUse.Code, checkoutInUse.Body.String())
	}
	mockBase := "/api/v1/environments/billing/local/mocks/checkout-empty"
	createdMock := request(server, authManager, http.MethodPost, "/api/v1/environments/billing/local/mocks", `{"name":"checkout-empty","service":"checkout","description":"predictable checkout"}`, true)
	if createdMock.Code != http.StatusCreated || !strings.Contains(createdMock.Body.String(), `"mock":{"project":"billing","environment":"local","name":"checkout-empty"`) || !strings.Contains(createdMock.Body.String(), `"warnings":[]`) {
		t.Fatalf("mock create response code=%d body=%s", createdMock.Code, createdMock.Body.String())
	}
	updatedMock := request(server, authManager, http.MethodPut, mockBase+"/routes/health", `{"method":"GET","path":"/health","status":200,"headers":{"Content-Type":"application/json"},"body":"{\"ready\":true}","enabled":true}`, true)
	if updatedMock.Code != http.StatusOK || !strings.Contains(updatedMock.Body.String(), `"name":"health"`) || !strings.Contains(updatedMock.Body.String(), `"method":"GET"`) {
		t.Fatalf("mock route response code=%d body=%s", updatedMock.Code, updatedMock.Body.String())
	}
	previewMock := request(server, authManager, http.MethodPost, mockBase+"/preview", `{"method":"GET","path":"/health","headers":{"Accept":["application/json"],"X-Trace":["one","two"]},"body":"preview payload"}`, true)
	if previewMock.Code != http.StatusOK || !strings.Contains(previewMock.Body.String(), `"matched":true`) || !strings.Contains(previewMock.Body.String(), `"route":"health"`) {
		t.Fatalf("mock preview response code=%d body=%s", previewMock.Code, previewMock.Body.String())
	}
	listedMocks := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/mocks", "", true)
	if listedMocks.Code != http.StatusOK || !strings.Contains(listedMocks.Body.String(), `"mocks":[`) || !strings.Contains(listedMocks.Body.String(), `"checkout-empty"`) {
		t.Fatalf("mock list response code=%d body=%s", listedMocks.Code, listedMocks.Body.String())
	}
	deletedMockRoute := request(server, authManager, http.MethodDelete, mockBase+"/routes/health", "", true)
	if deletedMockRoute.Code != http.StatusOK || !strings.Contains(deletedMockRoute.Body.String(), `"routes":[]`) {
		t.Fatalf("mock route delete response code=%d body=%s", deletedMockRoute.Code, deletedMockRoute.Body.String())
	}
	deletedMock := request(server, authManager, http.MethodDelete, mockBase, "", true)
	if deletedMock.Code != http.StatusNoContent {
		t.Fatalf("mock delete response code=%d body=%s", deletedMock.Code, deletedMock.Body.String())
	}
	inventorySource := t.TempDir()
	if err := os.WriteFile(filepath.Join(inventorySource, "package.json"), []byte(`{"name":"inventory","scripts":{"start:dev":"node server.js"},"dependencies":{"@nestjs/core":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	addedSource := request(server, authManager, http.MethodPost, "/api/v1/projects/billing/sources", `{"name":"inventory","environment":"local","path":`+strconv.Quote(inventorySource)+`}`, true)
	if addedSource.Code != http.StatusCreated || !strings.Contains(addedSource.Body.String(), `"name":"inventory"`) || !strings.Contains(addedSource.Body.String(), `"environment":{"project":"billing","name":"local"`) || !strings.Contains(addedSource.Body.String(), `"configurationRequired":[]`) {
		t.Fatalf("project source response code=%d body=%s", addedSource.Code, addedSource.Body.String())
	}

	started := time.Now().UTC().Add(-time.Second)
	httpExchange := app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Source: "checkout", Target: "orders", StartedAt: started, CompletedAt: started.Add(12 * time.Millisecond), Method: "GET", Path: "/orders", RequestTarget: "/orders?state=open", Status: 200, TraceContextSource: model.TrafficTraceContextGenerated, RequestHeaders: map[string][]string{"Accept": {"application/json"}}, ResponseHeaders: map[string][]string{"Content-Type": {"application/json"}}})
	tcpExchange := app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolTCP, Source: "checkout", Target: "postgres", Background: true, StartedAt: started, CompletedAt: started.Add(2 * time.Millisecond), RequestBytes: 18, ResponseBytes: 24,
		TCP: &model.TrafficTCPExchange{Kind: model.TrafficTCPKindOperation, ApplicationProtocol: model.ApplicationProtocolPostgreSQL, Operation: "SELECT", Inspection: model.TrafficInspectionDecoded, Outcome: model.TrafficTCPOutcomeSuccess, RequestMessageCount: 1, ResponseMessageCount: 1,
			RequestMessages: []model.TrafficMessage{{Type: "query", Content: "SELECT 1", ContentType: "text/x-sql", WireBytes: 18}}, ResponseMessages: []model.TrafficMessage{{Type: "row", Content: `[1]`, ContentType: "application/json", WireBytes: 24}}}})
	httpTraffic := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/exchanges?protocol=http&service=checkout&limit=10", "", true)
	if httpTraffic.Code != http.StatusOK || !strings.Contains(httpTraffic.Body.String(), `"target":"orders"`) || !strings.Contains(httpTraffic.Body.String(), `"traceContextSource":"generated"`) || strings.Contains(httpTraffic.Body.String(), `"target":"postgres"`) || strings.Contains(httpTraffic.Body.String(), `"requestHeaders"`) {
		t.Fatalf("filtered HTTP traffic response code=%d body=%s", httpTraffic.Code, httpTraffic.Body.String())
	}
	tcpTraffic := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/exchanges?protocol=tcp&edge=checkout:postgres", "", true)
	if tcpTraffic.Code != http.StatusOK || !strings.Contains(tcpTraffic.Body.String(), `"protocol":"tcp"`) || !strings.Contains(tcpTraffic.Body.String(), `"background":true`) || !strings.Contains(tcpTraffic.Body.String(), `"applicationProtocol":"postgresql"`) || !strings.Contains(tcpTraffic.Body.String(), `"operation":"SELECT"`) || strings.Contains(tcpTraffic.Body.String(), `"requestMessages"`) {
		t.Fatalf("filtered TCP traffic response code=%d body=%s", tcpTraffic.Code, tcpTraffic.Body.String())
	}
	tcpDetail := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/exchanges/"+strconv.FormatInt(tcpExchange.Sequence, 10), "", true)
	if tcpDetail.Code != http.StatusOK || !strings.Contains(tcpDetail.Body.String(), `"requestMessages":[{"type":"query"`) || !strings.Contains(tcpDetail.Body.String(), `"content":"SELECT 1"`) || !strings.Contains(tcpDetail.Body.String(), `"responseMessages":[{"type":"row"`) {
		t.Fatalf("TCP traffic detail response code=%d body=%s", tcpDetail.Code, tcpDetail.Body.String())
	}
	trafficDetail := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/exchanges/"+strconv.FormatInt(httpExchange.Sequence, 10), "", true)
	if trafficDetail.Code != http.StatusOK || !strings.Contains(trafficDetail.Body.String(), `"requestTarget":"/orders?state=open"`) || !strings.Contains(trafficDetail.Body.String(), `"traceContextSource":"generated"`) || !strings.Contains(trafficDetail.Body.String(), `"requestHeaders":{"Accept":["application/json"]}`) || !strings.Contains(trafficDetail.Body.String(), `"responseHeaders":{"Content-Type":["application/json"]}`) {
		t.Fatalf("traffic detail response code=%d body=%s", trafficDetail.Code, trafficDetail.Body.String())
	}
	defaultTraces := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/traces", "", true)
	if defaultTraces.Code != http.StatusOK || strings.Contains(defaultTraces.Body.String(), `"protocol":"tcp"`) || strings.Contains(defaultTraces.Body.String(), `"background":true`) {
		t.Fatalf("default traces retained background TCP housekeeping code=%d body=%s", defaultTraces.Code, defaultTraces.Body.String())
	}
	traces := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/traces?background=include", "", true)
	if traces.Code != http.StatusOK || !strings.Contains(traces.Body.String(), `"traces":[`) || !strings.Contains(traces.Body.String(), `"protocol":"http"`) || !strings.Contains(traces.Body.String(), `"provisional":false`) || strings.Contains(traces.Body.String(), `"spans"`) {
		t.Fatalf("trace summaries response code=%d body=%s", traces.Code, traces.Body.String())
	}
	traceDetail := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/traces/"+strconv.FormatInt(httpExchange.Sequence, 10), "", true)
	if traceDetail.Code != http.StatusOK || !strings.Contains(traceDetail.Body.String(), `"spans":[`) || !strings.Contains(traceDetail.Body.String(), `"requestTarget":"/orders?state=open"`) {
		t.Fatalf("trace detail response code=%d body=%s", traceDetail.Code, traceDetail.Body.String())
	}
	clearedTraffic := request(server, authManager, http.MethodDelete, "/api/v1/environments/billing/local/traffic", "", true)
	if clearedTraffic.Code != http.StatusOK || !strings.Contains(clearedTraffic.Body.String(), `"cleared":2`) || !strings.Contains(clearedTraffic.Body.String(), `"throughSequence":2`) {
		t.Fatalf("clear traffic response code=%d body=%s", clearedTraffic.Code, clearedTraffic.Body.String())
	}
	afterClear := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/exchanges?protocol=all", "", true)
	if afterClear.Code != http.StatusOK || afterClear.Body.String() != "{\"exchanges\":[]}\n" {
		t.Fatalf("traffic after clear response code=%d body=%s", afterClear.Code, afterClear.Body.String())
	}
	invalidTraffic := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/exchanges?protocol=udp", "", true)
	if invalidTraffic.Code != http.StatusBadRequest || !strings.Contains(invalidTraffic.Body.String(), `"code":"INVALID_TRAFFIC_PROTOCOL"`) {
		t.Fatalf("invalid traffic protocol response code=%d body=%s", invalidTraffic.Code, invalidTraffic.Body.String())
	}
	invalidLimit := request(server, authManager, http.MethodGet, "/api/v1/projects?limit=0", "", true)
	if invalidLimit.Code != http.StatusBadRequest || !strings.Contains(invalidLimit.Body.String(), `"code":"INVALID_LIMIT"`) {
		t.Fatalf("invalid limit response code=%d body=%s", invalidLimit.Code, invalidLimit.Body.String())
	}
	resetPlan := request(server, authManager, http.MethodGet, "/api/v1/runtime/reset", "", true)
	if resetPlan.Code != http.StatusOK || !strings.Contains(resetPlan.Body.String(), `"projects":1`) || !strings.Contains(resetPlan.Body.String(), `"environments":1`) || !strings.Contains(resetPlan.Body.String(), `"topologyIncompatible":false`) {
		t.Fatalf("runtime reset plan response code=%d body=%s", resetPlan.Code, resetPlan.Body.String())
	}
	faultPath := "/api/v1/environments/billing/local/faults/api-latency"
	createdFault := request(server, authManager, http.MethodPost, "/api/v1/environments/billing/local/faults", `{"name":"api-latency","source":"external","target":"checkout","probability":1,"latencyMs":50}`, true)
	if createdFault.Code != http.StatusCreated || !strings.Contains(createdFault.Body.String(), `"enabled":true`) {
		t.Fatalf("fault create response code=%d body=%s", createdFault.Code, createdFault.Body.String())
	}
	disabledFaults := request(server, authManager, http.MethodPost, "/api/v1/environments/billing/local/faults/disable-all", "", true)
	if disabledFaults.Code != http.StatusOK || !strings.Contains(disabledFaults.Body.String(), `"disabled":1`) {
		t.Fatalf("disable-all response code=%d body=%s", disabledFaults.Code, disabledFaults.Body.String())
	}
	reenabledFault := request(server, authManager, http.MethodPost, faultPath+"/enable", "", true)
	if reenabledFault.Code != http.StatusOK || !strings.Contains(reenabledFault.Body.String(), `"enabled":true`) {
		t.Fatalf("fault enable response code=%d body=%s", reenabledFault.Code, reenabledFault.Body.String())
	}
	disabledFault := request(server, authManager, http.MethodPost, faultPath+"/disable", "", true)
	if disabledFault.Code != http.StatusNoContent {
		t.Fatalf("fault disable response code=%d body=%s", disabledFault.Code, disabledFault.Body.String())
	}
	deletedFault := request(server, authManager, http.MethodDelete, faultPath, "", true)
	if deletedFault.Code != http.StatusNoContent {
		t.Fatalf("fault delete response code=%d body=%s", deletedFault.Code, deletedFault.Body.String())
	}
	missingFault := request(server, authManager, http.MethodGet, faultPath, "", true)
	if missingFault.Code != http.StatusNotFound {
		t.Fatalf("deleted fault response code=%d body=%s", missingFault.Code, missingFault.Body.String())
	}
	daemonStatus := request(server, authManager, http.MethodGet, "/api/v1/daemon", "", true)
	if daemonStatus.Code != http.StatusOK || !strings.Contains(daemonStatus.Body.String(), `"instanceId":"instance-current"`) || !strings.Contains(daemonStatus.Body.String(), `"protocolVersion":"2.0.0"`) || strings.Contains(daemonStatus.Body.String(), "installationId") {
		t.Fatalf("daemon status response code=%d body=%s", daemonStatus.Code, daemonStatus.Body.String())
	}
	if daemonControl.handoffCalls != 0 {
		t.Fatalf("shallow daemon status performed %d handoff audits", daemonControl.handoffCalls)
	}
	daemonLogs := request(server, authManager, http.MethodGet, "/api/v1/daemon/logs", "", true)
	var logSnapshot contract.DaemonLogSnapshot
	if err := json.Unmarshal(daemonLogs.Body.Bytes(), &logSnapshot); err != nil {
		t.Fatalf("decode daemon logs: %v; body=%s", err, daemonLogs.Body.String())
	}
	if daemonLogs.Code != http.StatusOK || !strings.Contains(logSnapshot.Content, "Portless daemon ready") || !logSnapshot.Truncated || daemonControl.logCalls != 1 {
		t.Fatalf("daemon logs response code=%d snapshot=%#v calls=%d", daemonLogs.Code, logSnapshot, daemonControl.logCalls)
	}
	daemonControl.logsErr = errors.New("private daemon log is unavailable")
	failedDaemonLogs := request(server, authManager, http.MethodGet, "/api/v1/daemon/logs", "", true)
	if failedDaemonLogs.Code != http.StatusServiceUnavailable || !strings.Contains(failedDaemonLogs.Body.String(), `"code":"DAEMON_LOG_UNAVAILABLE"`) {
		t.Fatalf("failed daemon logs response code=%d body=%s", failedDaemonLogs.Code, failedDaemonLogs.Body.String())
	}
	daemonControl.logsErr = nil
	daemonHandoff := request(server, authManager, http.MethodGet, "/api/v1/daemon/handoff", "", true)
	if daemonHandoff.Code != http.StatusOK || !strings.Contains(daemonHandoff.Body.String(), `"state":"ready"`) || daemonControl.handoffCalls != 1 {
		t.Fatalf("daemon handoff response code=%d body=%s calls=%d", daemonHandoff.Code, daemonHandoff.Body.String(), daemonControl.handoffCalls)
	}
	relayStatus := request(server, authManager, http.MethodGet, "/api/v1/relay", "", true)
	if relayStatus.Code != http.StatusOK || !strings.Contains(relayStatus.Body.String(), `"httpHealthy":true`) || !strings.Contains(relayStatus.Body.String(), `"helperCurrent":true`) || !strings.Contains(relayStatus.Body.String(), `"helperBuildId":"build-current"`) || !strings.Contains(relayStatus.Body.String(), `"dnsHealthy":true`) || !strings.Contains(relayStatus.Body.String(), `"resolverHealthy":true`) || !strings.Contains(relayStatus.Body.String(), `"dnsListenAddress":"127.77.0.1:1053"`) || !strings.Contains(relayStatus.Body.String(), `"localhostResolverPath":"/etc/resolver/portless.localhost"`) {
		t.Fatalf("relay status response code=%d body=%s", relayStatus.Code, relayStatus.Body.String())
	}
	daemonRestart := request(server, authManager, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"instance-current"}`, true)
	if daemonRestart.Code != http.StatusAccepted || !strings.Contains(daemonRestart.Body.String(), `"restarting":true`) || daemonControl.restartedInstance != "instance-current" {
		t.Fatalf("daemon restart response code=%d body=%s restarted=%q", daemonRestart.Code, daemonRestart.Body.String(), daemonControl.restartedInstance)
	}
	staleRestart := request(server, authManager, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"stale"}`, true)
	if staleRestart.Code != http.StatusConflict || !strings.Contains(staleRestart.Body.String(), `"code":"DAEMON_INSTANCE_CHANGED"`) {
		t.Fatalf("stale daemon restart response code=%d body=%s", staleRestart.Code, staleRestart.Body.String())
	}
	claim, _, err := authManager.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, csrf, _, _, err := authManager.ConsumeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	missingCSRF := requestBrowser(server, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"instance-current"}`, sessionToken, "")
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("browser restart without CSRF returned %d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}
	browserRestart := requestBrowser(server, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"instance-current"}`, sessionToken, csrf)
	if browserRestart.Code != http.StatusAccepted {
		t.Fatalf("browser restart with CSRF returned %d body=%s", browserRestart.Code, browserRestart.Body.String())
	}
	browserReset := requestBrowser(server, http.MethodPost, "/api/v1/runtime/reset", "", sessionToken, csrf)
	if browserReset.Code != http.StatusForbidden || !strings.Contains(browserReset.Body.String(), `"code":"CLI_AUTH_REQUIRED"`) {
		t.Fatalf("browser runtime reset returned %d body=%s", browserReset.Code, browserReset.Body.String())
	}
	browserSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(browserSource, "package.json"), []byte(`{"name":"catalog","scripts":{"start":"node server.js"},"dependencies":{"express":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	browserAddedSource := requestBrowser(server, http.MethodPost, "/api/v1/projects/billing/sources", `{"name":"catalog","environment":"local","path":`+strconv.Quote(browserSource)+`}`, sessionToken, csrf)
	if browserAddedSource.Code != http.StatusCreated || !strings.Contains(browserAddedSource.Body.String(), `"name":"catalog"`) {
		t.Fatalf("browser project source response code=%d body=%s", browserAddedSource.Code, browserAddedSource.Body.String())
	}
	browserDeletedSource := requestBrowser(server, http.MethodDelete, "/api/v1/projects/billing/sources/catalog", "", sessionToken, csrf)
	if browserDeletedSource.Code != http.StatusOK || !strings.Contains(browserDeletedSource.Body.String(), `"removedServices":["catalog"]`) || !strings.Contains(browserDeletedSource.Body.String(), `"environments":[`) {
		t.Fatalf("browser project source delete response code=%d body=%s", browserDeletedSource.Code, browserDeletedSource.Body.String())
	}
	missingSource := requestBrowser(server, http.MethodDelete, "/api/v1/projects/billing/sources/missing", "", sessionToken, csrf)
	if missingSource.Code != http.StatusNotFound || !strings.Contains(missingSource.Body.String(), `"code":"RESOURCE_NOT_FOUND"`) {
		t.Fatalf("missing project source delete response code=%d body=%s", missingSource.Code, missingSource.Body.String())
	}

	privateKey, err := controlStore.PrivateEnvironmentKey(context.Background(), "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	logDirectory := filepath.Join(data, "environments", privateKey, "logs", "checkout", "1")
	sink, err := logstore.OpenSink(logDirectory, "checkout", "stdout", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write([]byte("ready on an allocated port\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	logs := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/logs?limit=10&since=1h", "", true)
	if logs.Code != http.StatusOK || !strings.Contains(logs.Body.String(), `"service":"checkout"`) || !strings.Contains(logs.Body.String(), `"stream":"stdout"`) || !strings.Contains(logs.Body.String(), `"message":"ready on an allocated port"`) {
		t.Fatalf("structured logs response code=%d body=%s", logs.Code, logs.Body.String())
	}

	binding := request(server, authManager, http.MethodPut, "/api/v1/environments/billing/local/bindings/checkout", `{"provider":"remote","remote":{"url":"https://checkout.qa.example.test","classification":"qa","writePolicy":"read-only","healthPath":"/health"}}`, true)
	if binding.Code != http.StatusAccepted || !strings.Contains(binding.Body.String(), `"type":"change-provider"`) || !strings.Contains(binding.Body.String(), `"state":"running"`) {
		t.Fatalf("remote binding response code=%d body=%s", binding.Code, binding.Body.String())
	}
	var bindingOperation model.Operation
	if err := json.Unmarshal(binding.Body.Bytes(), &bindingOperation); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for bindingOperation.State == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		bindingOperation, err = app.Operation(context.Background(), "billing", "local", bindingOperation.Number)
		if err != nil {
			t.Fatal(err)
		}
	}
	if bindingOperation.State != "succeeded" {
		t.Fatalf("remote binding operation = %#v", bindingOperation)
	}
	if err := controlStore.SetEnvironmentStatus(context.Background(), "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	blockedReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset", "", true)
	if blockedReset.Code != http.StatusConflict || !strings.Contains(blockedReset.Body.String(), `"code":"ACTIVE_ENVIRONMENTS"`) {
		t.Fatalf("active environment reset returned %d body=%s", blockedReset.Code, blockedReset.Body.String())
	}
	forcedReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset", `{"force":true}`, true)
	if forcedReset.Code != http.StatusOK || !strings.Contains(forcedReset.Body.String(), `"processes":0`) {
		t.Fatalf("forced active-environment reset returned %d body=%s", forcedReset.Code, forcedReset.Body.String())
	}
	forcedResetCanceled := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset/cancel", "", true)
	if forcedResetCanceled.Code != http.StatusNoContent {
		t.Fatalf("forced reset cancellation returned %d body=%s", forcedResetCanceled.Code, forcedResetCanceled.Body.String())
	}
	if err := controlStore.SetEnvironmentStatus(context.Background(), "billing", "local", model.EnvironmentStopped, ""); err != nil {
		t.Fatal(err)
	}
	preparedReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset", "", true)
	if preparedReset.Code != http.StatusOK || !strings.Contains(preparedReset.Body.String(), `"processes":0`) || !strings.Contains(preparedReset.Body.String(), `"runtimes":[]`) {
		t.Fatalf("runtime reset preparation returned %d body=%s", preparedReset.Code, preparedReset.Body.String())
	}
	canceledReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset/cancel", "", true)
	if canceledReset.Code != http.StatusNoContent {
		t.Fatalf("runtime reset cancellation returned %d body=%s", canceledReset.Code, canceledReset.Body.String())
	}

	browserClaim := requestHost(server, authManager, http.MethodPost, "/api/v1/browser-claims", `{"next":"/environments/billing/local"}`, true, "127.0.0.1:7331")
	if browserClaim.Code != http.StatusCreated || !strings.Contains(browserClaim.Body.String(), `"url":"http://portless.localhost/auth/claim/`) || strings.Contains(browserClaim.Body.String(), ":7331") {
		t.Fatalf("browser claim did not use clean control origin: %d %s", browserClaim.Code, browserClaim.Body.String())
	}

	applicationAPI := requestHost(server, authManager, http.MethodGet, "/api/v1/projects", "", false, "checkout.local.billing.localhost")
	if applicationAPI.Code != http.StatusMisdirectedRequest {
		t.Fatalf("application host reached control API: %d", applicationAPI.Code)
	}
	unknown := requestHost(server, authManager, http.MethodGet, "/api/v1/health", "", false, "malicious.example")
	if unknown.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host returned %d", unknown.Code)
	}
	legacyModel := []byte(`{"suggestedName":"billing"}`)
	if _, err := controlStore.DB().ExecContext(context.Background(), `UPDATE projects SET model_json = ?`, legacyModel); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(context.Background(), `UPDATE environments SET model_json = ?`, legacyModel); err != nil {
		t.Fatal(err)
	}
	incompatible := request(server, authManager, http.MethodGet, "/api/v1/environments", "", true)
	if incompatible.Code != http.StatusConflict || !strings.Contains(incompatible.Body.String(), `"code":"INCOMPATIBLE_STATE"`) || !strings.Contains(incompatible.Body.String(), "portless reset --force --yes") {
		t.Fatalf("incompatible environment response code=%d body=%s", incompatible.Code, incompatible.Body.String())
	}
	recoveryPlan := request(server, authManager, http.MethodGet, "/api/v1/runtime/reset", "", true)
	if recoveryPlan.Code != http.StatusOK || !strings.Contains(recoveryPlan.Body.String(), `"topologyIncompatible":true`) || !strings.Contains(recoveryPlan.Body.String(), `"environments":1`) {
		t.Fatalf("incompatible-state reset plan response code=%d body=%s", recoveryPlan.Code, recoveryPlan.Body.String())
	}
}

type fakeDaemonControl struct {
	identity          contract.DaemonStatus
	logs              contract.DaemonLogSnapshot
	logsErr           error
	logCalls          int
	handoff           contract.DaemonHandoffStatus
	handoffCalls      int
	restartedInstance string
}

func (f *fakeDaemonControl) Status(context.Context) (contract.DaemonStatus, error) {
	return f.identity, nil
}

func (f *fakeDaemonControl) Logs(context.Context) (contract.DaemonLogSnapshot, error) {
	f.logCalls++
	return f.logs, f.logsErr
}

func (f *fakeDaemonControl) HandoffStatus(context.Context) (contract.DaemonHandoffStatus, error) {
	f.handoffCalls++
	return f.handoff, nil
}

func (f *fakeDaemonControl) Restart(_ context.Context, instanceID string) (contract.DaemonRestart, error) {
	if instanceID != f.identity.InstanceID {
		return contract.DaemonRestart{}, &contract.DaemonControlError{Code: "DAEMON_INSTANCE_CHANGED", Message: "daemon instance changed"}
	}
	f.restartedInstance = instanceID
	return contract.DaemonRestart{Restarting: true, PreviousInstanceID: instanceID, Handoff: true, ActiveEnvironments: append([]string(nil), f.identity.ActiveEnvironments...)}, nil
}

func TestDirectorySelectionRequiresABrowserSessionAndHandlesCancellation(t *testing.T) {
	data := t.TempDir()
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>portless</html>"), Mode: fs.FileMode(0o600)}}
	selectedPath := t.TempDir()
	var selectedPrompt string
	var selectedInitialPath string
	server, err := New(Dependencies{
		Auth: authManager, Assets: assets,
		SelectDirectory: func(_ context.Context, prompt, initialPath string) (string, bool, error) {
			selectedPrompt = prompt
			selectedInitialPath = initialPath
			return selectedPath, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	nativeClient := request(server, authManager, http.MethodPost, "/api/v1/system/directories/select", `{"initialPath":"/workspace/checkout"}`, true)
	if nativeClient.Code != http.StatusForbidden || !strings.Contains(nativeClient.Body.String(), `"code":"BROWSER_SESSION_REQUIRED"`) {
		t.Fatalf("native client directory selection code=%d body=%s", nativeClient.Code, nativeClient.Body.String())
	}
	claim, _, err := authManager.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, csrf, _, _, err := authManager.ConsumeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	withoutCSRF := requestBrowser(server, http.MethodPost, "/api/v1/system/directories/select", `{"initialPath":"/workspace/checkout"}`, sessionToken, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("browser directory selection without CSRF code=%d body=%s", withoutCSRF.Code, withoutCSRF.Body.String())
	}
	selected := requestBrowser(server, http.MethodPost, "/api/v1/system/directories/select", `{"initialPath":"/workspace/checkout"}`, sessionToken, csrf)
	if selected.Code != http.StatusOK || !strings.Contains(selected.Body.String(), strconv.Quote(selectedPath)) {
		t.Fatalf("browser directory selection code=%d body=%s", selected.Code, selected.Body.String())
	}
	if selectedPrompt != "Choose a Portless source directory" || selectedInitialPath != "/workspace/checkout" {
		t.Fatalf("picker prompt=%q initialPath=%q", selectedPrompt, selectedInitialPath)
	}

	server.selectDirectory = func(context.Context, string, string) (string, bool, error) {
		return "", true, nil
	}
	canceled := requestBrowser(server, http.MethodPost, "/api/v1/system/directories/select", `{"initialPath":""}`, sessionToken, csrf)
	if canceled.Code != http.StatusNoContent || canceled.Body.Len() != 0 {
		t.Fatalf("canceled directory selection code=%d body=%s", canceled.Code, canceled.Body.String())
	}

	server.selectDirectory = nil
	unavailable := requestBrowser(server, http.MethodPost, "/api/v1/system/directories/select", `{"initialPath":""}`, sessionToken, csrf)
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"DIRECTORY_PICKER_UNAVAILABLE"`) {
		t.Fatalf("unavailable directory selection code=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
}

func TestApplicationHostRequiresServiceEnvironmentProject(t *testing.T) {
	if service, environment, project, ok := applicationHost("checkout.local.billing.localhost"); !ok || service != "checkout" || environment != "local" || project != "billing" {
		t.Fatalf("canonical host parsed as %q %q %q %v", service, environment, project, ok)
	}
	if _, _, _, ok := applicationHost("checkout.billing.localhost"); ok {
		t.Fatal("two-label application host should not be accepted")
	}
}

func TestTrafficSummaryOmitsDetailContentWithoutMutatingDetail(t *testing.T) {
	detail := model.TrafficExchange{TraceContextSource: model.TrafficTraceContextGenerated, RequestHeaders: map[string][]string{"Authorization": {"Bearer local"}}, ResponseHeaders: map[string][]string{"Set-Cookie": {"session=local"}}, RequestBody: `{"request":true}`, ResponseBody: `{"response":true}`, RequestBodyTruncated: true, ResponseBodyTruncated: true,
		TCP: &model.TrafficTCPExchange{Kind: model.TrafficTCPKindOperation, Operation: "GET", Inspection: model.TrafficInspectionDecoded, RequestMessages: []model.TrafficMessage{{Type: "command", Content: `["GET","key"]`}}}}
	summary := trafficSummary(detail)
	if summary.TraceContextSource != model.TrafficTraceContextGenerated || summary.RequestHeaders != nil || summary.ResponseHeaders != nil || summary.RequestBody != "" || summary.ResponseBody != "" || summary.RequestBodyTruncated || summary.ResponseBodyTruncated || summary.TCP == nil || summary.TCP.Operation != "GET" || summary.TCP.RequestMessages != nil {
		t.Fatalf("summary retained detail content: %#v", summary)
	}
	if len(detail.RequestHeaders) != 1 || len(detail.ResponseHeaders) != 1 || detail.RequestBody == "" || detail.ResponseBody == "" || !detail.RequestBodyTruncated || !detail.ResponseBodyTruncated || len(detail.TCP.RequestMessages) != 1 {
		t.Fatalf("summary mutated detail: %#v", detail)
	}
}

func TestEnvironmentContextSelectionCanBeInspectedAndCleared(t *testing.T) {
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(context.Background(), "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if _, err := controlStore.CreateEnvironment(context.Background(), "billing", "local", definition,
		[]model.SourceBinding{{Name: "checkout", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	app := controlplane.New(controlStore, events.NewBroker(), controlplane.Config{DataDirectory: data, InstallationKey: "test-installation"})
	defer app.Close(context.Background())
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>portless</html>"), Mode: fs.FileMode(0o600)}}
	server, err := New(Dependencies{Application: app, Auth: authManager, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}

	contextPath := "/api/v1/environments/context?path=" + url.QueryEscape(source)
	resolved := request(server, authManager, http.MethodGet, contextPath, "", true)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"resolution":"inferred"`) || !strings.Contains(resolved.Body.String(), `"name":"local"`) {
		t.Fatalf("inferred context response code=%d body=%s", resolved.Code, resolved.Body.String())
	}
	selected := request(server, authManager, http.MethodPut, "/api/v1/environments/select", `{"path":"`+source+`","project":"billing","environment":"local"}`, true)
	if selected.Code != http.StatusNoContent {
		t.Fatalf("select response code=%d body=%s", selected.Code, selected.Body.String())
	}
	resolved = request(server, authManager, http.MethodGet, contextPath, "", true)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"resolution":"selected"`) {
		t.Fatalf("selected context response code=%d body=%s", resolved.Code, resolved.Body.String())
	}
	cleared := request(server, authManager, http.MethodDelete, "/api/v1/environments/select?path="+url.QueryEscape(source), "", true)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"cleared":true`) {
		t.Fatalf("clear response code=%d body=%s", cleared.Code, cleared.Body.String())
	}
	cleared = request(server, authManager, http.MethodDelete, "/api/v1/environments/select?path="+url.QueryEscape(source), "", true)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"cleared":false`) {
		t.Fatalf("idempotent clear response code=%d body=%s", cleared.Code, cleared.Body.String())
	}
}

func request(server *Server, authManager *auth.Manager, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	return requestHost(server, authManager, method, path, body, authenticated, "localhost:7331")
}

func requestClientKind(server *Server, authManager *auth.Manager, method, path, body, clientKind string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://localhost:7331"+path, strings.NewReader(body))
	request.Host = "localhost:7331"
	request.Header.Set("Authorization", "Bearer "+authManager.Token())
	request.Header.Set(contract.ClientKindHeader, clientKind)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func requestHost(server *Server, authManager *auth.Manager, method, path, body string, authenticated bool, host string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	request.Host = host
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+authManager.Token())
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func requestBrowser(server *Server, method, path, body, sessionToken, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://portless.localhost"+path, strings.NewReader(body))
	request.Host = "portless.localhost"
	request.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionToken})
	request.Header.Set("Origin", "http://portless.localhost")
	if csrf != "" {
		request.Header.Set("X-Portless-CSRF", csrf)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}
