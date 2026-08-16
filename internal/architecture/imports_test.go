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

const modulePath = "github.com/portless-run/portless"

func TestInternalDependencyDirection(t *testing.T) {
	root := repositoryRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		packagePath := modulePath + "/" + filepath.ToSlash(relative)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			return nil
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Errorf("decode import in %s: %v", path, err)
				continue
			}
			if packagePath == modulePath+"/internal/cli" && matchesAny(importPath, modulePath+"/internal/project/discovery") && filepath.Base(path) != "app.go" {
				t.Errorf("%s imports %s: project discovery is allowed only in the CLI dependency composition file", filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), importPath)
				continue
			}
			if packagePath == modulePath+"/internal/api/server" && !strings.HasSuffix(path, "_test.go") && strings.HasPrefix(importPath, modulePath+"/internal/") && !matchesAny(importPath,
				modulePath+"/internal/api/contract",
				modulePath+"/internal/application",
				modulePath+"/internal/auth",
				modulePath+"/internal/model",
			) {
				t.Errorf("%s imports %s: the API server may depend only on contract, application, auth, and model packages", filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), importPath)
				continue
			}
			if reason := forbiddenImport(packagePath, importPath); reason != "" {
				t.Errorf("%s imports %s: %s", filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), importPath, reason)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPIClientTransportIsPrivate(t *testing.T) {
	root := repositoryRoot(t)
	clientDirectory := filepath.Join(root, "internal", "api", "client")
	err := filepath.WalkDir(clientDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
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
						t.Errorf("api/client.Client is not a struct")
						continue
					}
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							if ast.IsExported(name.Name) {
								t.Errorf("api/client.Client exposes transport field %s", name.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if value.Recv != nil && (value.Name.Name == "Do" || value.Name.Name == "DoWithHeaders") {
					t.Errorf("api/client exposes raw transport method %s", value.Name.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestObsoletePackagesAndRelayIdentifiersStayRemoved(t *testing.T) {
	root := repositoryRoot(t)
	for _, directory := range []string{"bootstrap", "ingress"} {
		path := filepath.Join(root, "internal", directory)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("obsolete package directory still exists: internal/%s", directory)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	legacy := []string{"__ingress", "__install-ingress", "__restart-ingress", "__uninstall-ingress", "dev.portless.ingress", "portless-ingress"}
	for _, directory := range []string{filepath.Join(root, "cmd"), filepath.Join(root, "internal", "relay")} {
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, value := range legacy {
				if strings.Contains(string(content), value) {
					t.Errorf("%s contains obsolete privileged relay identifier %q", filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), value)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCLIUsesTypedDaemonAPI(t *testing.T) {
	root := repositoryRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "internal", "cli"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "/api/v1") {
			t.Errorf("%s constructs a daemon API route; move it to api/client", filepath.Base(path))
		}
		if strings.Contains(string(content), ".Do(") || strings.Contains(string(content), ".DoWithHeaders(") {
			t.Errorf("%s calls the raw API transport; add a typed api/client method", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func forbiddenImport(source, target string) string {
	bootstrap := modulePath + "/internal/bootstrap"
	ingress := modulePath + "/internal/ingress"
	if target == bootstrap || strings.HasPrefix(target, bootstrap+"/") || target == ingress || strings.HasPrefix(target, ingress+"/") {
		return "obsolete bootstrap and ingress packages are forbidden"
	}

	installation := modulePath + "/internal/installation"
	if source == installation {
		if strings.HasPrefix(target, modulePath+"/") || isThirdPartyImport(target) {
			return "installation must use only the Go standard library"
		}
	}

	contract := modulePath + "/internal/api/contract"
	client := modulePath + "/internal/api/client"
	if source == contract && strings.HasPrefix(target, modulePath+"/") && target != modulePath+"/internal/model" {
		return "api contracts may depend only on model values"
	}
	if source == client && strings.HasPrefix(target, modulePath+"/") && target != contract {
		return "the API client may depend only on api/contract"
	}

	server := modulePath + "/internal/api/server"
	if source == server && matchesAny(target,
		modulePath+"/internal/cli",
		modulePath+"/internal/daemon/control",
		modulePath+"/internal/relay/install",
	) {
		return "the API server must receive process control and relay installation through injected interfaces"
	}

	control := modulePath + "/internal/daemon/control"
	if source == control && (matchesAny(target,
		server,
		modulePath+"/internal/application",
		modulePath+"/internal/store",
	) || target == modulePath+"/internal/daemon") {
		return "daemon control cannot depend on server composition or application state"
	}
	if source == modulePath+"/internal/daemon" && matchesAny(target, control) {
		return "daemon composition cannot depend on client-side daemon control"
	}

	if source == modulePath+"/internal/relay" || strings.HasPrefix(source, modulePath+"/internal/relay/install") {
		if matchesAny(target,
			modulePath+"/internal/daemon",
			server,
			modulePath+"/internal/application",
			modulePath+"/internal/cli",
		) {
			return "relay packages cannot depend on the daemon, API server, application, or CLI"
		}
	}

	if isDomainPackage(source) && matchesAny(target,
		modulePath+"/internal/cli",
		server,
		modulePath+"/internal/daemon",
		modulePath+"/internal/relay/install",
	) {
		return "domain packages cannot depend on process adapters"
	}

	cli := modulePath + "/internal/cli"
	if source == cli && matchesAny(target,
		modulePath+"/internal/application",
		modulePath+"/internal/runtime/container",
	) {
		return "CLI wire operations must use api/client and api/contract"
	}
	return ""
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

func isDomainPackage(path string) bool {
	for _, area := range []string{"application", "project", "proxy", "resource", "runtime", "store"} {
		prefix := modulePath + "/internal/" + area
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
