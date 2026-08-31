package node

import (
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestNestHealthCandidatesComposeStaticPrefixes(t *testing.T) {
	source := nodeSource{file: "src/health.controller.ts", tokens: tokenizeJavaScript([]byte(`
@Controller('system')
class HealthController {
  @Get('health')
  @HealthCheck()
  check() { return this.health.check([]) }
}
`))}
	candidates := nestHealthCandidates(source, "/api")
	if len(candidates) != 1 || candidates[0].Path != "/api/system/health" || candidates[0].Rank != nestHealthCheckRank+10 {
		t.Fatalf("NestJS health candidates = %#v", candidates)
	}
}

func TestRegisteredNodeHealthRoutesRequireAProvenServer(t *testing.T) {
	source := nodeSource{file: "server.ts", tokens: tokenizeJavaScript([]byte(`
const app = express()
const router = express.Router()
app.use('/internal', router)
router.get('/ready', handler)
client.get('/health')
// app.get('/commented-health', handler)
`))}
	candidates := registeredGetHealthCandidates(source)
	if len(candidates) != 1 || candidates[0].Path != "/internal/ready" {
		t.Fatalf("registered health candidates = %#v", candidates)
	}
}

func TestFastifyPluginHealthRouteUsesAStaticRegisterPrefix(t *testing.T) {
	source := nodeSource{file: "server.ts", tokens: tokenizeJavaScript([]byte(`
const routes = async (instance) => {
  instance.get('/health', handler)
}
const app = Fastify()
app.register(routes, { prefix: '/internal' })
`))}
	candidates := registeredGetHealthCandidates(source)
	if len(candidates) != 1 || candidates[0].Path != "/internal/health" {
		t.Fatalf("Fastify plugin candidates = %#v", candidates)
	}

	dynamic := nodeSource{file: "server.ts", tokens: tokenizeJavaScript([]byte(`
function routes(instance) { instance.get('/health', handler) }
const app = Fastify()
app.register(routes, { prefix: configuration.prefix })
`))}
	if candidates := registeredGetHealthCandidates(dynamic); len(candidates) != 0 {
		t.Fatalf("dynamic Fastify prefix was accepted: %#v", candidates)
	}
}

func TestRawNodeAndNextHealthRoutesAreStatic(t *testing.T) {
	raw := nodeSource{file: "server.mjs", tokens: tokenizeJavaScript([]byte(`
import { createServer } from 'node:http'
createServer((request, response) => {
  const url = new URL(request.url, 'http://localhost')
	url.pathname = '/ready'
  if (request.method === 'GET' && url.pathname === '/health') response.end('ok')
}).listen(Number(process.env.PORT))
`))}
	rawCandidates := rawNodeHealthCandidates(raw, true)
	if len(rawCandidates) != 1 || rawCandidates[0].Path != "/health" {
		t.Fatalf("raw Node candidates = %#v", rawCandidates)
	}

	next := nodeSource{file: "apps/web/src/app/(system)/api/health/route.ts", tokens: tokenizeJavaScript([]byte(`
export async function GET() { return Response.json({ ok: true }) }
`))}
	candidate, ok := nextHealthCandidate("apps/web", next)
	if !ok || candidate.Path != "/api/health" {
		t.Fatalf("Next.js candidate = %#v, %t", candidate, ok)
	}

	dynamic := nodeSource{file: "apps/web/src/app/api/[name]/route.ts", tokens: next.tokens}
	if candidate, ok := nextHealthCandidate("apps/web", dynamic); ok {
		t.Fatalf("dynamic Next.js route was accepted: %#v", candidate)
	}
}

func TestJavaScriptTokenizerRejectsDynamicTemplatesAndIgnoresComments(t *testing.T) {
	tokens := tokenizeJavaScript([]byte("// app.get('/health')\nconst route = `/health/${kind}`\nconst literal = '/ready'"))
	stringsFound := make([]string, 0)
	for _, token := range tokens {
		if token.kind == javascriptString {
			stringsFound = append(stringsFound, token.value)
		}
	}
	if !reflect.DeepEqual(stringsFound, []string{"/ready"}) {
		t.Fatalf("static strings = %#v", stringsFound)
	}
}

func TestStaticGlobalPrefixDetectionFailsClosedOnDynamicValues(t *testing.T) {
	values, dynamic := staticMethodStringArguments(tokenizeJavaScript([]byte(`
app.setGlobalPrefix('/api')
other.setGlobalPrefix(configuration.prefix)
`)), "setGlobalPrefix")
	if !reflect.DeepEqual(values, []string{"/api"}) || !dynamic {
		t.Fatalf("prefixes = %#v, dynamic = %t", values, dynamic)
	}
}

func TestNextPagesHealthRouteRequiresAStaticGETCheck(t *testing.T) {
	withoutMethod := nodeSource{file: "pages/api/health.ts", tokens: tokenizeJavaScript([]byte(`export default function handler(_request, response) { response.end() }`))}
	if candidate, ok := nextHealthCandidate(".", withoutMethod); ok {
		t.Fatalf("method-agnostic pages route was accepted: %#v", candidate)
	}
	withMethod := nodeSource{file: "pages/api/health.ts", tokens: tokenizeJavaScript([]byte(`
export default function handler(request, response) {
  if (request.method === 'GET') response.end()
}
`))}
	if candidate, ok := nextHealthCandidate(".", withMethod); !ok || candidate.Path != "/api/health" {
		t.Fatalf("GET pages route = %#v, %t", candidate, ok)
	}
}

func TestNodeDebugCapabilityPrefersExplicitDebugScript(t *testing.T) {
	manifest := packageJSON{Scripts: map[string]string{
		"start:dev":   "nest start --watch",
		"start:debug": "nest start --watch --debug 0.0.0.0:9229",
	}}
	capability := nodeDebugCapability(manifest, "npm", "start:dev")
	if capability == nil || capability.Adapter != model.DebugNodeInspector || capability.Launcher != model.DebugNestCLI {
		t.Fatalf("capability = %#v", capability)
	}
	want := []string{"npm", "exec", "--", "nest", "start", "--watch"}
	if !reflect.DeepEqual(capability.Command, want) {
		t.Fatalf("command = %#v, want %#v", capability.Command, want)
	}
}

func TestNodeDebugCapabilitySupportsDirectNodeAndRejectsShellScripts(t *testing.T) {
	direct := nodeDebugCapability(packageJSON{Scripts: map[string]string{"dev": `node "dist/main.js"`}}, "pnpm", "dev")
	if direct == nil || direct.Launcher != model.DebugNodeDirect || !reflect.DeepEqual(direct.Command, []string{"node", "dist/main.js"}) {
		t.Fatalf("direct capability = %#v", direct)
	}
	unsafe := nodeDebugCapability(packageJSON{Scripts: map[string]string{"dev": "node server.js | tee server.log"}}, "npm", "dev")
	if unsafe != nil {
		t.Fatalf("unsafe shell command was accepted: %#v", unsafe)
	}
}
