import { createHash, randomBytes } from 'node:crypto';
import { mkdir, readFile, writeFile, chmod, rename, rm, stat } from 'node:fs/promises';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

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

// Checksums verify release integrity, not publisher identity. Use a trusted HTTPS release.
export async function installCore({ base, directory = cacheDirectory(), platform, arch, fetchImpl = fetch }) {
  const name = binaryName(platform, arch);
  const release = releaseURL(base);
  const manifest = checksums((await download(new URL('SHA256SUMS', release), 128 * 1024, fetchImpl)).toString('utf8'));
  const expected = manifest.get(name);
  if (!expected) throw new Error(`SHA256SUMS has no entry for ${name}.`);
  const data = await download(new URL(name, release), 96 * 1024 * 1024, fetchImpl);
  if (sha256(data) !== expected) throw new Error('Binary checksum mismatch. Nothing was installed.');
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const destination = join(directory, name);
  const temporary = `${destination}.${randomBytes(8).toString('hex')}.tmp`;
  try {
    await writeFile(temporary, data, { flag: 'wx', mode: 0o700 });
    await chmod(temporary, 0o700);
    await rename(temporary, destination);
    await writeFile(`${destination}.sha256`, `${expected}\n`, { mode: 0o600 });
  } finally { await rm(temporary, { force: true }); }
  return destination;
}

async function exists(path) {
  try { return (await stat(path)).isFile(); } catch { return false; }
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
    const expected = (await readFile(`${cached}.sha256`, 'utf8')).trim();
    if (!/^[a-f0-9]{64}$/.test(expected) || sha256(await readFile(cached)) !== expected) throw new Error('Cached core checksum changed. Reinstall the core.');
    return cached;
  }
  throw new Error('Meshway core is not installed. In the source checkout run ./scripts/build.sh, or run meshway install-core --release-base https://YOUR-RELEASE-DIRECTORY/.');
}
