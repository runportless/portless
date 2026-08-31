package golang

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestGoHealthDetectionRecognizesStaticRouterRegistrations(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", `package main
import (
  "net/http"
  "github.com/go-chi/chi/v5"
)
func main() {
  router := chi.NewRouter()
  router.Get("/ready", handler)
  _ = http.ListenAndServe(":8080", router)
}
func handler(http.ResponseWriter, *http.Request) {}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	detection := detectGoHealth([]goSource{{file: "main.go", parsed: parsed}})
	if detection.path != "/ready" || detection.evidence == nil {
		t.Fatalf("detection = %#v", detection)
	}
}

func TestGoHealthDetectionRejectsClientAndDynamicPaths(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", `package main
import "net/http"
func main() {
  client := &http.Client{}
  _, _ = client.Get("/health")
  mux := http.NewServeMux()
  route := "/ready"
  mux.HandleFunc(route, handler)
}
func handler(http.ResponseWriter, *http.Request) {}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if detection := detectGoHealth([]goSource{{file: "main.go", parsed: parsed}}); detection.path != "" {
		t.Fatalf("unproven route was selected: %#v", detection)
	}
}

func TestGoHealthDetectionSupportsMethodQualifiedServeMuxPatterns(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", `package main
import "net/http"
func main() { http.HandleFunc("GET /health", handler) }
func handler(http.ResponseWriter, *http.Request) {}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if detection := detectGoHealth([]goSource{{file: "main.go", parsed: parsed}}); detection.path != "/health" {
		t.Fatalf("detection = %#v", detection)
	}
}
