#!/usr/bin/env node
// Optional real Firefox validation. Requires npm ci and a Playwright Firefox build.
// npm exec playwright install firefox
// FIREFOX_EXECUTABLE=/path/to/playwright/firefox node scripts/firefox-extension-lab.mjs
// Only disposable browser preferences and owned loopback listeners are changed.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { firefox } from 'playwright';

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const extension = join(repository, 'apps/extension/dist/firefox');
const manifest = JSON.parse(readFileSync(join(extension, 'manifest.json'), 'utf8'));
const addonId = manifest.browser_specific_settings.gecko.id;
const executable = process.env.FIREFOX_EXECUTABLE || firefox.executablePath();
assert(existsSync(executable), 'Install Playwright Firefox or set FIREFOX_EXECUTABLE to its executable.');
const output = resolve(process.env.MESHWAY_FIREFOX_EVIDENCE || join(repository, 'artifacts/firefox-extension-validation.json'));
const sockets = new Set();
const servers = [];
const profiles = [];
let context;
let rdp;
const started = new Date().toISOString();
const evidence = {
  schema: 1, started_at: started, status: 'running',
  platform: { os: process.platform, arch: process.arch, node: process.version },
  source_commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repository, encoding: 'utf8' }).trim(),
  browser: { executable_sha256: sha256(readFileSync(executable)) },
  extension: { id: addonId, version: manifest.version, files_sha256: {} },
  method: 'Real headless Firefox; native RDP temporary addon and popup DOM; actual browser proxy API and loopback TCP traffic.',
  bounds: [
    'The packaged popup is opened as an extension tab and its actual buttons/checkbox are operated through native RDP.',
    'The SOCKS fixtures forward only reserved test destinations to an owned loopback HTTP origin; this is browser proxy validation, not a Meshway peer-core or public internet test.',
    'Private-window permission is granted only to this addon in the disposable profile with the same ExtensionPermissions.add operation used by about:addons.',
    'Firefox signing/store installation, physical field use, device routes, system DNS and system proxy are outside this test.',
  ],
};
for (const file of readdirSync(extension).sort()) {
  evidence.extension.files_sha256[file] = sha256(readFileSync(join(extension, file)));
}

function sha256(bytes) { return createHash('sha256').update(bytes).digest('hex'); }
function own(socket) {
  sockets.add(socket);
  socket.once('close', () => sockets.delete(socket));
  socket.on('error', () => {});
  return socket;
}
async function listen(server) {
  servers.push(server);
  server.on('connection', own);
  await new Promise((fulfill, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', fulfill);
  });
  return server.address().port;
}
async function reservePort() {
  const reservation = net.createServer();
  await new Promise(fulfill => reservation.listen(0, '127.0.0.1', fulfill));
  const port = reservation.address().port;
  await new Promise(fulfill => reservation.close(fulfill));
  return port;
}

// Native Firefox RDP uses byte-length-prefixed JSON and unsolicited events.
class FirefoxRDP {
  constructor(port) {
    this.socket = own(net.connect(port, '127.0.0.1'));
    this.buffer = Buffer.alloc(0);
    this.packets = [];
    this.waiters = [];
    this.socket.on('data', chunk => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      while (true) {
        const colon = this.buffer.indexOf(58);
        if (colon < 0) break;
        const size = Number(this.buffer.subarray(0, colon).toString());
        assert(Number.isSafeInteger(size) && size >= 0 && size <= 5 * 1024 * 1024, 'Invalid RDP frame.');
        if (this.buffer.length < colon + 1 + size) break;
        const packet = JSON.parse(this.buffer.subarray(colon + 1, colon + 1 + size).toString());
        this.buffer = this.buffer.subarray(colon + 1 + size);
        const index = this.waiters.findIndex(waiter => waiter.match(packet));
        if (index < 0) this.packets.push(packet);
        else {
          const [waiter] = this.waiters.splice(index, 1);
          clearTimeout(waiter.timer);
          waiter.fulfill(packet);
        }
      }
    });
  }
  next(match) {
    return new Promise((fulfill, reject) => {
      const index = this.packets.findIndex(match);
      if (index >= 0) { fulfill(this.packets.splice(index, 1)[0]); return; }
      const waiter = { match, fulfill };
      waiter.timer = setTimeout(() => {
        this.waiters.splice(this.waiters.indexOf(waiter), 1);
        reject(new Error('Firefox RDP timed out.'));
      }, 15_000);
      this.waiters.push(waiter);
    });
  }
  async request(packet) {
    const response = this.next(value => value.from === packet.to && !value.type);
    const json = JSON.stringify(packet);
    this.socket.write(Buffer.from(`${Buffer.byteLength(json)}:${json}`));
    const value = await response;
    assert(!value.error, value.message || value.error);
    return value;
  }
  async evaluate(actor, body) {
    const reply = await this.request({
      to: actor, type: 'evaluateJSAsync', text: `(async()=>{${body}})()`,
      mapped: { await: true }, disableBreaks: true,
    });
    const value = await this.next(packet => packet.from === actor && packet.type === 'evaluationResult' && packet.resultID === reply.resultID);
    assert(!value.hasException && !value.topLevelAwaitRejected, value.exceptionMessage || 'Firefox evaluation rejected.');
    return value.result;
  }
  close() { this.socket.destroy(); }
}

function socksFixture(label, originPort, requests) {
  return net.createServer(socket => {
    socket.setTimeout(15_000, () => socket.destroy());
    let buffer = Buffer.alloc(0);
    let stage = 'greeting';
    const parse = chunk => {
      buffer = Buffer.concat([buffer, chunk]);
      if (stage === 'greeting') {
        if (buffer.length < 2 || buffer.length < 2 + buffer[1]) return;
        if (buffer[0] !== 5 || !buffer.subarray(2, 2 + buffer[1]).includes(0)) { socket.destroy(); return; }
        buffer = buffer.subarray(2 + buffer[1]);
        socket.write(Buffer.from([5, 0]));
        stage = 'connect';
      }
      if (stage !== 'connect' || buffer.length < 5) return;
      const atyp = buffer[3];
      const addressBytes = atyp === 1 ? 4 : atyp === 3 ? 1 + buffer[4] : 0;
      if (!addressBytes || buffer.length < 4 + addressBytes + 2) return;
      const host = atyp === 1 ? Array.from(buffer.subarray(4, 8)).join('.') : buffer.subarray(5, 4 + addressBytes).toString();
      const port = buffer.readUInt16BE(4 + addressBytes);
      if (buffer[0] !== 5 || buffer[1] !== 1 || buffer[2] !== 0 || port !== 80 || !['meshway-firefox.invalid', '192.0.2.1'].includes(host)) {
        socket.destroy(); return;
      }
      requests.push({ fixture: label, socks_version: 5, command: 'CONNECT', address_type: atyp === 3 ? 'domain' : 'IPv4', host, port });
      const remainder = buffer.subarray(4 + addressBytes + 2);
      stage = 'forward';
      socket.removeListener('data', parse);
      socket.pause();
      const upstream = own(net.connect(originPort, '127.0.0.1'));
      upstream.once('connect', () => {
        socket.write(Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 0, 0]));
        if (remainder.length) upstream.write(remainder);
        socket.pipe(upstream); upstream.pipe(socket); socket.resume();
      });
      socket.once('close', () => upstream.destroy());
      upstream.once('error', () => socket.destroy());
    };
    socket.on('data', parse);
  });
}

const popupState = `
  const deadline=Date.now()+10000;
  while(document.getElementById('toggle').disabled){
    if(Date.now()>deadline)throw new Error('Popup initialization/mutation timed out.');
    await new Promise(resolve=>setTimeout(resolve,25));
  }
  return JSON.stringify({
    label:document.getElementById('status-label').textContent,
    error:document.getElementById('error').hidden ? null : document.getElementById('error').textContent,
    consent_checked:document.getElementById('consent').checked,
    proxy:await browser.proxy.settings.get({}),
    recovery_present:Object.hasOwn(await browser.storage.local.get('meshwayProxyRecovery'),'meshwayProxyRecovery'),
  });`;

async function startBrowser(originalPort, grantPrivatePermission) {
  const profile = mkdtempSync(join(tmpdir(), 'meshway-firefox-extension-'));
  profiles.push(profile);
  const debugPort = await reservePort();
  context = await firefox.launchPersistentContext(profile, {
    executablePath: executable, headless: true,
    args: [`--start-debugger-server=${debugPort}`],
    firefoxUserPrefs: {
      'devtools.debugger.remote-enabled': true, 'devtools.debugger.prompt-connection': false,
      'devtools.chrome.enabled': true, 'devtools.debugger.force-local': true,
      // A real nondefault prior browser proxy makes restoration observable.
      'network.proxy.type': 1, 'network.proxy.socks': '127.0.0.1',
      'network.proxy.socks_port': originalPort, 'network.proxy.socks_version': 5,
      'network.proxy.socks_remote_dns': false, 'network.proxy.socks5_remote_dns': false,
      'network.proxy.no_proxies_on': 'localhost, 127.0.0.1, ::1, preserved.invalid',
      'network.proxy.failover_direct': false, 'network.dns.disablePrefetch': true,
      'network.prefetch-next': false, 'network.predictor.enabled': false,
      'network.http.speculative-parallel-limit': 0,
    },
  });
  rdp = new FirefoxRDP(debugPort);
  await rdp.next(packet => packet.applicationType === 'browser');
  const root = await rdp.request({ to: 'root', type: 'getRoot' });
  const descriptor = await rdp.request({ to: 'root', type: 'getProcess', id: 0 });
  const parent = (await rdp.request({ to: descriptor.processDescriptor.actor, type: 'getTarget' })).process;
  if (grantPrivatePermission) {
    await rdp.evaluate(parent.consoleActor, `
      const {ExtensionPermissions}=ChromeUtils.importESModule('resource://gre/modules/ExtensionPermissions.sys.mjs');
      await ExtensionPermissions.add(${JSON.stringify(addonId)},{permissions:['internal:privateBrowsingAllowed'],origins:[]});
      return 'granted';`);
  }
  await rdp.request({ to: root.addonsActor, type: 'installTemporaryAddon', addonPath: extension });
  const addons = await rdp.request({ to: 'root', type: 'listAddons' });
  const addon = addons.addons.find(value => value.id === addonId);
  assert(addon?.temporarilyInstalled && addon.debuggable && addon.isWebExtension);
  assert.deepEqual(addon.warnings, []);
  const watcher = await rdp.request({ to: addon.actor, type: 'getWatcher', isServerTargetSwitchingEnabled: true });
  await rdp.request({ to: watcher.actor, type: 'watchTargets', targetType: 'frame' });
  const background = (await rdp.next(packet => packet.type === 'target-available-form' && packet.target.addonId === addonId && packet.target.url.endsWith('/_generated_background_page.html'))).target;
  const privateAllowed = await rdp.evaluate(background.consoleActor, 'return await browser.extension.isAllowedIncognitoAccess();');
  assert.equal(privateAllowed, grantPrivatePermission);
  await rdp.evaluate(background.consoleActor, 'await browser.tabs.create({url:browser.runtime.getURL("popup.html")}); return "opened";');
  const popup = (await rdp.next(packet => packet.type === 'target-available-form' && packet.target.addonId === addonId && packet.target.url.endsWith('/popup.html'))).target;
  const state = () => rdp.evaluate(popup.consoleActor, popupState).then(JSON.parse);
  return { profile, popup, state, addon, privateAllowed };
}

async function closeBrowser() {
  rdp?.close(); rdp = undefined;
  if (context) { const ownedContext = context; context = undefined; await ownedContext.close(); }
  for (const profile of profiles) rmSync(profile, { recursive: true, force: true });
}
async function cleanup() {
  await closeBrowser();
  for (const socket of sockets) socket.destroy();
  await Promise.all(servers.filter(server => server.listening).map(server => new Promise(fulfill => server.close(fulfill))));
  evidence.cleanup = {
    browser_closed: !context, profiles_deleted: profiles.every(profile => !existsSync(profile)),
    loopback_listeners_stopped: servers.every(server => !server.listening),
    host_routes_dns_system_proxy_changes: false,
  };
}
process.once('SIGINT', () => { cleanup().finally(() => process.exit(130)); });
process.once('SIGTERM', () => { cleanup().finally(() => process.exit(143)); });

try {
  const originRequests = [];
  const origin = http.createServer((request, response) => {
    originRequests.push({ path: request.url, host: request.headers.host });
    response.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store', Connection: 'close' });
    // A data favicon avoids an unrelated automatic request racing proxy-count assertions.
    response.end(`<!doctype html><html><head><link rel="icon" href="data:,"></head><body>meshway-firefox-runtime:${request.url}</body></html>`);
  });
  const originPort = await listen(origin);
  const socksRequests = [];
  const originalPort = await listen(socksFixture('previous-browser-proxy', originPort, socksRequests));
  const meshwayPort = await listen(socksFixture('meshway-browser-proxy', originPort, socksRequests));

  // Firefox refuses global proxy writes without this user-granted addon permission.
  const ungranted = await startBrowser(originalPort, false);
  const initial = await ungranted.state();
  assert.equal(initial.label, 'Browser proxy is off');
  await rdp.evaluate(ungranted.popup.consoleActor, `document.getElementById('consent').click(); document.getElementById('port').value=${JSON.stringify(String(meshwayPort))}; document.getElementById('toggle').click(); return 'clicked';`);
  const denied = await ungranted.state();
  assert.match(denied.error, /proxy\.settings requires private browsing permission/);
  assert.deepEqual(denied.proxy, initial.proxy);
  assert.equal(denied.recovery_present, false);
  evidence.private_permission_gate = { allowed: false, error: denied.error, settings_unchanged: true, recovery_removed: true };
  await closeBrowser();

  const granted = await startBrowser(originalPort, true);
  evidence.browser.version = await rdp.evaluate(granted.popup.consoleActor, 'return navigator.userAgent;');
  evidence.extension.temporary_install = true;
  evidence.extension.firefox_warnings = granted.addon.warnings;
  evidence.private_permission_granted = granted.privateAllowed;
  const before = await granted.state();
  assert.equal(before.label, 'Browser proxy is off');
  assert.equal(before.proxy.value.socks, `127.0.0.1:${originalPort}`);
  assert.equal(before.proxy.value.proxyDNS, false);
  const page = await context.newPage();
  const navigate = async url => {
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 15_000 });
    return page.locator('body').innerText();
  };
  assert.equal(await navigate('http://192.0.2.1/before'), 'meshway-firefox-runtime:/before');
  assert(socksRequests.some(value => value.fixture === 'previous-browser-proxy' && value.host === '192.0.2.1'));

  await rdp.evaluate(granted.popup.consoleActor, "document.getElementById('toggle').click(); return 'clicked';");
  const noConsent = await granted.state();
  assert.match(noConsent.error, /Please confirm/);
  assert.deepEqual(noConsent.proxy, before.proxy);
  assert.equal(noConsent.recovery_present, false);
  const backgroundDenial = JSON.parse(await rdp.evaluate(granted.popup.consoleActor, `return JSON.stringify(await browser.runtime.sendMessage({type:'enable',host:'127.0.0.1',port:${JSON.stringify(String(meshwayPort))},consent:false}));`));
  assert.equal(backgroundDenial.ok, false);
  assert.match(backgroundDenial.error, /Explicit consent/);
  evidence.consent = { popup_rejected: true, background_rejected: true, settings_unchanged: true };

  await rdp.evaluate(granted.popup.consoleActor, `document.getElementById('consent').click(); document.getElementById('port').value=${JSON.stringify(String(meshwayPort))}; document.getElementById('toggle').click(); return 'clicked';`);
  const enabled = await granted.state();
  assert.equal(enabled.label, 'Browser proxy enabled');
  assert.equal(enabled.proxy.levelOfControl, 'controlled_by_this_extension');
  assert.equal(enabled.proxy.value.proxyType, 'manual');
  assert.equal(enabled.proxy.value.socks, `127.0.0.1:${meshwayPort}`);
  assert.equal(enabled.proxy.value.socksVersion, 5);
  assert.equal(enabled.proxy.value.proxyDNS, true);
  assert.equal(enabled.proxy.value.passthrough, 'localhost, 127.0.0.1, ::1');
  assert.equal(enabled.recovery_present, true);
  assert.equal(await navigate('http://meshway-firefox.invalid/proxied'), 'meshway-firefox-runtime:/proxied');
  assert(socksRequests.some(value => value.fixture === 'meshway-browser-proxy' && value.address_type === 'domain' && value.host === 'meshway-firefox.invalid'));
  const countBeforeBypass = socksRequests.length;
  assert.equal(await navigate(`http://127.0.0.1:${originPort}/bypass`), 'meshway-firefox-runtime:/bypass');
  assert.equal(socksRequests.length, countBeforeBypass);

  await rdp.evaluate(granted.popup.consoleActor, "document.getElementById('toggle').click(); return 'clicked';");
  const after = await granted.state();
  assert.equal(after.label, 'Browser proxy is off');
  assert.deepEqual(after.proxy, before.proxy);
  assert.equal(after.recovery_present, false);
  assert.equal(after.consent_checked, false);
  const priorAfter = socksRequests.filter(value => value.fixture === 'previous-browser-proxy').length;
  const meshwayAfter = socksRequests.filter(value => value.fixture === 'meshway-browser-proxy').length;
  assert.equal(await navigate('http://192.0.2.1/after'), 'meshway-firefox-runtime:/after');
  assert.equal(socksRequests.filter(value => value.fixture === 'previous-browser-proxy').length, priorAfter + 1);
  assert.equal(socksRequests.filter(value => value.fixture === 'meshway-browser-proxy').length, meshwayAfter);
  evidence.proxy = { before: before.proxy, enabled: enabled.proxy, after: after.proxy, exact_restoration: true, recovery_removed: true };
  evidence.traffic = { socks_requests: socksRequests, origin_requests: originRequests, domain_dns_through_socks: true, loopback_bypass: true, restored_proxy_used: true };
  evidence.status = 'passed';
} catch (error) {
  evidence.status = 'failed';
  evidence.error = error.message;
  process.exitCode = 1;
} finally {
  try { await cleanup(); }
  catch (error) { evidence.cleanup_error = error.message; evidence.status = 'failed'; process.exitCode = 1; }
  evidence.finished_at = new Date().toISOString();
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, `${JSON.stringify(evidence, null, 2)}\n`);
}
console.log(`Firefox extension validation ${evidence.status}: ${output}`);
