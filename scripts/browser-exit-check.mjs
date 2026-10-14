#!/usr/bin/env node
import { chromium } from '@playwright/test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { isIP } from 'node:net';

if (process.argv.includes('--help')) {
 console.log('Usage: node scripts/browser-exit-check.mjs --expected-exit-ip SERVER_IP [--proxy socks5://127.0.0.1:1080] [--browser CHROME_PATH] [--output RESULT.json]');
 process.exit(0);
}
const options = new Map();
const allowed = new Set(['--expected-exit-ip', '--proxy', '--output', '--browser']);
for (let i = 2; i < process.argv.length; i += 2) {
  const name = process.argv[i], value = process.argv[i + 1];
  if (!allowed.has(name) || !value || options.has(name)) throw new Error('Usage: node scripts/browser-exit-check.mjs --expected-exit-ip SERVER_IP [--proxy socks5://127.0.0.1:1080] [--browser CHROME_PATH] [--output RESULT.json]');
  options.set(name, value);
}
const expectedIP = options.get('--expected-exit-ip');
if (!expectedIP || !isIP(expectedIP)) throw new Error('Pass the expected server IP with --expected-exit-ip.');
const proxy = options.get('--proxy');
if (proxy) {
  const parsed = new URL(proxy);
  if (parsed.protocol !== 'socks5:' || !['127.0.0.1', '[::1]'].includes(parsed.hostname) || !parsed.port || parsed.username || parsed.password || !['', '/'].includes(parsed.pathname) || parsed.search || parsed.hash) throw new Error('--proxy must be a loopback SOCKS5 URL without credentials.');
  if (Number(parsed.port) < 1024 || Number(parsed.port) > 65535) throw new Error('--proxy requires an unprivileged loopback port.');
}
const output = resolve(options.get('--output') ?? 'browser-exit-check.json');
const execute = promisify(execFile);
async function snapshot() {
  const commands = process.platform === 'darwin' ? [
    ['/usr/sbin/scutil', ['--proxy']], ['/usr/sbin/scutil', ['--dns']],
    ['/sbin/route', ['-n', 'get', 'default']], ['/sbin/route', ['-n', 'get', '-inet6', 'default']],
  ] : process.platform === 'linux' ? [
    ['ip', ['-j', 'route', 'show', 'default']], ['ip', ['-j', '-6', 'route', 'show', 'default']], ['cat', ['/etc/resolv.conf']],
  ] : [];
  return Object.fromEntries(await Promise.all(commands.map(async ([file, args]) => {
    let code = 0, stdout = '';
    try { ({ stdout } = await execute(file, args, { timeout: 4000, maxBuffer: 262144, encoding: 'utf8' })); }
    catch (error) { code = error.code ?? 'failure'; stdout = String(error.stdout ?? ''); }
    return [[file, ...args].join(' '), { code, sha256: createHash('sha256').update(stdout).digest('hex') }];
  })));
}
const proof = {
  date: new Date().toISOString(),
  mode: proxy ? 'isolated browser with explicit loopback SOCKS' : 'isolated browser using current OS proxy preferences',
  expected_exit_ip: expectedIP, checks: {}, request_failures: [], host_network_mutations: false,
};
const before = await snapshot();
const profile = await mkdtemp(join(tmpdir(), 'meshway-browser-exit-'));
let context, failure;
try {
  const launch = {
    headless: true, timeout: 15000,
    args: ['--no-first-run', '--disable-default-apps', '--disable-background-networking'],
  };
  const browser = options.get('--browser') ?? (process.platform === 'darwin' ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : undefined);
  if (browser) launch.executablePath = browser;
  if (proxy) launch.proxy = { server: proxy };
  context = await chromium.launchPersistentContext(profile, launch);
  context.setDefaultNavigationTimeout(20000);
  const page = await context.newPage();
  page.on('requestfailed', request => {
    if (proof.request_failures.length < 16) proof.request_failures.push({ host: new URL(request.url()).hostname, error: request.failure()?.errorText ?? 'request failed' });
  });
  const ipResponse = await page.goto('https://api.ipify.org?format=json');
  if (ipResponse?.status() !== 200) throw new Error('The public IP endpoint failed.');
  const data = JSON.parse(await page.locator('body').innerText());
  if (!isIP(data.ip) || data.ip !== expectedIP) throw new Error('Browser public IP differs from the expected server.');
  proof.actual_exit_ip = data.ip;
  proof.checks.expected_exit_ip = true;
  const dualResponse = await page.goto('https://api64.ipify.org?format=json');
  if (dualResponse?.status() !== 200) throw new Error('The dual-stack public IP endpoint failed.');
  const dual = JSON.parse(await page.locator('body').innerText());
  if (!isIP(dual.ip) || dual.ip !== expectedIP) throw new Error('The dual-stack browser exit differs from the expected server; supply its actual egress IP.');
  proof.dual_stack_exit_ip = dual.ip;
  proof.checks.dual_stack_exit_ip = true;
  const httpsResponse = await page.goto('https://example.com/');
  if (httpsResponse?.status() !== 200 || !(await page.title()).includes('Example Domain')) throw new Error('Certificate-verified public browser HTTPS failed.');
  proof.checks.browser_https = true;
} catch (error) {
  failure = error;
  proof.error = error.message;
} finally {
  if (context) await context.close();
  await rm(profile, { recursive: true, force: true });
  proof.checks.temporary_browser_closed = true;
  const after = await snapshot();
  proof.host_network_baseline = { before, after };
  proof.checks.host_network_baseline_preserved = JSON.stringify(before) === JSON.stringify(after);
  if (!Object.keys(before).length) proof.baseline_limitation = 'No platform snapshot adapter; this script still performs no route, resolver or OS proxy writes.';
  if (!proof.checks.host_network_baseline_preserved && !failure) failure = new Error('Host networking changed during the check; inspect concurrent changes.');
  proof.passed = !failure;
  await writeFile(output, JSON.stringify(proof, null, 2) + '\n', { mode: 0o600 });
}
console.log(JSON.stringify({ passed: proof.passed, checks: proof.checks, output }));
if (failure) process.exitCode = 1;
