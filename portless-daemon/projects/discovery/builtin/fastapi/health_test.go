package fastapi

import "testing"

func TestFastAPIRouterHealthCandidateUsesStaticPrefixes(t *testing.T) {
	tokens := tokenizePython([]byte(`
app = FastAPI()
router = fastapi.APIRouter(prefix="/system")
app.include_router(router, prefix="/api")

@router.get("/ready")
def ready():
    return {"ready": True}
`))
	routers := fastAPIRouters(tokens)
	included := includedFastAPIRouters(tokens, "app", routers)
	if routers["router"] != "/system" || included["router"] != "/api" {
		t.Fatalf("routers = %#v, included = %#v", routers, included)
	}
}

func TestFastAPIRouterDynamicPrefixFailsClosed(t *testing.T) {
	tokens := tokenizePython([]byte(`
app = FastAPI()
router = APIRouter(prefix=SETTINGS_PREFIX)
app.include_router(router)
@router.get("/health")
def health(): pass
`))
	routers := fastAPIRouters(tokens)
	if len(routers) != 0 {
		t.Fatalf("dynamic router prefix was accepted: %#v", routers)
	}
}

func TestPythonTokenizerDoesNotPromoteCommentsDocstringsOrFStrings(t *testing.T) {
	tokens := tokenizePython([]byte(`
# @app.get("/health")
"""@app.get('/health')"""
@app.get(f"/{kind}")
def route(): pass
`))
	for index := 0; index+5 < len(tokens); index++ {
		if tokens[index].value == "@" && tokens[index+1].value == "app" && tokens[index+3].value == "get" && tokens[index+5].kind == pythonString {
			t.Fatal("dynamic or non-code route was tokenized as a static decorator")
		}
	}
}
