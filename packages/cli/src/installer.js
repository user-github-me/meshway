import { createHash, randomBytes } from 'node:crypto';
import { mkdir, readFile, writeFile, chmod, rename, rm, stat, open } from 'node:fs/promises';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { publicKey, verifyRelease } from './release.js';

export const projectReleaseVersion = '0.1.0-alpha.1';
export const projectReleaseBase = `https://github.com/user-github-me/meshway/releases/download/v${projectReleaseVersion}/`;

export function binaryName(platform = process.platform, arch = process.arch) {
  const operatingSystem = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[platform];
  const architecture = { x64: 'amd64', arm64: 'arm64' }[arch];
  if (!operatingSystem || !architecture) throw new Error(`Unsupported platform: ${platform}/${arch}. Build the Go core from source.`);
  return `meshway-${operatingSystem}-${architecture}${platform === 'win32' ? '.exe' : ''}`;
}

export function releaseURL(base) {
  const url = new URL(base);
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) {
    throw new Error('Release base must be an HTTPS directory URL without credentials, query, or fragment.');
  }
  if (!url.pathname.endsWith('/')) url.pathname += '/';
  return url;
}

export function checksums(text) {
  const result = new Map();
  for (const line of text.trim().split(/\r?\n/)) {
    const match = /^([a-fA-F0-9]{64})\s+\*?([a-zA-Z0-9._-]+)$/.exec(line);
    if (!match || result.has(match[2])) throw new Error('Malformed or duplicate SHA256SUMS entry.');
    result.set(match[2], match[1].toLowerCase());
  }
  return result;
}

export const cacheDirectory = () => process.env.MESHWAY_CACHE_DIR || join(homedir(), '.meshway', 'bin');
const sha256 = data => createHash('sha256').update(data).digest('hex');

async function download(url, limit, fetchImpl) {
  let target = url;
  for (let redirect = 0; redirect <= 5; redirect++) {
    const response = await fetchImpl(target, { redirect: 'manual', signal: AbortSignal.timeout(90_000) });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      const location = response.headers.get('location');
      if (!location) throw new Error('Release redirect has no location.');
      target = new URL(location, target);
      if (target.protocol !== 'https:' || target.username || target.password) throw new Error('Unsafe release redirect.');
      await response.body?.cancel();
      continue;
    }
    if (!response.ok) throw new Error(`Release download failed (${response.status}). Publish release files or build the core locally.`);
    if (Number(response.headers.get('content-length')) > limit) throw new Error('Release file exceeds size limit.');
    const chunks = []; let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > limit) { await response.body.cancel().catch(() => {}); throw new Error('Release file exceeds size limit.'); }
      chunks.push(Buffer.from(chunk));
    }
    return Buffer.concat(chunks);
  }
  throw new Error('Too many release redirects.');
}

export async function installCore({ base = projectReleaseBase, publisherKey, expectedVersion = base === projectReleaseBase ? projectReleaseVersion : undefined, directory = cacheDirectory(), platform, arch, fetchImpl = fetch } = {}) {
  const name = binaryName(platform, arch);
  const release = releaseURL(base);
  publisherKey ||= await readFile(new URL('../release-public.pem', import.meta.url), 'utf8');
  const canonicalKey = publicKey(publisherKey).export({ type: 'spki', format: 'pem' });
  const metadata = await download(new URL('RELEASE.json', release), 128 * 1024, fetchImpl);
  const signature = await download(new URL('RELEASE.json.sig', release), 64, fetchImpl);
  const manifest = verifyRelease(metadata, signature, canonicalKey);
  if (expectedVersion && manifest.version !== expectedVersion) throw new Error('Signed release version does not match requested version.');
  const expected = manifest.files[name];
  if (!expected) throw new Error(`Signed release has no entry for ${name}.`);
  const destination = join(directory, name);
  const data = await download(new URL(name, release), 96 * 1024 * 1024, fetchImpl);
  if (sha256(data) !== expected) throw new Error('Binary checksum mismatch. Nothing was installed.');
  const notices = [];
  for (const companion of ['LICENSE', 'THIRD_PARTY_NOTICES.txt']) {
    if (!manifest.files[companion]) continue;
    const contents = await download(new URL(companion, release), 8 * 1024 * 1024, fetchImpl);
    if (sha256(contents) !== manifest.files[companion]) throw new Error('Release license checksum mismatch. Nothing was installed.');
    notices.push([companion, contents]);
  }
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const temporary = `${destination}.${randomBytes(8).toString('hex')}.tmp`;
  const lockPath = join(directory, '.install.lock');
  let lock;
  try { lock = await open(lockPath, 'wx', 0o600); }
  catch (error) {
    if (error.code === 'EEXIST') throw new Error('Another installer holds this cache lock. Retry after it finishes; remove a stale lock only after confirming no installer is running.');
    throw error;
  }
  try {
    // Check rollback while holding the writer lock, after downloads complete.
    if (await exists(`${destination}.release.json`)) {
      const previousKey = await readFile(`${destination}.pub.pem`, 'utf8');
      if (previousKey !== canonicalKey) throw new Error('Installed publisher key differs. Use a separate cache to explicitly adopt a new publisher key.');
      const previousMetadata = await readFile(`${destination}.release.json`);
      const previous = verifyRelease(previousMetadata, await readFile(`${destination}.release.sig`), canonicalKey, { allowExpired: true });
      if (manifest.sequence < previous.sequence || (manifest.sequence === previous.sequence && sha256(metadata) !== sha256(previousMetadata))) throw new Error('Release rollback or conflicting release sequence rejected.');
    }
    await writeFile(temporary, data, { flag: 'wx', mode: 0o700 });
    await chmod(temporary, 0o700);
    // Metadata is written first: interruption fails closed instead of launching unverified bytes.
    await writeFile(`${destination}.pub.pem`, canonicalKey, { mode: 0o600 });
    await writeFile(`${destination}.release.json`, metadata, { mode: 0o600 });
    await writeFile(`${destination}.release.sig`, signature, { mode: 0o600 });
    for (const [companion, contents] of notices) await writeFile(`${destination}.${companion}`, contents, { mode: 0o600 });
    await rename(temporary, destination);
  } finally { await rm(temporary, { force: true }); await lock.close(); await rm(lockPath, { force: true }); }
  return destination;
}

async function exists(path) {
  try { return (await stat(path)).isFile(); } catch { return false; }
}

export async function verifyCachedCore(cached, name) {
  const release = verifyRelease(await readFile(`${cached}.release.json`), await readFile(`${cached}.release.sig`), await readFile(`${cached}.pub.pem`, 'utf8'), { allowExpired: true });
  if (sha256(await readFile(cached)) !== release.files[name]) throw new Error('Cached core checksum changed. Reinstall the core.');
  return cached;
}

export async function resolveCore() {
  if (process.env.MESHWAY_BINARY) {
    if (!await exists(process.env.MESHWAY_BINARY)) throw new Error('MESHWAY_BINARY does not point to a file.');
    return process.env.MESHWAY_BINARY;
  }
  const name = binaryName();
  const root = fileURLToPath(new URL('../../../', import.meta.url));
  for (const path of [join(root, 'dist', 'bin', name), join(root, 'dist', 'meshway')]) {
    if (await exists(path)) return path;
  }
  const cached = join(cacheDirectory(), name);
  if (await exists(cached)) {
    return verifyCachedCore(cached, name);
  }
  throw new Error('Meshway core is not installed. Run meshway install-core to install the signed project release, or build it locally with ./scripts/build.sh.');
}
