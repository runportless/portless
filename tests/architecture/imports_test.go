package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/runportless/portless"
	cliRoot    = modulePath + "/portless-cli"
	daemonRoot = modulePath + "/portless-daemon"
	mcpRoot    = modulePath + "/portless-mcp"
	relayRoot  = modulePath + "/portless-relay"
)

func TestProductDependencyDirection(t *testing.T) {
	root := repositoryRoot(t)
	for _, product := range []string{"portless-cli", "portless-daemon", "portless-mcp", "portless-relay", "portless-web"} {
		walkGoFiles(t, filepath.Join(root, product), func(path, packagePath string, parsed *ast.File) {
			for _, spec := range parsed.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Errorf("decode import in %s: %v", relativePath(root, path), err)
					continue
				}
				if reason := forbiddenProductImport(packagePath, importPath); reason != "" {
					t.Errorf("%s imports %s: %s", relativePath(root, path), importPath, reason)
				}
			}
		})
	}
}

func TestProductRootsReplaceGenericInternalTree(t *testing.T) {
	root := repositoryRoot(t)
	for _, directory := range []string{"portless-cli", "portless-daemon", "portless-mcp", "portless-relay", "portless-web"} {
		info, err := os.Stat(filepath.Join(root, directory))
		if err != nil {
			t.Errorf("product root %s is missing: %v", directory, err)
		} else if !info.IsDir() {
			t.Errorf("product root %s is not a directory", directory)
		}
	}
	for _, directory := range []string{"internal", "api", "cmd", "web", "webui"} {
		path := filepath.Join(root, directory)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("obsolete top-level implementation directory still exists: %s", directory)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestProductPackagesUseOwnershipNames(t *testing.T) {
	root := repositoryRoot(t)
	retired := map[string]string{
		"application": "controlplane",
		"store":       "database",
		"resource":    "providers",
		"instance":    "identity",
		"diagnostics": "doctor",
	}
	for _, product := range []string{"portless-cli", "portless-daemon", "portless-mcp", "portless-relay"} {
		walkGoFiles(t, filepath.Join(root, product), func(path, _ string, parsed *ast.File) {
			replacement, obsolete := retired[parsed.Name.Name]
			if obsolete {
				t.Errorf("%s still declares package %s; use the product ownership name %s", relativePath(root, path), parsed.Name.Name, replacement)
			}
		})
	}
}

func TestCLIRootIsOnlyComposition(t *testing.T) {
	root := repositoryRoot(t)
	cliDirectory := filepath.Join(root, "portless-cli")
	requiredPackages := []string{"administration", "cmd", "command", "doctor", "environment", "observe", "projects", "traffic"}
	for _, name := range requiredPackages {
		info, err := os.Stat(filepath.Join(cliDirectory, name))
		if err != nil || !info.IsDir() {
			t.Errorf("CLI ownership package %s is missing or is not a directory", name)
		}
	}

	entries, err := os.ReadDir(cliDirectory)
	if err != nil {
		t.Fatal(err)
	}
	allowedProduction := map[string]bool{"app.go": true, "commands.go": true}
	allowedTests := map[string]bool{
		"cli_test.go":              true,
		"command_contract_test.go": true,
		"dependencies_test.go":     true,
		"execution_test.go":        true,
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			if !allowedTests[entry.Name()] {
				t.Errorf("portless-cli/%s tests feature behavior from the composition root; move it to the owning CLI package", entry.Name())
			}
			continue
		}
		if !allowedProduction[entry.Name()] {
			t.Errorf("portless-cli/%s is feature implementation in the composition root; move it to the owning CLI package", entry.Name())
		}
	}
}

func TestProductPublicDeclarationsHaveGoDoc(t *testing.T) {
	root := repositoryRoot(t)
	for _, product := range []string{"portless-cli", "portless-daemon", "portless-mcp", "portless-relay"} {
		walkGoFiles(t, filepath.Join(root, product), func(path, _ string, parsed *ast.File) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			for _, declaration := range parsed.Decls {
				switch value := declaration.(type) {
				case *ast.FuncDecl:
					if value.Name.IsExported() && !goDocStartsWith(value.Doc, value.Name.Name) {
						t.Errorf("%s exports %s without a GoDoc comment beginning with its name", relativePath(root, path), value.Name.Name)
					}
				case *ast.GenDecl:
					for _, item := range value.Specs {
						switch spec := item.(type) {
						case *ast.TypeSpec:
							if spec.Name.IsExported() && !goDocStartsWith(firstGoDoc(spec.Doc, value.Doc), spec.Name.Name) {
								t.Errorf("%s exports %s without a GoDoc comment beginning with its name", relativePath(root, path), spec.Name.Name)
							}
							contract, ok := spec.Type.(*ast.InterfaceType)
							if !ok || !spec.Name.IsExported() {
								continue
							}
							for _, field := range contract.Methods.List {
								for _, name := range field.Names {
									if name.IsExported() && !goDocStartsWith(firstGoDoc(field.Doc, field.Comment), name.Name) {
										t.Errorf("%s exports interface method %s without a GoDoc comment beginning with its name", relativePath(root, path), name.Name)
									}
								}
							}
						case *ast.ValueSpec:
							for _, name := range spec.Names {
								if name.IsExported() && !goDocStartsWith(firstGoDoc(spec.Doc, value.Doc), name.Name) {
									t.Errorf("%s exports %s without a GoDoc comment beginning with its name", relativePath(root, path), name.Name)
								}
							}
						}
					}
				}
			}
		})
	}
}

func TestCLIFeaturesDependOnSharedCommandPrimitivesNotEachOther(t *testing.T) {
	root := repositoryRoot(t)
	features := []string{"administration", "environment", "observe", "projects", "traffic"}
	for _, feature := range features {
		directory := filepath.Join(root, "portless-cli", feature)
		walkGoFiles(t, directory, func(path, _ string, parsed *ast.File) {
			for _, spec := range parsed.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(importPath, cliRoot+"/") {
					continue
				}
				if importPath == cliRoot+"/command" || feature == "administration" && importPath == cliRoot+"/doctor" {
					continue
				}
				t.Errorf("%s imports sibling CLI feature %s; communicate through the shared command context or daemon API", relativePath(root, path), importPath)
			}
		})
	}
}

func TestAPIClientTransportIsPrivate(t *testing.T) {
	root := repositoryRoot(t)
	walkGoFiles(t, filepath.Join(root, "portless-daemon", "api", "client"), func(_ string, _ string, parsed *ast.File) {
		for _, declaration := range parsed.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				for _, item := range value.Specs {
					typeSpec, ok := item.(*ast.TypeSpec)
					if !ok || typeSpec.Name.Name != "Client" {
						continue
					}
					structure, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						t.Errorf("daemon api client Client is not a struct")
						continue
					}
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							if ast.IsExported(name.Name) {
								t.Errorf("daemon api client exposes transport field %s", name.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if value.Recv != nil && (value.Name.Name == "Do" || value.Name.Name == "DoWithHeaders") {
					t.Errorf("daemon api client exposes raw transport method %s", value.Name.Name)
				}
			}
		}
	})
}

func TestCLIUsesTypedDaemonAPI(t *testing.T) {
	root := repositoryRoot(t)
	walkGoFiles(t, filepath.Join(root, "portless-cli"), func(path, _ string, _ *ast.File) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "/api/v1") {
			t.Errorf("%s constructs a daemon API route; add a typed daemon API client method", relativePath(root, path))
		}
		if strings.Contains(string(content), ".Do(") || strings.Contains(string(content), ".DoWithHeaders(") {
			t.Errorf("%s calls raw daemon API transport", relativePath(root, path))
		}
	})
}

func TestRetiredRelayIdentifiersStayRemoved(t *testing.T) {
	root := repositoryRoot(t)
	legacy := []string{"__ingress", "__install-ingress", "__restart-ingress", "__uninstall-ingress", "dev.portless.ingress", "portless-ingress"}
	for _, directory := range []string{filepath.Join(root, "portless-cli", "cmd"), filepath.Join(root, "portless-relay")} {
		walkGoFiles(t, directory, func(path, _ string, _ *ast.File) {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range legacy {
				if strings.Contains(string(content), value) {
					t.Errorf("%s contains retired relay identifier %q", relativePath(root, path), value)
				}
			}
		})
	}
}

func forbiddenProductImport(source, target string) string {
	if target == modulePath+"/internal" || strings.HasPrefix(target, modulePath+"/internal/") {
		return "the generic internal package tree has been retired"
	}

	contract := daemonRoot + "/api/contract"
	client := daemonRoot + "/api/client"
	server := daemonRoot + "/api/server"
	control := daemonRoot + "/control"
	controlplane := daemonRoot + "/controlplane"
	database := daemonRoot + "/database"
	installation := daemonRoot + "/system/installation"
	mcpSDK := "github.com/modelcontextprotocol/go-sdk"

	if matchesAny(target, mcpRoot) && source != cliRoot+"/administration" && !matchesAny(source, mcpRoot) {
		return "the MCP product is consumed only by the CLI administration adapter"
	}
	if matchesAny(source, mcpRoot) && strings.HasPrefix(target, modulePath+"/") && target != client && target != contract {
		return "the MCP product may depend only on the typed daemon API client and contract"
	}
	if matchesAny(target, mcpSDK) && !matchesAny(source, mcpRoot) {
		return "the official MCP SDK is confined to the portless-mcp product"
	}

	if source == contract && strings.HasPrefix(target, modulePath+"/") && target != daemonRoot+"/model" {
		return "daemon API contracts may depend only on stable daemon model values"
	}
	if source == client && strings.HasPrefix(target, modulePath+"/") && target != contract {
		return "the daemon API client may depend only on the API contract"
	}
	if source == server && matchesAny(target, cliRoot, control, relayRoot) {
		return "the daemon API server receives process and relay control through injected interfaces"
	}
	if source == control && (matchesAny(target, server, controlplane, database) || target == daemonRoot) {
		return "daemon lifecycle control cannot depend on the running control plane"
	}
	if source == daemonRoot && matchesAny(target, control) {
		return "daemon composition cannot depend on client-side daemon control"
	}
	if strings.HasPrefix(source, relayRoot) && matchesAny(target, cliRoot, server, controlplane, database) {
		return "the relay cannot depend on CLI or daemon control-plane implementations"
	}
	if strings.HasPrefix(source, cliRoot) && matchesAny(target, server, controlplane, database) {
		return "the CLI must use the daemon API instead of daemon feature implementations"
	}
	if source == installation && (strings.HasPrefix(target, modulePath+"/") || isThirdPartyImport(target)) {
		return "daemon installation safety primitives use only the Go standard library"
	}
	if isDaemonFeature(source) && (matchesAny(target, cliRoot, server, relayRoot) || target == daemonRoot) {
		return "daemon feature packages cannot depend on process adapters"
	}
	if strings.HasPrefix(source, cliRoot+"/cmd/") && strings.HasPrefix(target, modulePath+"/") && target != cliRoot && target != daemonRoot && target != relayRoot {
		return "the executable entry point may import only product facades"
	}
	return ""
}

func isDaemonFeature(path string) bool {
	for _, area := range []string{"controlplane", "projects", "traffic", "providers", "runtime", "database", "networking", "dns", "events"} {
		prefix := daemonRoot + "/" + area
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func isThirdPartyImport(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return strings.Contains(first, ".")
}

func matchesAny(path string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func firstGoDoc(primary, fallback *ast.CommentGroup) *ast.CommentGroup {
	if primary != nil {
		return primary
	}
	return fallback
}

func goDocStartsWith(documentation *ast.CommentGroup, name string) bool {
	return documentation != nil && strings.HasPrefix(strings.TrimSpace(documentation.Text()), name+" ")
}

func walkGoFiles(t *testing.T, directory string, visit func(path, packagePath string, parsed *ast.File)) {
	t.Helper()
	root := repositoryRoot(t)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		visit(path, modulePath+"/"+filepath.ToSlash(relative), parsed)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
