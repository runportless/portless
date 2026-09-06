import { appendFile, cp, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const execute = promisify(execFile);
const checkout = fileURLToPath(new URL('..', import.meta.url));

export async function createInstallation(artifactDirectory) {
  if (!process.env.PORTLESS_E2E_BINARY) throw new Error('Run make test-e2e-chat: PORTLESS_E2E_BINARY is required.');
  const binary = await realpath(process.env.PORTLESS_E2E_BINARY);
  const root = await mkdtemp(join(tmpdir(), 'portless-chat-e2e-'));
  const home = join(root, 'home');
  await mkdir(home, { mode: 0o700 });
  await mkdir(artifactDirectory, { recursive: true });
  const log = join(artifactDirectory, 'commands.log');
  const knownPIDs = new Set();
  let stopped = false;

  async function cli(args, tolerateFailure = false) {
    let result;
    try {
      result = await execute(binary, args, {
        cwd: checkout, env: { ...process.env, PORTLESS_HOME: home, NO_COLOR: '1' },
        timeout: 90_000, maxBuffer: 4 * 1024 * 1024, encoding: 'utf8',
      });
      result.code = 0;
    } catch (error) { result = error; }
    const output = (result.stdout || '') + (result.stderr || '');
    await appendFile(log, '$ portless ' + args.join(' ') + '\n' + output + '\n');
    if (result.code !== 0 && !tolerateFailure) throw new Error('portless ' + args.join(' ') + ' failed:\n' + output);
    return { code: result.code, output, stdout: result.stdout || '' };
  }
  async function json(args) { return JSON.parse((await cli(['--json', ...args])).stdout); }
  async function status() {
    const environment = await json(['status']);
    for (const service of environment.services || []) if (service.pid) knownPIDs.add(service.pid);
    return environment;
  }
  async function copyLogs(directory, destination) {
    for (const entry of await readdir(directory, { withFileTypes: true }).catch(() => [])) {
      if (entry.isSymbolicLink()) continue;
      const source = join(directory, entry.name);
      const target = join(destination, entry.name);
      if (entry.isDirectory()) await copyLogs(source, target);
      else if (entry.name.endsWith('.log')) {
        await mkdir(destination, { recursive: true });
        await cp(source, target);
      }
    }
  }
  return {
    home, root, cli, json, status, url: '',
    async start() {
      await cli(['up', '--managed', '--no-open', '--timeout', '60s']);
      const environment = await status();
      if (environment.project !== 'chat' || environment.name !== 'local') throw new Error('Chat discovery returned an unexpected project identity.');
      const service = environment.services.find((item) => item.name === 'chat');
      const endpoint = service?.endpoints.find((item) => item.url?.startsWith('http:'));
      if (!endpoint) throw new Error('Chat has no discovered HTTP endpoint.');
      const control = JSON.parse(await readFile(join(home, 'control.json'), 'utf8'));
      const url = new URL(endpoint.url);
      url.port = String(control.port);
      this.url = url.href;
    },
    async stop() {
      if (stopped) return;
      stopped = true;
      await status().catch(() => {});
      const daemon = await json(['daemon', 'status']).catch(() => null);
      if (daemon?.pid) knownPIDs.add(daemon.pid);
      // Only log files and redacted CLI output are artifacts; never copy installation keys.
      await cli(['logs', '--limit', '100'], true);
      await copyLogs(home, join(artifactDirectory, 'logs'));
      const reset = await cli(['reset', '--force', '--yes'], true);
      const stop = await cli(['daemon', 'stop', '--force'], true);
      const alive = (pid) => { try { process.kill(pid, 0); return true; } catch (error) { return error.code !== 'ESRCH'; } };
      const deadline = Date.now() + 5000;
      while ([...knownPIDs].some(alive) && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 50));
      const remaining = [...knownPIDs].filter(alive);
      const summary = { reset: reset.code, stop: stop.code, remaining };
      await writeFile(join(artifactDirectory, 'cleanup.json'), JSON.stringify(summary, null, 2) + '\n');
      if (remaining.length || reset.code !== 0 || stop.code !== 0) throw new Error('Chat test cleanup failed; retained isolated home at ' + home);
      await rm(root, { recursive: true, force: true });
      if (process.env.PORTLESS_E2E_ARTIFACT_DIR) {
        const destination = join(resolve(process.env.PORTLESS_E2E_ARTIFACT_DIR), basename(root));
        await cp(artifactDirectory, destination, { recursive: true });
      }
    },
  };
}
