package mocks

import (
	"strings"
	"testing"
)

func TestRoutesFromOpenAPIImportsExamplesAndRequiredQuery(t *testing.T) {
	document := []byte(`
openapi: 3.1.0
paths:
  /inventory/{sku}:
    get:
      operationId: getInventory
      parameters:
        - name: warehouse
          in: query
          required: true
          schema:
            type: string
            default: central
      responses:
        "200":
          $ref: "#/components/responses/Inventory"
components:
  responses:
    Inventory:
      description: inventory response
      content:
        application/json:
          example:
            available: false
            quantity: 0
`)
	routes, warnings, err := RoutesFromOpenAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(routes) != 1 {
		t.Fatalf("routes = %#v warnings = %#v", routes, warnings)
	}
	route := routes[0]
	if route.Name != "getinventory" || route.Method != "GET" || route.Path != "/inventory/{sku}" || route.Query["warehouse"] != "central" || route.Status != 200 || route.Headers["Content-Type"] != "application/json" || route.Body != `{"available":false,"quantity":0}` {
		t.Fatalf("route = %#v", route)
	}
}

func TestRoutesFromOpenAPIRejectsExternalReferences(t *testing.T) {
	_, _, err := RoutesFromOpenAPI([]byte(`{"openapi":"3.0.3","paths":{"/inventory":{"$ref":"https://example.test/path.yaml"}}}`))
	if err == nil || !strings.Contains(err.Error(), "external reference") {
		t.Fatalf("error = %v", err)
	}
}
