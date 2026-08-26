//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

const storeExampleE2EEnvironment = "PORTLESS_STORE_EXAMPLE_E2E"

type storeOrder struct {
	ID        int    `json:"id"`
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
}

type storeInventoryItem struct {
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Requested int    `json:"requested,omitempty"`
	OnHand    int    `json:"onHand"`
	Available bool   `json:"available,omitempty"`
	Warehouse string `json:"warehouse"`
}

type storeInventoryReservation struct {
	ID       int64  `json:"id"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
	State    string `json:"state"`
}

func TestStoreExampleEndToEnd(t *testing.T) {
	if os.Getenv(storeExampleE2EEnvironment) != "1" {
		t.Skip(storeExampleE2EEnvironment + "=1 is required for the dependency-backed Store example")
	}
	binary := e2eBinary(t)
	home, checkout := storeExampleFixture(t)
	defer cleanupInstallation(t, binary, home, checkout)
	selectRequestedRuntime(t, binary, home, checkout)

	const selector = "store-example/local"
	upOutput, err := runCLIAt(binary, home, checkout, "up", "--name", "store-example", "--managed", "--no-open", "--timeout", "4m")
	if err != nil {
		t.Fatalf("start Store example: %v\n%s\ndaemon log:\n%s", err, upOutput, readDaemonLog(home))
	}
	environment := explicitEnvironmentStatus(t, binary, home, checkout, selector)
	if environment.Status != model.EnvironmentHealthy || environment.PrimaryService != "checkout" || len(environment.Services) != 6 {
		t.Fatalf("Store environment is not healthy: %#v", environment)
	}
	for _, service := range environment.Services {
		if service.Status != model.ServiceReady {
			t.Fatalf("Store service %s is %s: %#v", service.Name, service.Status, service)
		}
	}

	const host = "checkout.local.store-example.localhost"
	const inventoryHost = "inventory.local.store-example.localhost"
	waitForStoreReady(t, home, host)
	assertStoreCheckoutPage(t, home, host)
	beforeInventory := storeInventoryLookup(t, home, inventoryHost, "coffee-mug", 2)
	if beforeInventory.OnHand != 24 || !beforeInventory.Available {
		t.Fatalf("initial Store inventory = %#v", beforeInventory)
	}
	traceHeaders := map[string]string{
		"content-type": "application/json",
		"traceparent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	createdBody := assertStoreJSON(t, home, host, http.MethodPost, "/checkout", `{"sku":"coffee-mug","quantity":2}`, traceHeaders, http.StatusCreated)
	var created struct {
		Checkout    string                    `json:"checkout"`
		Inventory   storeInventoryItem        `json:"inventory"`
		Reservation storeInventoryReservation `json:"reservation"`
		Order       storeOrder                `json:"order"`
	}
	if err := json.Unmarshal(createdBody, &created); err != nil || created.Checkout != "accepted" || created.Order.ID < 1 || created.Order.SKU != "coffee-mug" || created.Order.Quantity != 2 || created.Order.State != "created" || created.Order.CreatedAt == "" || created.Reservation.ID < 1 || created.Reservation.State != "reserved" || created.Inventory.OnHand != 22 {
		t.Fatalf("decode persisted Store order: err=%v response=%#v body=%s", err, created, createdBody)
	}
	afterReservation := storeInventoryLookup(t, home, inventoryHost, "coffee-mug", 2)
	if afterReservation.OnHand != 22 || !afterReservation.Available {
		t.Fatalf("reserved Store inventory = %#v", afterReservation)
	}

	path := "/orders/" + strconv.Itoa(created.Order.ID)
	first := storeOrderLookup(t, home, host, path)
	if first.Cache != "miss" || first.Order != created.Order {
		t.Fatalf("first Store lookup = %#v, want PostgreSQL-backed cache miss for %#v", first, created.Order)
	}
	second := storeOrderLookup(t, home, host, path)
	if second.Cache != "hit" || second.Order != created.Order {
		t.Fatalf("second Store lookup = %#v, want Redis hit for %#v", second, created.Order)
	}

	assertStoreProtocolTraffic(t, binary, home, checkout, selector, created.Order.ID)

	redis := requireService(t, environment, "orders-redis")
	if deleted := valkeyCommand(t, managedResourceProbeAddress(t, redis), "DEL", fmt.Sprintf("store:order:%d", created.Order.ID)); deleted != "1" {
		t.Fatalf("clear Store order cache = %q", deleted)
	}
	if output, err := runCLIAt(binary, home, checkout, "--env", selector, "service", "restart", "orders", "--timeout", "2m"); err != nil {
		t.Fatalf("restart Store orders service: %v\n%s", err, output)
	}
	waitForStoreReady(t, home, host)
	afterProcessRestart := storeOrderLookup(t, home, host, path)
	if afterProcessRestart.Cache != "miss" || afterProcessRestart.Order != created.Order {
		t.Fatalf("Store order did not survive process restart: %#v", afterProcessRestart)
	}
	if output, err := runCLIAt(binary, home, checkout, "--env", selector, "service", "restart", "inventory", "--timeout", "2m"); err != nil {
		t.Fatalf("restart Store inventory service: %v\n%s", err, output)
	}
	waitForStoreReady(t, home, host)
	afterInventoryRestart := storeInventoryLookup(t, home, inventoryHost, "coffee-mug", 2)
	if afterInventoryRestart.OnHand != 22 {
		t.Fatalf("Store inventory did not survive process restart: %#v", afterInventoryRestart)
	}

	if output, err := runCLIAt(binary, home, checkout, "--env", selector, "down", "--timeout", "3m"); err != nil {
		t.Fatalf("stop Store before persistence check: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "--env", selector, "up", "--managed", "--no-open", "--timeout", "4m"); err != nil {
		t.Fatalf("restart Store for persistence check: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	waitForStoreReady(t, home, host)
	environment = explicitEnvironmentStatus(t, binary, home, checkout, selector)
	redis = requireService(t, environment, "orders-redis")
	valkeyCommand(t, managedResourceProbeAddress(t, redis), "DEL", fmt.Sprintf("store:order:%d", created.Order.ID))
	afterEnvironmentRestart := storeOrderLookup(t, home, host, path)
	if afterEnvironmentRestart.Cache != "miss" || afterEnvironmentRestart.Order != created.Order {
		t.Fatalf("Store order did not survive ordinary down/up: %#v", afterEnvironmentRestart)
	}
	afterEnvironmentInventory := storeInventoryLookup(t, home, inventoryHost, "coffee-mug", 2)
	if afterEnvironmentInventory.OnHand != 22 {
		t.Fatalf("Store inventory did not survive ordinary down/up: %#v", afterEnvironmentInventory)
	}
}

func assertStoreProtocolTraffic(t *testing.T, binary, home, checkout, selector string, orderID int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastOutput string
	for time.Now().Before(deadline) {
		output, err := runCLIAt(binary, home, checkout, "--env", selector, "--json", "traffic", "list", "--protocol", "tcp", "--limit", "300")
		lastOutput = output
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var traffic struct {
			Exchanges []model.TrafficExchange `json:"exchanges"`
		}
		if json.Unmarshal([]byte(output), &traffic) != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		inventoryUpdate := findStoreProtocolExchange(traffic.Exchanges, "inventory", "inventory-postgres", model.ApplicationProtocolPostgreSQL, "UPDATE")
		insert := findStoreProtocolExchange(traffic.Exchanges, "orders", "orders-postgres", model.ApplicationProtocolPostgreSQL, "INSERT")
		selectOperation := findStoreProtocolExchange(traffic.Exchanges, "orders", "orders-postgres", model.ApplicationProtocolPostgreSQL, "SELECT")
		get := findStoreProtocolExchange(traffic.Exchanges, "orders", "orders-redis", model.ApplicationProtocolRedis, "GET")
		set := findStoreProtocolExchange(traffic.Exchanges, "orders", "orders-redis", model.ApplicationProtocolRedis, "SET")
		if inventoryUpdate == nil || insert == nil || selectOperation == nil || get == nil || set == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		inventoryUpdateDetail := storeTrafficDetail(t, binary, home, checkout, selector, inventoryUpdate.Sequence)
		insertDetail := storeTrafficDetail(t, binary, home, checkout, selector, insert.Sequence)
		selectDetail := storeTrafficDetail(t, binary, home, checkout, selector, selectOperation.Sequence)
		getDetail := storeTrafficDetail(t, binary, home, checkout, selector, get.Sequence)
		setDetail := storeTrafficDetail(t, binary, home, checkout, selector, set.Sequence)
		if !storeTrafficContains(inventoryUpdateDetail, "UPDATE store_inventory") {
			t.Fatalf("Store inventory PostgreSQL UPDATE detail = %#v", inventoryUpdateDetail)
		}
		if !storeTrafficContains(insertDetail, "INSERT INTO store_orders") {
			t.Fatalf("Store PostgreSQL INSERT detail = %#v", insertDetail)
		}
		if !storeTrafficContains(selectDetail, "SELECT id, sku, quantity, state, created_at") {
			t.Fatalf("Store PostgreSQL SELECT detail = %#v", selectDetail)
		}
		cacheKey := fmt.Sprintf("store:order:%d", orderID)
		if !storeTrafficContains(getDetail, cacheKey) || !storeTrafficContains(setDetail, cacheKey) {
			t.Fatalf("Store Redis details do not contain %q: GET=%#v SET=%#v", cacheKey, getDetail, setDetail)
		}
		for _, exchange := range []model.TrafficExchange{inventoryUpdateDetail, insertDetail, selectDetail, getDetail, setDetail} {
			if exchange.TCP == nil || exchange.TCP.Inspection != model.TrafficInspectionDecoded || exchange.TCP.Outcome != model.TrafficTCPOutcomeSuccess {
				t.Fatalf("Store protocol operation is not decoded successfully: %#v", exchange)
			}
		}
		return
	}
	t.Fatalf("Store protocol operations were not captured; last response:\n%s", lastOutput)
}

func findStoreProtocolExchange(exchanges []model.TrafficExchange, source, target string, applicationProtocol model.ApplicationProtocol, operation string) *model.TrafficExchange {
	for index := range exchanges {
		exchange := &exchanges[index]
		if exchange.Source == source && exchange.Target == target && exchange.TCP != nil && exchange.TCP.Kind == model.TrafficTCPKindOperation && exchange.TCP.ApplicationProtocol == applicationProtocol && exchange.TCP.Operation == operation {
			return exchange
		}
	}
	return nil
}

func storeInventoryLookup(t *testing.T, home, host, sku string, quantity int) storeInventoryItem {
	t.Helper()
	body := assertStoreJSON(t, home, host, http.MethodGet, "/inventory/"+sku+"?quantity="+strconv.Itoa(quantity), "", nil, http.StatusOK)
	var inventory storeInventoryItem
	if err := json.Unmarshal(body, &inventory); err != nil {
		t.Fatalf("decode Store inventory lookup: %v\n%s", err, body)
	}
	return inventory
}

func storeTrafficDetail(t *testing.T, binary, home, checkout, selector string, sequence int64) model.TrafficExchange {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--env", selector, "--json", "traffic", "show", strconv.FormatInt(sequence, 10))
	if err != nil {
		t.Fatalf("show Store protocol exchange %d: %v\n%s", sequence, err, output)
	}
	var exchange model.TrafficExchange
	if err := json.Unmarshal([]byte(output), &exchange); err != nil {
		t.Fatalf("decode Store protocol exchange %d: %v\n%s", sequence, err, output)
	}
	return exchange
}

func storeTrafficContains(exchange model.TrafficExchange, expected string) bool {
	if exchange.TCP == nil {
		return false
	}
	for _, message := range append(append([]model.TrafficMessage(nil), exchange.TCP.RequestMessages...), exchange.TCP.ResponseMessages...) {
		if strings.Contains(message.Content, expected) || strings.Contains(message.Summary, expected) {
			return true
		}
		for _, field := range message.Fields {
			if strings.Contains(field.Value, expected) {
				return true
			}
		}
	}
	return false
}

func storeOrderLookup(t *testing.T, home, host, path string) struct {
	Order storeOrder `json:"order"`
	Cache string     `json:"cache"`
} {
	t.Helper()
	body := assertStoreJSON(t, home, host, http.MethodGet, path, "", map[string]string{
		"traceparent": "00-7bf92f3577b34da6a3ce929d0e0e4736-10f067aa0ba902b7-01",
	}, http.StatusOK)
	var result struct {
		Order storeOrder `json:"order"`
		Cache string     `json:"cache"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode Store order lookup: %v\n%s", err, body)
	}
	return result
}

func assertStoreJSON(t *testing.T, home, host, method, path, body string, headers map[string]string, status int) []byte {
	t.Helper()
	response := applicationRequestWithMethod(t, home, host, method, path, body, headers)
	encoded, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("Store %s %s = %s, want %d\n%s", method, path, response.Status, status, encoded)
	}
	return encoded
}

func assertStoreCheckoutPage(t *testing.T, home, host string) {
	t.Helper()
	response := applicationRequest(t, home, host, "/", nil)
	encoded, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("content-type"), "text/html;") || !strings.Contains(string(encoded), `<form id="checkout-form">`) || !strings.Contains(string(encoded), `src="/checkout.js"`) {
		t.Fatalf("Store checkout page = %s content-type=%q\n%s", response.Status, response.Header.Get("content-type"), encoded)
	}
}

func waitForStoreReady(t *testing.T, home, host string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastStatus string
	var lastBody []byte
	for time.Now().Before(deadline) {
		response := applicationRequest(t, home, host, "/health", nil)
		encoded, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		lastStatus, lastBody = response.Status, encoded
		if response.StatusCode == http.StatusOK && strings.Contains(string(encoded), `"service":"checkout"`) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Store checkout did not become HTTP-ready: %s\n%s", lastStatus, lastBody)
}

func storeExampleFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "portless-store-example-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	checkout, err := filepath.EvalSymlinks(e2eRepositoryPath(t, "examples", "store"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return home, checkout
}
