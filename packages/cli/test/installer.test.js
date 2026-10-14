import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash, generateKeyPairSync, sign } from 'node:crypto';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { binaryName, checksums, installCore, releaseURL, verifyCachedCore } from '../src/installer.js';
import { verifyRelease } from '../src/release.js';

const keys = generateKeyPairSync('ed25519');
const publisherKey = keys.publicKey.export({ type: 'spki', format: 'pem' });
const data = Buffer.from('test binary bytes');
function fixture({ sequence = 1, bytes = data, expires = '2999-01-01T00:00:00Z' } = {}) {
  const metadata = Buffer.from(JSON.stringify({ schema: 1, version: '0.1.0', sequence, created: '2026-01-01T00:00:00Z', expires, files: { 'meshway-linux-amd64': createHash('sha256').update(bytes).digest('hex') } }));
  const signature = sign(null, metadata, keys.privateKey);
  return { metadata, signature, fetchImpl: async url => new Response(url.pathname.endsWith('RELEASE.json') ? metadata : url.pathname.endsWith('RELEASE.json.sig') ? signature : bytes) };
}
const options = { base: 'https://releases.example/v0.1.0', publisherKey, platform: 'linux', arch: 'x64' };
async function temporary(fn) {
  const directory = await mkdtemp(join(tmpdir(), 'meshway-installer-'));
  try { await fn(directory); } finally { await rm(directory, { recursive: true }); }
}

test('maps supported platforms and rejects unknown architectures', () => {
  assert.equal(binaryName('win32', 'arm64'), 'meshway-windows-arm64.exe');
  assert.equal(binaryName('darwin', 'x64'), 'meshway-darwin-amd64');
  assert.equal(binaryName('linux', 'arm64'), 'meshway-linux-arm64');
  assert.throws(() => binaryName('linux', 'ia32'), /Unsupported/);
});

test('rejects insecure URLs and ambiguous or traversing manifests', () => {
  for (const url of ['http://example.com/', 'https://a:b@example.com/', 'https://example.com/?token=x']) assert.throws(() => releaseURL(url));
  const digest = 'a'.repeat(64);
  for (const text of [`${digest}  ../meshway`, `${digest}  meshway\n${digest}  meshway`, 'garbage']) assert.throws(() => checksums(text));
});

test('verifies publisher signature and binary before installing', () => temporary(async directory => {
  const { fetchImpl } = fixture();
  const binary = await installCore({ ...options, directory, fetchImpl, expectedVersion: '0.1.0' });
  assert.deepEqual(await readFile(binary), data);
  const manifest = verifyRelease(await readFile(`${binary}.release.json`), await readFile(`${binary}.release.sig`), await readFile(`${binary}.pub.pem`));
  assert.equal(manifest.sequence, 1);
}));

test('checksum mismatch leaves no installed file', () => temporary(async directory => {
  const f = fixture();
  const fetchImpl = async url => url.pathname.endsWith('meshway-linux-amd64') ? new Response('corrupt') : f.fetchImpl(url);
  await assert.rejects(installCore({ ...options, directory, fetchImpl }), /checksum mismatch/);
  assert.deepEqual(await readdir(directory), []);
}));

test('bundled pin rejects unknown publisher, tampered metadata, wrong key, truncated signature and expired release', () => temporary(async directory => {
  const f = fixture();
  await assert.rejects(installCore({ ...options, publisherKey: undefined, directory, fetchImpl: f.fetchImpl }), /signature/);
  const otherKey = generateKeyPairSync('ed25519').publicKey.export({ type: 'spki', format: 'pem' });
  await assert.rejects(installCore({ ...options, publisherKey: otherKey, directory, fetchImpl: f.fetchImpl }), /signature/);
  assert.throws(() => verifyRelease(Buffer.concat([f.metadata, Buffer.from(' ')]), f.signature, publisherKey), /signature/);
  assert.throws(() => verifyRelease(f.metadata, f.signature.subarray(0, 63), publisherKey), /signature/);
  const expired = fixture({ expires: '2026-01-02T00:00:00Z' });
  assert.throws(() => verifyRelease(expired.metadata, expired.signature, publisherKey), /expired/);
  await assert.rejects(installCore({ ...options, directory, fetchImpl: f.fetchImpl, expectedVersion: '0.2.0' }), /version/);
  assert.deepEqual(await readdir(directory), []);
}));

test('cached launches reverify signed metadata and binary bytes', () => temporary(async directory => {
  const path = await installCore({ ...options, directory, fetchImpl: fixture().fetchImpl });
  await verifyCachedCore(path, 'meshway-linux-amd64');
  await writeFile(path, 'tampered cache');
  await assert.rejects(verifyCachedCore(path, 'meshway-linux-amd64'), /checksum changed/);
  await writeFile(path, data);
  await writeFile(`${path}.release.sig`, Buffer.alloc(64));
  await assert.rejects(verifyCachedCore(path, 'meshway-linux-amd64'), /signature/);
}));

test('updates reject rollback and equivocation', () => temporary(async directory => {
  await installCore({ ...options, directory, fetchImpl: fixture({ sequence: 2 }).fetchImpl });
  await assert.rejects(installCore({ ...options, directory, fetchImpl: fixture().fetchImpl }), /rollback/);
  await assert.rejects(installCore({ ...options, directory, fetchImpl: fixture({ sequence: 2, bytes: Buffer.from('changed') }).fetchImpl }), /conflicting/);
  await installCore({ ...options, directory, fetchImpl: fixture({ sequence: 3 }).fetchImpl });
}));

test('an active installer lock prevents concurrent cache mutation', () => temporary(async directory => {
  await writeFile(join(directory, '.install.lock'), 'fixture', { mode: 0o600 });
  await assert.rejects(installCore({ ...options, directory, fetchImpl: fixture().fetchImpl }), /cache lock/);
  assert.deepEqual(await readdir(directory), ['.install.lock']);
}));

test('rejects HTTPS to HTTP redirect', async () => {
  await assert.rejects(installCore({ ...options, fetchImpl: async () => new Response(null, { status: 302, headers: { location: 'http://example.com/insecure' } }) }), /Unsafe/);
});
