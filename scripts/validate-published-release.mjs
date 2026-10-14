import { createHash } from 'node:crypto';
import { mkdtemp, readFile, writeFile, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';

// Explicit invocation downloads only the declared public release into a
// private temporary npm prefix/cache. It never starts a daemon or changes routes.
if (process.platform === 'win32') throw new Error('Run this npm-prefix validation on macOS or Linux. Windows has its separate native runtime lab.');
const root = fileURLToPath(new URL('../', import.meta.url));
const { verifyRelease } = await import(pathToFileURL(resolve(root, 'packages/cli/src/release.js')));
const sha = data => createHash('sha256').update(data).digest('hex');
const base = 'https://github.com/user-github-me/meshway/releases/download/v0.1.0-alpha.1/';
async function get(name, limit) {
  let url = new URL(name, base);
  for (let index = 0; index <= 5; index++) {
    const response = await fetch(url, { redirect: 'manual', signal: AbortSignal.timeout(90_000) });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get('location');
      if (!location) throw new Error('Redirect without location');
      url = new URL(location, url);
      if (url.protocol !== 'https:' || url.username || url.password) throw new Error('Unsafe redirect');
      await response.body?.cancel();
      continue;
    }
    if (!response.ok) throw new Error(`${name}: HTTP ${response.status}`);
    const parts = []; let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > limit) throw new Error('Oversize artifact');
      parts.push(Buffer.from(chunk));
    }
    return Buffer.concat(parts);
  }
  throw new Error('Redirect limit');
}
function run(cmd, args, env) {
  const result = spawnSync(cmd, args, { encoding: 'utf8', timeout: 180_000, env: env || process.env });
  if (result.error || result.status !== 0) throw new Error(result.error?.message || result.stderr.slice(-2000));
  return result.stdout.trim();
}
const temporary = await mkdtemp(join(tmpdir(), 'meshway-public-release-'));
let result;
try {
  const metadata = await get('RELEASE.json', 128 * 1024);
  const signature = await get('RELEASE.json.sig', 64);
  const pin = await readFile(join(root, 'config/release-public.pem'), 'utf8');
  const manifest = verifyRelease(metadata, signature, pin);
  if (manifest.version !== '0.1.0-alpha.1' || manifest.sequence !== 1) throw new Error('Unexpected signed release');
  const name = 'meshway-cli-0.1.0.tgz';
  const tarball = await get(name, 2 * 1024 * 1024);
  if (sha(tarball) !== manifest.files[name]) throw new Error('CLI tarball publisher checksum mismatch');
  await writeFile(join(temporary, name), tarball, { mode: 0o600 });
  run('npm', ['install', '--prefix', temporary, '--ignore-scripts', '--no-audit', '--no-fund', join(temporary, name)]);
  const packageRoot = join(temporary, 'node_modules/@meshway/cli');
  if (await readFile(join(packageRoot, 'release-public.pem'), 'utf8') !== pin) throw new Error('Published CLI pin differs from source publisher');
  const env = { ...process.env, MESHWAY_CACHE_DIR: join(temporary, 'cache') };
  delete env.MESHWAY_BINARY;
  const cli = join(packageRoot, 'bin/meshway.js');
  run(process.execPath, [cli, 'install-core'], env);
  const version = run(process.execPath, [cli, 'version'], env);
  if (version !== 'Meshway 0.1.0-alpha.1') throw new Error('Installed core version mismatch');
  const { binaryName, verifyCachedCore } = await import(pathToFileURL(join(packageRoot, 'src/installer.js')));
  const binary = binaryName();
  const cached = join(env.MESHWAY_CACHE_DIR, binary);
  const contents = await readFile(cached);
  if (sha(contents) !== manifest.files[binary]) throw new Error('Installed core differs from signed release');
  await verifyCachedCore(cached, binary);
  const notices = await readFile(cached + '.THIRD_PARTY_NOTICES.txt');
  if (sha(notices) !== manifest.files['THIRD_PARTY_NOTICES.txt']) throw new Error('Installed notices differ');
  await writeFile(cached, Buffer.concat([contents, Buffer.from('tamper')]));
  const rejected = spawnSync(process.execPath, [cli, 'version'], { encoding: 'utf8', env, timeout: 20_000 });
  if (rejected.status !== 1 || !rejected.stderr.includes('Cached core checksum changed')) throw new Error('Changed cached core was not rejected');
  result = { status: 'PASS', checked_at: new Date().toISOString(), repository: 'user-github-me/meshway', version, metadata_sha256: sha(metadata), signed_file_count: Object.keys(manifest.files).length, package_sha256: sha(tarball), core: binary, core_sha256: sha(contents), source_publisher_pin: true, github_tarball_installed_locally: true, explicit_default_core_install: true, signed_notices: true, cached_tamper_rejected_before_execution: true, host_routing_dns_proxy_changes: false };
} finally {
  await rm(temporary, { recursive: true, force: true });
}
result.temporary_package_and_core_removed = true;
await mkdir(join(root, 'artifacts'), { recursive: true });
await writeFile(join(root, 'artifacts/published-install-validation.json'), JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result, null, 2));
