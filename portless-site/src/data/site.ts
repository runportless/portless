export const problemCards = [
  {project: 'payments', service: 'postgres', port: ':5432', tone: 'danger'},
  {project: 'billing', service: 'postgres', port: ':5432', tone: 'danger'},
  {project: 'checkout', service: 'valkey', port: ':6379', tone: 'warning'},
  {project: 'orders', service: 'valkey', port: ':6379', tone: 'warning'},
] as const;

export const principles = [
  {
    index: '01',
    title: 'No required project file',
    copy: 'Bounded, read-only discovery understands supported application frameworks and managed resources without running project code.',
  },
  {
    index: '02',
    title: 'Readable endpoints',
    copy: 'Every service gets a stable project and environment-aware address while private runtime ports stay private.',
  },
  {
    index: '03',
    title: 'One application, many sources',
    copy: 'Model a project across repositories, then choose a local, container, remote, or mock provider for each component.',
  },
  {
    index: '04',
    title: 'Local by design',
    copy: 'There is no account or hosted control plane. Environment state, traffic, recordings, and faults stay on your machine.',
  },
] as const;

export const frameworks = ['Spring Boot', 'NestJS', 'Express', 'Fastify', 'Next.js', 'Go HTTP/RPC', 'FastAPI'] as const;
export const resources = ['PostgreSQL', 'Valkey', 'MySQL', 'NATS'] as const;

export function primaryCallToAction(earlyAccessURL?: string) {
  const href = earlyAccessURL?.trim();
  return href
    ? {href, label: 'Get early access', external: true}
    : {href: '#demo', label: 'Watch the demo', external: false};
}
