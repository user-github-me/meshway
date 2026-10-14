import { test, expect, chromium } from '@playwright/test';
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';

const repository = resolve(import.meta.dirname, '../../..');
const screenshots = resolve(repository, 'artifacts/screenshots');
let work: string;
let daemon: ChildProcess;
let exitDaemon: ChildProcess;
let personalDaemon: ChildProcess;
let apiToken: string;
let peerID: string;
let serverID: string;
let serverAddress: string;
let personalID: string;
let personalAddress: string;
let binary: string;
let configPath: string;
let apiOrigin: string;
let personalOrigin: string;

function startDaemon(path: string): Promise<{ process: ChildProcess; status: { peer_id: string; addresses: string[]; api_listen: string } }> {
  const child = spawn(binary, ['daemon', '--config', path], { stdio: 'pipe' });
  return new Promise((resolve, reject) => {
    let output = '', diagnostics = '';
    const timer = setTimeout(() => { child.kill('SIGTERM'); reject(new Error(`Test daemon startup timeout: ${diagnostics}`)); }, 15_000);
    child.stderr?.on('data', chunk => { diagnostics += String(chunk); });
    child.once('error', error => { clearTimeout(timer); reject(error); });
    child.once('exit', code => { clearTimeout(timer); reject(new Error(`Test daemon exited (${code}): ${diagnostics}`)); });
    child.stdout?.on('data', chunk => {
      output += String(chunk);
      try { const status = JSON.parse(output); if (!status.api_listen || !status.peer_id) return; clearTimeout(timer); resolve({ process: child, status }); } catch {}
    });
  });
}

test.beforeAll(async () => {
  mkdirSync(screenshots, { recursive: true });
  execFileSync(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['run', 'build', '--workspace', '@meshway/extension'], { cwd: repository, stdio: 'pipe', timeout: 60_000 });
  work = mkdtempSync(resolve(tmpdir(), 'meshway-web-'));
  binary = resolve(work, 'meshway');
  const localGo = resolve(repository, '.tools/go/bin/go');
  execFileSync(process.env.GO_BINARY || (existsSync(localGo) ? localGo : 'go'), ['build', '-o', binary, './cmd/meshway'], { cwd: repository, stdio: 'pipe', timeout: 180_000 });
  configPath = resolve(work, 'config.json');
  execFileSync(binary, ['init', '--config', configPath], { stdio: 'pipe' });
  const config = JSON.parse(readFileSync(configPath, 'utf8'));
  config.listen_addresses = ['/ip4/127.0.0.1/tcp/0'];
  config.api_listen = '127.0.0.1:0'; config.socks_listen = '127.0.0.1:0'; config.allowed_origins = ['http://127.0.0.1:5173'];
  apiToken = config.api_token;
  writeFileSync(configPath, JSON.stringify(config), { mode: 0o600 });
  const clientStart = await startDaemon(configPath); daemon = clientStart.process; peerID = clientStart.status.peer_id; apiOrigin = `http://${clientStart.status.api_listen}`;
  const exitConfigPath = resolve(work, 'exit', 'config.json');
  execFileSync(binary, ['init', '--config', exitConfigPath], { stdio: 'pipe' });
  const exitConfig = JSON.parse(readFileSync(exitConfigPath, 'utf8'));
  exitConfig.listen_addresses = ['/ip4/127.0.0.1/tcp/0']; exitConfig.api_listen = '127.0.0.1:0'; exitConfig.socks_listen = '';
  exitConfig.exit = true; exitConfig.advertise = true; exitConfig.server_label = 'Browser test exit'; exitConfig.server_country = 'DE'; exitConfig.allowed_peers = [peerID];
  writeFileSync(exitConfigPath, JSON.stringify(exitConfig), { mode: 0o600 });
  const exitStart = await startDaemon(exitConfigPath); exitDaemon = exitStart.process; serverID = exitStart.status.peer_id; serverAddress = exitStart.status.addresses[0];
});

test.afterAll(async () => {
  if (daemon && daemon.exitCode === null) {
    const stopped = new Promise(resolve => daemon.once('exit', resolve));
    daemon.kill('SIGTERM'); await stopped;
  }
  if (exitDaemon && exitDaemon.exitCode === null) { const stopped = new Promise(resolve => exitDaemon.once('exit', resolve)); exitDaemon.kill('SIGTERM'); await stopped; }
  if (personalDaemon && personalDaemon.exitCode === null) { const stopped = new Promise(resolve => personalDaemon.once('exit', resolve)); personalDaemon.kill('SIGTERM'); await stopped; }
  if (work) rmSync(work, { recursive: true, force: true });
});

test('public website is responsive, role choices and installation tabs work', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /A network, made of us/ })).toBeVisible();
  await page.screenshot({ path: resolve(screenshots, 'website-desktop.png'), fullPage: true });
  await page.getByRole('tab', { name: 'Internet exit', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Share internet access deliberately.' })).toBeVisible();
  await page.getByRole('tab', { name: 'npm', exact: true }).click();
  await expect(page.locator('.terminal-card pre')).toContainText('meshway install-core');
  await expect(page.getByRole('link', { name: 'Android' }).first()).toHaveAttribute('href', /github\.com\/user-github-me\/meshway\/releases\/download\/v0\.1\.0-alpha\.1\/meshway-android-debug\.apk$/);
  await page.getByRole('link', { name: 'Get started', exact: true }).click();
  await expect(page.locator('#get-started')).toBeInViewport();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await page.getByRole('link', { name: 'Documentation' }).first().click();
  await expect(page.getByRole('heading', { name: /Start small/ })).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  await page.goto('/');
  await page.screenshot({ path: resolve(screenshots, 'website-mobile.png'), fullPage: true });
});

test('dashboard pairs with a real daemon, shows its account, validates errors and forgets the token', async ({ page }) => {
  await page.goto('/#/dashboard');
  await expect(page.getByRole('heading', { name: 'Start your local connection' })).toBeVisible();
  await page.getByLabel('Daemon API address').fill(apiOrigin);
  await page.getByLabel('Local API token').fill('wrong-token');
  await page.getByRole('button', { name: 'Pair daemon', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('bearer token required');
  await page.getByLabel('Local API token').fill(apiToken);
  await page.getByRole('button', { name: 'Pair daemon', exact: true }).click();
  await expect(page.getByText('Daemon paired', { exact: true })).toBeVisible();
  await page.getByText('Advanced node details', { exact: true }).click();
  await expect(page.locator('.connection-details')).toContainText(peerID);
  await expect(page.locator('.account-details')).toContainText('Off · local only');
  await expect(page.locator('.device-list')).toContainText('Authorized');
  await expect(page.getByRole('button', { name: 'Sync records', exact: true })).toBeDisabled();
  await page.getByLabel('Full peer multiaddress').fill('invalid-address');
  await page.getByRole('button', { name: 'Connect peer', exact: true }).click();
  await expect(page.getByRole('alert').first()).toBeVisible();
  await page.getByRole('button', { name: 'Dismiss error' }).click();
  await page.getByLabel('Full peer multiaddress').fill('');
  await page.locator('.advanced-settings > summary').click();
  await page.screenshot({ path: resolve(screenshots, 'dashboard-desktop.png'), fullPage: true });
  expect(await page.evaluate(() => ({ session: sessionStorage.length, local: localStorage.length }))).toEqual({ session: 0, local: 0 });
  await page.getByRole('link', { name: 'Account & devices' }).click();
  await expect(page.locator('#account')).toBeInViewport();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  await page.screenshot({ path: resolve(screenshots, 'dashboard-mobile.png'), fullPage: true });
  await page.locator('.advanced-settings > summary').click();
  await page.getByRole('button', { name: 'Disconnect dashboard' }).click();
  await expect(page.getByLabel('Local API token')).toHaveValue('');
  await page.getByLabel('Daemon API address').fill('http://example.com:7777');
  await page.getByLabel('Local API token').fill('test-token');
  await page.getByRole('button', { name: 'Pair daemon', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Use an HTTP loopback address');
  await page.reload();
  await expect(page.getByLabel('Local API token')).toHaveValue('');
});

test('advertised server cards and opt-in map select a real authorized exit', async ({ page }) => {
  await page.goto('/#/dashboard');
  await page.getByLabel('Daemon API address').fill(apiOrigin); await page.getByLabel('Local API token').fill(apiToken);
  await page.getByRole('button', { name: 'Pair daemon', exact: true }).click();
  await expect(page.getByText('Daemon paired', { exact: true })).toBeVisible();
  await page.getByText('Add a trusted peer invitation', { exact: true }).click();
  await page.getByLabel('Trusted peer invitation').fill(serverAddress);
  await page.getByRole('button', { name: 'Add peer', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Browser test exit' })).toBeVisible();
  await page.getByRole('button', { name: 'Map', exact: true }).click();
  await expect(page.getByRole('img', { name: 'Map with country locations shared by available peers' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Select Browser test exit' })).toBeVisible();
  await page.getByRole('button', { name: 'Connect', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Disconnect', exact: true })).toBeVisible();
  const response = await fetch(`${apiOrigin}/v1/status`, { headers: { Authorization: `Bearer ${apiToken}` } });
  expect((await response.json()).upstream_peer).toEqual(serverID);
  await page.screenshot({ path: resolve(screenshots, 'dashboard-map.png'), fullPage: true });
  await page.getByRole('button', { name: 'List', exact: true }).click();
  await expect(page.locator('.server-grid')).toContainText('Connected to this server');
  await page.screenshot({ path: resolve(screenshots, 'dashboard-connections.png'), fullPage: true });
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible();
  const disconnected = await fetch(`${apiOrigin}/v1/status`, { headers: { Authorization: `Bearer ${apiToken}` } });
  expect((await disconnected.json()).upstream_peer).toEqual('');
});

test('Chromium extension opts in and restores actual browser proxy settings', async () => {
  test.setTimeout(60_000);
  const profile = mkdtempSync(resolve(tmpdir(), 'meshway-extension-'));
  const extension = resolve(repository, 'apps/extension/dist/chrome');
  const context = await chromium.launchPersistentContext(profile, {
    headless: true, channel: 'chromium', executablePath: process.env.CHROMIUM_EXECUTABLE,
    // Recent Chromium enables extension debugging explicitly. This switch is
    // confined to the disposable profile and the one locally built extension.
    ignoreDefaultArgs: ['--disable-extensions'],
    args: ['--enable-unsafe-extension-debugging', `--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
  });
  try {
    const worker = await test.step('load the isolated extension service worker', async () => context.serviceWorkers()[0] || await context.waitForEvent('serviceworker', { timeout: 15_000 }));
    const id = new URL(worker.url()).host;
    const original = await worker.evaluate(() => new Promise<unknown>(resolve => chrome.proxy.settings.get({ incognito: false }, resolve)));
    const page = await context.newPage();
    await page.goto(`chrome-extension://${id}/popup.html`);
    await expect(page.getByText('Browser proxy is off', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Enable browser proxy' }).click();
    await expect(page.getByRole('alert')).toContainText('Please confirm');
    await page.locator('#consent').check();
    await page.getByRole('button', { name: 'Enable browser proxy' }).click();
    await expect(page.getByText('Browser proxy enabled', { exact: true })).toBeVisible();
    const applied = await worker.evaluate(() => new Promise<any>(resolve => chrome.proxy.settings.get({ incognito: false }, resolve)));
    expect(applied.value.rules.singleProxy).toEqual({ scheme: 'socks5', host: '127.0.0.1', port: 1080 });
    await page.screenshot({ path: resolve(screenshots, 'extension.png') });
    await page.getByRole('button', { name: 'Disable & restore settings' }).click();
    await expect(page.getByText('Browser proxy is off', { exact: true })).toBeVisible();
    const restored = await worker.evaluate(() => new Promise<unknown>(resolve => chrome.proxy.settings.get({ incognito: false }, resolve)));
    expect(restored).toEqual(original);
  } finally { await context.close(); rmSync(profile, { recursive: true, force: true }); }
});

test('personal VPS same recovery key is private and connects without a manual peer allowlist', async ({ page }) => {
  // Offline key export/import only touches temporary test state. Real host routes/DNS/proxies are untouched.
  const stopped = new Promise(resolve => daemon.once('exit', resolve)); daemon.kill('SIGTERM'); await stopped;
  const recovery = resolve(work, 'recovery.txt');
  execFileSync(binary, ['account', 'export', '--config', configPath, '--output', recovery], { stdio: 'pipe' });
  const personalConfigPath = resolve(work, 'personal', 'config.json');
  execFileSync(binary, ['setup-server', '--personal', '--config', personalConfigPath], { stdio: 'pipe' });
  execFileSync(binary, ['account', 'import', '--config', personalConfigPath, '--input', recovery, '--force'], { stdio: 'pipe' });
  rmSync(recovery);
  const personalConfig = JSON.parse(readFileSync(personalConfigPath, 'utf8'));
  personalConfig.listen_addresses = ['/ip4/127.0.0.1/tcp/0']; personalConfig.api_listen = '127.0.0.1:0'; personalConfig.socks_listen = '';
  personalConfig.server_label = 'My private VPS'; personalConfig.server_country = 'SG';
  expect(personalConfig.allowed_peers).toEqual([]);
  writeFileSync(personalConfigPath, JSON.stringify(personalConfig), { mode: 0o600 });
  const clientStart = await startDaemon(configPath); daemon = clientStart.process; apiOrigin = `http://${clientStart.status.api_listen}`;
  const personalStart = await startDaemon(personalConfigPath); personalDaemon = personalStart.process; personalID = personalStart.status.peer_id; personalAddress = personalStart.status.addresses[0]; personalOrigin = `http://${personalStart.status.api_listen}`;
  await page.goto('/#/dashboard');
  await page.getByLabel('Daemon API address').fill(apiOrigin); await page.getByLabel('Local API token').fill(apiToken);
  await page.getByRole('button', { name: 'Pair daemon', exact: true }).click();
  await expect(page.getByText('Daemon paired', { exact: true })).toBeVisible();
  await page.getByText('Add a trusted peer invitation', { exact: true }).click();
  await page.getByLabel('Trusted peer invitation').fill(personalAddress); await page.getByRole('button', { name: 'Add peer', exact: true }).click();
  const personalCard = page.locator('.server-choice').filter({ hasText: 'My private VPS' });
  await expect(personalCard).toContainText('Private'); await expect(personalCard).toContainText('Only devices in your account');
  await personalCard.getByRole('button', { name: 'Connect', exact: true }).click();
  await expect(personalCard.getByRole('button', { name: 'Disconnect', exact: true })).toBeVisible();
  const response = await fetch(`${apiOrigin}/v1/status`, { headers: { Authorization: `Bearer ${apiToken}` } }); expect((await response.json()).upstream_peer).toEqual(personalID);
  const serverAccount = await fetch(`${personalOrigin}/v1/account/status`, { headers: { Authorization: `Bearer ${personalConfig.api_token}` } }).then(r => r.json());
  const clientAccount = await fetch(`${apiOrigin}/v1/account/status`, { headers: { Authorization: `Bearer ${apiToken}` } }).then(r => r.json());
  expect(serverAccount.account_id).toEqual(clientAccount.account_id); expect(serverAccount.device_id).not.toEqual(clientAccount.device_id);
  await page.getByRole('button', { name: 'Map', exact: true }).click();
  await page.getByRole('button', { name: 'Select My private VPS' }).click();
  await page.screenshot({ path: resolve(screenshots, 'dashboard-personal.png'), fullPage: true });
  const personalStopped = new Promise(resolve => personalDaemon.once('exit', resolve)); personalDaemon.kill('SIGTERM'); await personalStopped;
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.locator('.map-selected')).toContainText('Selected · connection offline');
  await expect(page.locator('.map-selected .server-choice')).not.toHaveClass(/server-current/);
});
