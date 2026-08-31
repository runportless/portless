package golang

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"strconv"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
	"github.com/runportless/portless/portless-daemon/projects/discovery/statichealth"
)

const goRegisteredRouteRank = 400

type goSource struct {
	file   string
	parsed *ast.File
}

type goHealthDetection struct {
	path       string
	evidence   *model.Evidence
	diagnostic *spec.Diagnostic
}

func detectGoHealth(sources []goSource) goHealthDetection {
	routers := goRouterVariables(sources)
	var candidates []statichealth.Candidate
	for _, source := range sources {
		imports := goImportNames(source.parsed)
		ast.Inspect(source.parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			endpoint, ok := goRoutePath(call, imports, routers)
			if !ok {
				return true
			}
			semantic := statichealth.SemanticRank(endpoint)
			if semantic == 0 {
				return true
			}
			candidates = append(candidates, statichealth.Candidate{
				Path: endpoint, File: source.file, Explanation: fmt.Sprintf("Go HTTP readiness route %s found", endpoint), Rank: goRegisteredRouteRank + semantic,
			})
			return true
		})
	}
	selection := statichealth.Select(candidates)
	if len(selection.Ambiguous) > 0 {
		paths := make([]string, 0, len(selection.Ambiguous))
		for _, candidate := range selection.Ambiguous {
			paths = append(paths, candidate.Path)
		}
		return goHealthDetection{diagnostic: &spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "AMBIGUOUS_HEALTH_ENDPOINT", File: selection.Ambiguous[0].File,
			Message: fmt.Sprintf("equally strong readiness routes %s were found; TCP readiness was kept", strings.Join(paths, ", ")),
		}}
	}
	if selection.Candidate.Path == "" {
		return goHealthDetection{}
	}
	return goHealthDetection{
		path: selection.Candidate.Path,
		evidence: &model.Evidence{
			File: selection.Candidate.File, Explanation: selection.Candidate.Explanation, Confidence: "high",
		},
	}
}

func goRouterVariables(sources []goSource) map[string]bool {
	result := make(map[string]bool)
	for _, source := range sources {
		imports := goImportNames(source.parsed)
		ast.Inspect(source.parsed, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.AssignStmt:
				for index, expression := range declaration.Rhs {
					if !goRouterConstructor(expression, imports) || index >= len(declaration.Lhs) {
						continue
					}
					if name, ok := declaration.Lhs[index].(*ast.Ident); ok {
						result[name.Name] = true
					}
				}
			case *ast.ValueSpec:
				for index, expression := range declaration.Values {
					if !goRouterConstructor(expression, imports) || index >= len(declaration.Names) {
						continue
					}
					result[declaration.Names[index].Name] = true
				}
			}
			return true
		})
	}
	return result
}

func goImportNames(file *ast.File) map[string]string {
	result := make(map[string]string)
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(importPath)
		if len(name) > 1 && name[0] == 'v' {
			if _, versionErr := strconv.Atoi(name[1:]); versionErr == nil {
				name = path.Base(path.Dir(importPath))
			}
		}
		if imported.Name != nil && imported.Name.Name != "" && imported.Name.Name != "." && imported.Name.Name != "_" {
			name = imported.Name.Name
		}
		result[name] = importPath
	}
	return result
}

func goRouterConstructor(expression ast.Expr, imports map[string]string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	importPath := imports[receiver.Name]
	switch importPath {
	case "net/http":
		return selector.Sel.Name == "NewServeMux"
	case "github.com/gin-gonic/gin":
		return selector.Sel.Name == "Default" || selector.Sel.Name == "New"
	case "github.com/go-chi/chi", "github.com/go-chi/chi/v5":
		return selector.Sel.Name == "NewRouter" || selector.Sel.Name == "NewMux"
	case "github.com/gofiber/fiber", "github.com/gofiber/fiber/v2":
		return selector.Sel.Name == "New"
	case "github.com/labstack/echo", "github.com/labstack/echo/v4":
		return selector.Sel.Name == "New"
	case "github.com/gorilla/mux":
		return selector.Sel.Name == "NewRouter"
	default:
		return false
	}
}

func goRoutePath(call *ast.CallExpr, imports map[string]string, routers map[string]bool) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	method := selector.Sel.Name
	if receiver, ok := selector.X.(*ast.Ident); ok {
		if imports[receiver.Name] == "net/http" && (method == "Handle" || method == "HandleFunc") {
			return goLiteralRoute(call.Args, 0, false)
		}
		if !routers[receiver.Name] {
			return "", false
		}
		switch method {
		case "Handle", "HandleFunc", "GET", "Get":
			return goLiteralRoute(call.Args, 0, false)
		case "Method", "MethodFunc":
			return goLiteralRoute(call.Args, 1, true)
		}
	}
	return "", false
}

func goLiteralRoute(arguments []ast.Expr, pathIndex int, methodArgument bool) (string, bool) {
	if pathIndex >= len(arguments) {
		return "", false
	}
	if methodArgument {
		if len(arguments) == 0 {
			return "", false
		}
		method, ok := goStringLiteral(arguments[0])
		if !ok || !strings.EqualFold(method, "GET") {
			return "", false
		}
	}
	value, ok := goStringLiteral(arguments[pathIndex])
	if !ok {
		return "", false
	}
	if method, endpoint, found := strings.Cut(value, " "); found {
		if !strings.EqualFold(strings.TrimSpace(method), "GET") {
			return "", false
		}
		value = strings.TrimSpace(endpoint)
	}
	endpoint := statichealth.CleanPath(value)
	return endpoint, endpoint != ""
}

func goStringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}
