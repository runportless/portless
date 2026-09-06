package contract

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestTrafficReplayOpenAPIContractResolves(t *testing.T) {
	content, err := os.ReadFile("../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("OpenAPI is not valid YAML: %v", err)
	}
	if version := document["info"].(map[string]any)["version"]; version != APIVersion {
		t.Fatalf("OpenAPI version %v differs from API version %s", version, APIVersion)
	}
	paths := document["paths"].(map[string]any)
	base := "/environments/{projectName}/{environmentName}/traffic/replays"
	for path, methods := range map[string][]string{base: {"post"}, base + "/{replayNumber}": {"get", "delete"}, base + "/{replayNumber}/activity": {"post"}, base + "/{replayNumber}/draft": {"put"}, base + "/{replayNumber}/runs": {"post"}} {
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("replay path %s is undocumented", path)
			continue
		}
		for _, method := range methods {
			if item[method] == nil {
				t.Errorf("replay operation %s %s is undocumented", method, path)
			}
		}
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key != "$ref" {
					walk(child)
					continue
				}
				reference, ok := child.(string)
				if !ok || !strings.HasPrefix(reference, "#/") {
					continue
				}
				var target any = document
				for _, segment := range strings.Split(reference[2:], "/") {
					object, ok := target.(map[string]any)
					if !ok {
						target = nil
						break
					}
					target = object[strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")]
				}
				if target == nil {
					t.Errorf("unresolved OpenAPI reference %s", reference)
				}
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(document)
}
