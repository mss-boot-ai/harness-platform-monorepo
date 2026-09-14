// Isolated acceptance stack: official Thin Host migrations/login, actual Gateway/ABA, deterministic ACP.
// All private state belongs to a fresh temporary directory and is removed when the stack stops.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer, request as upstreamRequest } from 'node:http';
import { createServer as portServer } from 'node:net';
import { mkdtemp, mkdir, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { randomBytes, randomUUID } from 'node:crypto';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { websocketTap } from './websocket-tap.mjs';
const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function freePort() {
  const server = portServer(); await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port; await new Promise((resolve) => server.close(resolve)); return port;
}
async function until(check, label, milliseconds = 60_000) {
  const deadline = Date.now() + milliseconds;
  while (Date.now() < deadline) { const result = await check(); if (result) return result; await delay(200); }
  throw new Error(`Timed out: ${label}`);
}
export async function startRemoteStack() {
  const binaries = { admin: process.env.HC_TEST_ADMIN_BINARY, gateway: process.env.HC_TEST_GATEWAY_BINARY, aba: process.env.HARNESS_TEST_ABA_BINARY };
  for (const [name, filename] of Object.entries(binaries)) assert.ok(filename && path.isAbsolute(filename), `Set an absolute ${name} binary path`);
  const directory = await realpath(await mkdtemp(path.join(tmpdir(), 'harness-hc-browser-')));
  const children = []; const sockets = new Set(); const logs = [];
  let wireObserver = () => true;
  const transportErrors = [];
  const password = process.env.HC_TEST_PASSWORD || `C3!aZ9${randomBytes(24).toString('base64url')}`;
  assert.ok(password.length >= 24, 'Use a long, test-only password for the isolated browser stack');
  const authKey = randomBytes(32).toString('base64url');
  const identityKey = randomBytes(32).toString('base64url');
  const username = 'harness-c3-admin';
  const adminPort = await freePort(); const gatewayPort = await freePort();
  const privateEnvironment = { PATH: process.env.PATH, LANG: 'C.UTF-8', STAGE: 'hc-browser-fixture', CONFIG_PROVIDER: 'local' };
  const dist = path.join(repository, 'hc/web/dist');
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1');
    const port = url.pathname.startsWith('/admin/api/') ? adminPort : url.pathname.startsWith('/gateway/v1/') ? gatewayPort : null;
    if (port !== null) {
      const upstream = upstreamRequest({ hostname: '127.0.0.1', port, path: request.url, method: request.method, headers: request.headers }, (result) => {
        response.writeHead(result.statusCode, result.headers); result.pipe(response);
      });
      upstream.on('error', () => { if (!response.headersSent) response.writeHead(503); response.end('Fixture service unavailable'); });
      request.pipe(upstream); return;
    }
    try {
      const file = path.resolve(dist, `.${decodeURIComponent(url.pathname)}`);
      if (!file.startsWith(`${dist}${path.sep}`) && file !== dist) { response.writeHead(404); response.end(); return; }
      const target = path.extname(file) === '' || file === dist ? path.join(dist, 'index.html') : file;
      const type = { '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.svg': 'image/svg+xml' }[path.extname(target)] ?? 'application/octet-stream';
      response.writeHead(200, { 'Content-Type': type, 'Cache-Control': 'no-store' }); response.end(await readFile(target));
    } catch { if (!response.headersSent) response.writeHead(404); response.end(); }
  });
  server.on('connection', (socket) => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  server.on('upgrade', (request, socket, head) => {
    if (new URL(request.url, 'http://127.0.0.1').pathname !== '/gateway/v1/ws') { socket.destroy(); return; }
    const upstream = upstreamRequest({ hostname: '127.0.0.1', port: gatewayPort, path: request.url, method: request.method, headers: request.headers });
    upstream.on('upgrade', (response, remote, extra) => {
      let headers = 'HTTP/1.1 101 Switching Protocols\r\n';
      for (const [name, value] of Object.entries(response.headers)) for (const entry of Array.isArray(value) ? value : [value]) if (entry !== undefined) headers += `${name}: ${entry}\r\n`;
      socket.write(`${headers}\r\n`); if (extra.length) socket.write(extra); if (head.length) remote.write(head);
      const outgoing = websocketTap((bytes) => wireObserver('client', bytes));
      const incoming = websocketTap((bytes) => wireObserver('server', bytes));
      for (const tap of [outgoing, incoming]) tap.on('error', (error) => { transportErrors.push(error.message); socket.destroy(); remote.destroy(); });
      socket.pipe(outgoing).pipe(remote); remote.pipe(incoming).pipe(socket); socket.on('error', () => remote.destroy()); remote.on('error', () => socket.destroy());
      socket.on('close', () => remote.destroy());
    });
    upstream.on('response', (response) => { socket.end(`HTTP/1.1 ${response.statusCode} Rejected\r\nConnection: close\r\n\r\n`); response.resume(); });
    upstream.on('error', () => socket.destroy()); upstream.end();
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const nativeBase = `http://127.0.0.1:${gatewayPort}`;
  const scrub = (text) => text.replaceAll(password, '[redacted]').replaceAll(authKey, '[redacted]').replaceAll(identityKey, '[redacted]').replace(/\b[A-Za-z0-9_-]{32,}\b/gu, '[redacted]');
  function launch(name, args, extra = {}) {
    const child = spawn(binaries[name], args, { cwd: directory, env: { ...privateEnvironment, ...extra }, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    const entry = { child, name, output: '', code: null, ended: false }; children.push(entry);
    const capture = (chunk) => { if (entry.output.length < 1_048_576) entry.output += chunk.toString().slice(0, 1_048_576 - entry.output.length); };
    child.stdout.on('data', capture); child.stderr.on('data', capture);
    entry.done = new Promise((resolve, reject) => {
      child.on('error', reject); child.on('exit', (code) => { entry.code = code; entry.ended = true; logs.push(entry.output); resolve(code); });
    });
    return entry;
  }
  let stopped = false;
  async function stop() {
    if (stopped) return; stopped = true;
    for (const entry of children) if (!entry.ended && entry.child.pid) { try { process.kill(-entry.child.pid, 'SIGTERM'); } catch {} }
    for (const socket of sockets) socket.destroy(); await new Promise((resolve) => server.close(resolve));
    await Promise.race([Promise.allSettled(children.map((entry) => entry.done)), delay(5000)]);
    for (const entry of children) if (!entry.ended && entry.child.pid) { try { process.kill(-entry.child.pid, 'SIGKILL'); } catch {} }
    await Promise.allSettled(children.map((entry) => entry.done));
    await rm(directory, { recursive: true, force: true });
  }
  try {
    await mkdir(path.join(directory, 'config'), { mode: 0o700 });
    for (const name of ['workspace-a', 'workspace-b']) await mkdir(path.join(directory, name), { mode: 0o700 });
    const database = path.join(directory, 'platform.db');
    const config = {
      server: { addr: `127.0.0.1:${adminPort}`, healthz: true, readyz: true, metrics: false, pprof: false },
      application: { mode: 'dev', origin: base, trustedProxies: [], labels: { app: 'harness-hc-browser-fixture' } },
      cors: { allowOrigins: [base], allowMethods: ['GET', 'POST', 'PUT', 'DELETE', 'OPTIONS'], allowHeaders: ['Authorization', 'Content-Type', 'Idempotency-Key', 'X-CSRF-Token'], maxAge: '1h' },
      logger: { stdout: 'default', level: 'warn', addSource: false },
      database: { driver: 'sqlite', source: `${database}?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate`, name: 'harness-c3-fixture',
        config: { disableForeignKeyConstraintWhenMigrating: true }, timeout: '10s',
        casbinModel: '[request_definition]\nr = sub, tp, obj, act\n[policy_definition]\np = sub, tp, obj, act\n[policy_effect]\ne = some(where (p.eft == allow))\n[matchers]\nm = r.sub == p.sub && r.tp == p.tp && keyMatch(r.obj, p.obj) && regexMatch(r.act, p.act)' },
      auth: { realm: 'harness-fixture', key: authKey, identityKey, timeout: '1h', maxRefresh: '1h', browserSession: { secure: false, sameSite: 'lax', webSocketTicketTTL: '30s' } },
      monitor: { sampleInterval: '5s', sampleTimeout: '3s', historySize: 12 },
      cache: { queryCache: false, redis: null }, queue: { memory: { poolSize: 2 } }, task: { enable: false },
    };
    await writeFile(path.join(directory, 'config/application.yml'), JSON.stringify(config), { mode: 0o600 });
    const migration = launch('admin', ['migrate', '--config-provider', 'local', '--username', username, '--domain', new URL(base).host], { MSS_ADMIN_INITIAL_PASSWORD: password });
    if (await migration.done !== 0) throw new Error(`Fixture migration failed: ${scrub(migration.output).slice(-5000)}`);
    const admin = launch('admin', ['server', '--config-provider', 'local']);
    const gateway = launch('gateway', [], { HARNESS_GATEWAY_ADDR: `127.0.0.1:${gatewayPort}`, HARNESS_GATEWAY_DB_DSN: `file:${database}?_pragma=busy_timeout(30000)`,
      HARNESS_GATEWAY_ALLOWED_ORIGIN: base, HARNESS_GATEWAY_EXTERNAL_ORIGIN: base, HARNESS_GATEWAY_NATIVE_EXTERNAL_ORIGIN: nativeBase,
      HARNESS_GATEWAY_TRUST_FILE: path.join(directory, 'gateway-trust.json') });
    await until(async () => {
      for (const entry of [admin, gateway]) if (entry.ended) throw new Error(`${entry.name} exited: ${scrub(entry.output).slice(-5000)}`);
      try { return (await fetch(`${base}/gateway/v1/health`)).ok && (await fetch(`http://127.0.0.1:${adminPort}/healthz`)).ok; } catch { return false; }
    }, 'isolated Admin/Gateway readiness');
    const cookies = new Map();
    async function adminRequest(url, method = 'GET', body) {
      const headers = { Origin: base, 'Content-Type': 'application/json', Cookie: [...cookies].map(([key, value]) => `${key}=${value}`).join('; ') };
      if (cookies.has('mss_csrf')) headers['X-CSRF-Token'] = decodeURIComponent(cookies.get('mss_csrf'));
      if (method !== 'GET') headers['Idempotency-Key'] = randomUUID();
      const response = await fetch(`${base}${url}`, { method, headers, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
      for (const cookie of response.headers.getSetCookie()) { const value = cookie.split(';')[0]; const split = value.indexOf('='); cookies.set(value.slice(0, split), value.slice(split + 1)); }
      const value = await response.json();
      if (!response.ok) throw new Error(`Fixture Admin request ${url} failed: ${response.status} ${String(value.code ?? '')}`);
      return value;
    }
    await adminRequest('/admin/api/user/session/login', 'POST', { username, password });
    const identity = path.join(directory, 'identity.json');
    const initialized = launch('aba', ['identity', 'init', '--store', identity, '--platform', nativeBase, '--insecure-dev-keystore', '--json']);
    if (await initialized.done !== 0) throw new Error(`ABA identity initialization failed: ${scrub(initialized.output).slice(-1500)}`);
    const enrollment = launch('aba', ['enroll', '--store', identity, '--platform', nativeBase, '--name', 'C3 acceptance ABA', '--insecure-dev-keystore', '--timeout-seconds', '120']);
    const code = await until(async () => { if (enrollment.ended) throw new Error(`ABA enrollment exited: ${scrub(enrollment.output).slice(-1500)}`); return /user-code: ([A-Z0-9-]+)/u.exec(enrollment.output)?.[1]; }, 'ABA enrollment code');
    const pending = await until(async () => (await adminRequest('/admin/api/harness/v1/enrollments?limit=100')).items.find((item) => item.endpointName === 'C3 acceptance ABA' && item.status === 'PENDING'), 'pending enrollment');
    await adminRequest(`/admin/api/harness/v1/enrollments/${pending.id}/approve`, 'POST', { userCode: code });
    if (await enrollment.done !== 0) throw new Error('ABA enrollment did not complete');
    const python = await realpath(process.env.HC_TEST_PYTHON ?? '/usr/bin/python3');
    const fixture = path.join(repository, 'aba/tests/fixtures/duplex_agent.py');
    const toml = `schema_version = 1\npublish_catalog = true\n[platform]\nurl = ${JSON.stringify(nativeBase)}\n[[runtime]]\nid = "fixture"\ndisplay_name = "Deterministic ACP fixture"\ncommand = ${JSON.stringify(python)}\nargs = [${JSON.stringify(fixture)}]\nenv_allow = ["HC_E2E_AUDIT"]\nmax_sessions = 2\n` + ['workspace-a', 'workspace-b'].map((name) => `[[workspace]]\nid = "${name}"\ndisplay_name = "${name}"\npath = ${JSON.stringify(path.join(directory, name))}\nallowed_runtimes = ["fixture"]\n`).join('');
    const configuration = path.join(directory, 'aba.toml'); await writeFile(configuration, toml, { mode: 0o600 });
    const aba = launch('aba', ['run', '--config', configuration, '--store', identity, '--insecure-dev-keystore'], { HC_E2E_AUDIT: '1' });
    await until(async () => {
      if (aba.ended) throw new Error(`ABA runtime exited: ${scrub(aba.output).slice(-1500)}`);
      return (await adminRequest('/admin/api/harness/v1/endpoints?limit=100')).items.find((item) => item.name === 'C3 acceptance ABA' && item.lastSeenAt);
    }, 'ABA authenticated connection');
    console.log('Isolated Thin Host login, ABA enrollment and Gateway connection are ready (deterministic ACP fixture).');
    return { base, username, password, directory, stop, adminRequest, transportErrors, disconnectClients: () => { for (const socket of sockets) socket.destroy(); }, observeWire: (observer) => { wireObserver = observer; },
      async audit(workspace) { assert.ok(['workspace-a', 'workspace-b'].includes(workspace)); try { return (await readFile(path.join(directory, workspace, '.hc-e2e-executions'), 'utf8')).trim().split('\n').filter(Boolean).map((line) => JSON.parse(line)); } catch (error) { if (error.code === 'ENOENT') return []; throw error; } },
      async assertOpaque(canary) {
        const needles = [canary, Buffer.from(canary).toString('base64'), Buffer.from(canary).toString('base64url')];
        const buffers = [await readFile(database), ...children.filter((entry) => ['admin', 'gateway'].includes(entry.name)).map((entry) => Buffer.from(entry.output))];
        try { buffers.push(await readFile(`${database}-wal`)); } catch (error) { if (error.code !== 'ENOENT') throw error; }
        for (const bytes of buffers) for (const needle of needles) assert.equal(bytes.includes(Buffer.from(needle)), false, 'Plaintext canary leaked into Platform state');
      },
    };
  } catch (error) { await stop(); throw error; }
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const stack = await startRemoteStack(); console.log(`HC_BROWSER_TEST_URL=${stack.base}`);
  process.on('SIGUSR2', () => stack.disconnectClients());
  await new Promise((resolve) => { process.once('SIGINT', resolve); process.once('SIGTERM', resolve); }); await stack.stop();
}
