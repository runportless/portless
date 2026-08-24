export function GET() {
  return Response.json({ service: 'console', ready: true }, {
    headers: { 'X-Dispatch-Service': 'console' },
  })
}

