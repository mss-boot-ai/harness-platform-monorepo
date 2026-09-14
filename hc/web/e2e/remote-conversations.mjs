// CI acceptance of the production entrypoint against real Admin/Gateway/ABA and a deterministic ACP fixture.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startRemoteStack } from './run-remote-conversations.mjs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const tools = process.env.HC_BROWSER_TOOLS;
assert.ok(tools, 'Set HC_BROWSER_TOOLS to pinned Playwright 1.55.0');
const { chromium } = createRequire(path.join(tools, 'package.json'))('playwright');
const { createServer } = await import(path.join(root, 'hc/node_modules/vite/dist/node/index.js'));
const loader = await createServer({ root: path.join(root, 'hc/web'), configFile: path.join(root, 'hc/web/vite.config.ts'),
  server: { middlewareMode: true }, appType: 'custom', logLevel: 'error' });
const { decodeWireMessage, WirePacketSchema } = await loader.ssrLoadModule('/e2e/protocol.ts');
const output = process.env.HC_BROWSER_OUTPUT ?? path.join(root, 'hc/web/browser-evidence/remote');
await mkdir(output, { recursive: true });
const report = { runtime: 'deterministic ACP fixture; no live model', source: process.env.GITHUB_SHA ?? 'local', scenarios: [], limitations: [
  'Same browser installation only; independent-device authorization and persistent Host restart recovery remain unverified.',
  'Storage corruption/quota/key-loss refusal also has unit coverage; this browser run does not claim every storage fault combination.',
] };
let stack; let browser;
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(predicate, label, milliseconds = 30_000) {
  const deadline = Date.now() + milliseconds;
  while (Date.now() < deadline) { if (await predicate()) return; await delay(100); }
  throw new Error(`Timed out: ${label}`);
}
try {
  stack = await startRemoteStack();
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'zh-CN', reducedMotion: 'reduce' });
  let page = await context.newPage(); const errors = []; const registrations = [];
  const observePage = (target) => {
    target.on('pageerror', (error) => errors.push(error.message));
    target.on('request', (request) => { if (new URL(request.url()).pathname === '/admin/api/harness/v1/hc/endpoints' && request.method() === 'POST') registrations.push('registered'); });
  };
  observePage(page);
  const sent = []; let fault = null;
  stack.observeWire((direction, bytes) => {
    const packet = decodeWireMessage(WirePacketSchema, bytes);
    if (direction === 'client' && packet.body.case === 'encrypted') {
      const frame = packet.body.value; const id = Buffer.from(frame.sessionId).toString('hex');
      const item = { session: id, sequence: frame.sequence, message: Buffer.from(frame.messageId).toString('hex'), bytes: Buffer.from(bytes) }; sent.push(item);
      if (fault !== null && fault.session === id && fault.captured === null) {
        fault.captured = item;
        if (fault.mode === 'before-forward') { fault.hit = true; return false; }
      }
    }
    if (direction === 'server' && packet.body.case === 'ack' && fault?.captured !== null && fault?.captured !== undefined && !fault.hit) {
      const ack = packet.body.value;
      if (Buffer.from(ack.sessionId).toString('hex') === fault.session && ack.highestContiguousSequence >= fault.captured.sequence) {
        fault.hit = true; if (fault.mode === 'before-ack') return false;
      }
    }
    return true;
  });
  const settings = async () => { await page.locator('.connection-badge').click(); await page.getByRole('dialog', { name: '连接与设置', exact: true }).waitFor(); };
  await page.goto(stack.base, { waitUntil: 'networkidle' }); await settings();
  await page.getByRole('button', { name: '启用安全浏览器身份', exact: true }).click();
  await page.getByLabel('用户名', { exact: true }).fill(stack.username);
  await page.getByLabel('密码', { exact: true }).fill(stack.password);
  await page.getByRole('button', { name: '登录并连接', exact: true }).click();
  await page.getByRole('button', { name: '返回对话', exact: true }).waitFor({ timeout: 30_000 });
  await page.getByRole('button', { name: '返回对话', exact: true }).click();
  const environment = async (workspace) => {
    await page.locator('.agent-picker > summary').click();
    await page.getByLabel('Agent（Runtime ID）', { exact: true }).fill('fixture');
    await page.getByLabel('工作区 ID', { exact: true }).fill(workspace);
    await page.locator('.agent-picker > summary').click();
  };
  const send = async (text) => {
    await page.getByRole('textbox', { name: '消息', exact: true }).fill(text);
    await until(() => page.getByRole('button', { name: '发送消息', exact: true }).isEnabled(), 'send enabled');
    await page.getByRole('button', { name: '发送消息', exact: true }).click();
  };
  const completed = async (count) => until(async () => await page.getByRole('button', { name: '复制回复', exact: true }).count() === count, 'completed response');
  const selection = async (prefix) => { await page.locator('.conversation-item').filter({ hasText: prefix }).click(); };
  const sessionFor = async (workspace) => (await stack.adminRequest('/admin/api/harness/v1/sessions?limit=200')).items.find((item) => item.workspaceId === workspace);
  const canary = `HC_C3_CANARY_${randomUUID()}`;
  await environment('workspace-a'); await send(canary); await completed(1);
  const a = await sessionFor('workspace-a'); assert.ok(a); assert.equal(a.status, 'ACTIVE');
  await page.locator('.agent-picker > summary').click();
  await page.getByRole('combobox', { name: '模型', exact: true }).selectOption('large');
  await until(async () => await page.getByRole('combobox', { name: '模型', exact: true }).inputValue() === 'large', 'effective model change');
  await page.locator('.agent-picker > summary').click();
  await send('wait'); await page.getByRole('button', { name: '停止本轮', exact: true }).waitFor();
  await page.getByRole('button', { name: '新建对话', exact: true }).click();
  await environment('workspace-b'); await send('permission'); await page.getByRole('region', { name: '工具权限请求', exact: true }).waitFor();
  const b = await sessionFor('workspace-b'); assert.ok(b); assert.equal((await sessionFor('workspace-a')).status, 'ACTIVE');
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('draft B retained');
  await until(async () => !await page.getByText('正在保存草稿与会话选择，请勿清除浏览器数据。', { exact: true }).isVisible(), 'draft B durable');
  await selection(canary.slice(0, 20)); await page.getByRole('textbox', { name: '消息', exact: true }).fill('draft A retained');
  await until(async () => !await page.getByText('正在保存草稿与会话选择，请勿清除浏览器数据。', { exact: true }).isVisible(), 'draft A durable');
  await selection('permission');
  assert.equal(await page.getByRole('textbox', { name: '消息', exact: true }).inputValue(), 'draft B retained');
  await page.reload({ waitUntil: 'networkidle' });
  await page.getByRole('region', { name: '工具权限请求', exact: true }).waitFor({ timeout: 30_000 });
  assert.equal(await page.getByRole('textbox', { name: '消息', exact: true }).inputValue(), 'draft B retained');
  assert.equal(registrations.length, 1, 'Refresh must reuse the existing endpoint');
  await page.getByRole('button', { name: '允许此次 · Allow once', exact: true }).click();
  await page.getByRole('button', { name: '确认授权', exact: true }).click(); await completed(1);
  await send('continue B'); await completed(2);
  await selection(canary.slice(0, 20));
  assert.equal(await page.getByRole('textbox', { name: '消息', exact: true }).inputValue(), 'draft A retained');
  await page.locator('.agent-picker > summary').click();
  assert.equal(await page.getByRole('combobox', { name: '模型', exact: true }).inputValue(), 'large');
  await page.locator('.agent-picker > summary').click();
  await page.getByRole('button', { name: '停止本轮', exact: true }).click(); await completed(2);
  await send('continue A'); await completed(3);
  assert.equal((await stack.audit('workspace-a')).length, 3); assert.equal((await stack.audit('workspace-b')).length, 2);
  report.scenarios.push('Two live conversations, independent model/drafts/events, refresh during permission, one approval, cancel and continue: passed.');
  await page.screenshot({ path: path.join(output, 'durable-conversation.png') });
  let completeCount = 3;
  for (const mode of ['before-forward', 'before-ack', 'after-ack']) {
    const before = (await stack.audit('workspace-a')).length;
    fault = { mode, session: a.id, captured: null, hit: false };
    await send('wait'); await until(() => fault.hit, `${mode} boundary reached`);
    if (mode === 'after-ack') await page.getByText('消息已安全送达，正在等待执行结果。', { exact: true }).waitFor();
    const captured = fault.captured; assert.ok(captured);
    await page.reload({ waitUntil: 'networkidle' });
    await page.getByRole('button', { name: '停止本轮', exact: true }).waitFor({ timeout: 30_000 });
    await until(async () => (await stack.audit('workspace-a')).length === before + 1, `${mode} execution count`);
    const attempts = sent.filter((item) => item.message === captured.message);
    assert.equal(attempts.length, mode === 'after-ack' ? 1 : 2, 'Transport recovery count');
    for (const attempt of attempts) assert.deepEqual(attempt.bytes, captured.bytes, 'Recovery must replay the original bytes');
    fault = null;
    await page.getByRole('button', { name: '停止本轮', exact: true }).click(); await completed(++completeCount);
    const audit = await stack.audit('workspace-a'); assert.equal(new Set(audit.map((item) => item.id)).size, audit.length);
    report.scenarios.push(`${mode}: original packet continuity and one fixture execution across browser refresh passed.`);
  }
  const follower = await context.newPage(); observePage(follower);
  const followerPosts = []; const followerSockets = [];
  follower.on('request', (request) => { if (request.method() === 'POST') followerPosts.push(new URL(request.url()).pathname); });
  follower.on('websocket', (socket) => followerSockets.push(new URL(socket.url()).pathname));
  await follower.goto(stack.base, { waitUntil: 'networkidle' });
  await follower.getByText(/另一个标签页正在使用此浏览器的安全连接/).waitFor();
  assert.deepEqual(followerPosts, []); assert.deepEqual(followerSockets, []);
  await page.close(); page = follower;
  await until(async () => await page.locator('.conversation-item').count() === 2 && !await page.getByText(/另一个标签页正在使用此浏览器的安全连接/).isVisible(), 'successor restores both conversations');
  assert.equal(registrations.length, 1);
  await send('successor turn'); await completed(++completeCount);
  report.scenarios.push('Second tab cannot rotate/connect; successor restores both histories and continues the same endpoint: passed.');
  await stack.assertOpaque(canary);
  assert.equal(await page.evaluate(() => localStorage.length), 0);
  report.scenarios.push('Canary absent from Platform database/WAL/Admin/Gateway output; ordinary localStorage remains empty: passed.');
  await page.setViewportSize({ width: 390, height: 844 });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.screenshot({ path: path.join(output, 'durable-mobile.png') });
  await page.setViewportSize({ width: 320, height: 700 });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await selection('permission'); await page.getByRole('button', { name: '结束会话', exact: true }).click();
  await page.getByRole('button', { name: '确认结束', exact: true }).click();
  await until(async () => (await sessionFor('workspace-b')).status === 'CLOSED', 'only B closes');
  assert.equal((await sessionFor('workspace-a')).status, 'ACTIVE');
  await stack.adminRequest(`/admin/api/harness/v1/endpoints/${a.hcEndpointId}/revoke`, 'POST', {});
  await page.reload({ waitUntil: 'networkidle' });
  await page.getByText(/已有登录无法恢复/).first().waitFor();
  assert.equal(registrations.length, 1);
  report.scenarios.push('Close B preserves A; revocation rejects refresh without re-registration or new task: passed.');
  const unsupported = await browser.newContext();
  await unsupported.addInitScript(() => { Object.defineProperty(navigator, 'locks', { value: undefined }); });
  const unsupportedPage = await unsupported.newPage(); const forbidden = [];
  unsupportedPage.on('request', (request) => { if (request.method() === 'POST') forbidden.push('mutation'); });
  unsupportedPage.on('websocket', () => forbidden.push('connection'));
  await unsupportedPage.goto(stack.base, { waitUntil: 'networkidle' });
  await unsupportedPage.getByText(/此浏览器无法独占安全端点/).waitFor(); assert.deepEqual(forbidden, []);
  await unsupported.close(); assert.deepEqual(errors, []); assert.deepEqual(stack.transportErrors, []);
  report.scenarios.push('Unavailable Web Locks fails closed before any login/refresh/ticket/connection: passed.');
  report.status = 'passed';
  console.log(`Production HC browser acceptance passed: ${report.scenarios.length} scenario groups; actual Gateway/ABA, deterministic ACP, no live-model claim.`);
} catch (error) {
  report.status = 'failed'; report.error = String(error?.stack ?? error).replaceAll(stack?.password ?? '\0', '[redacted]').replace(/\b[A-Za-z0-9_-]{32,}\b/gu, '[redacted]');
  console.error(report.error); process.exitCode = 1;
} finally {
  await browser?.close(); await loader.close(); await stack?.stop();
  await writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
}
