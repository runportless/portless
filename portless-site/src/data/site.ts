export const problemCards = [
  {scope: 'Service', owner: 'orders', service: 'http', port: ':8080', tone: 'danger'},
  {scope: 'Service', owner: 'inventory', service: 'http', port: ':8080', tone: 'danger'},
  {scope: 'Resource', owner: 'orders', service: 'postgres', port: ':5432', tone: 'warning'},
  {scope: 'Resource', owner: 'inventory', service: 'postgres', port: ':5432', tone: 'warning'},
] as const;

export const principles = [
  {
    icon: 'file-off',
    title: 'No required project file',
    copy: 'Bounded, read-only discovery understands supported application frameworks and managed resources without running project code.',
  },
  {
    icon: 'link',
    title: 'Readable endpoints',
    copy: 'Every service gets a stable project and environment-aware address while private runtime ports stay private.',
  },
  {
    icon: 'branches',
    title: 'One application, many sources',
    copy: 'Model a project across repositories, then choose a local, container, remote, or mock provider for each component.',
  },
  {
    icon: 'machine',
    title: 'Local by design',
    copy: 'There is no account or hosted control plane. Environment state, traffic, recordings, and faults stay on your machine.',
  },
] as const;

export const frameworks = [
  {name: 'Spring Boot', href: 'https://spring.io/projects/spring-boot/'},
  {name: 'NestJS', href: 'https://nestjs.com/'},
  {name: 'Express', href: 'https://expressjs.com/'},
  {name: 'Fastify', href: 'https://fastify.dev/'},
  {name: 'Next.js', href: 'https://nextjs.org/'},
  {name: 'Go HTTP/RPC', href: 'https://go.dev/'},
  {name: 'FastAPI', href: 'https://fastapi.tiangolo.com/'},
] as const;

export const resources = [
  {name: 'PostgreSQL', href: 'https://www.postgresql.org/'},
  {name: 'Valkey', href: 'https://valkey.io/'},
  {name: 'MySQL', href: 'https://www.mysql.com/'},
  {name: 'NATS', href: 'https://nats.io/'},
] as const;

export function primaryCallToAction(earlyAccessURL?: string) {
  const href = earlyAccessURL?.trim();
  return href
    ? {href, label: 'Get early access', external: true}
    : {href: '#demo', label: 'Watch the demo', external: false};
}
