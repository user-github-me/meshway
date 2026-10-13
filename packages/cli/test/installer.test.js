import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { binaryName, checksums, installCore, releaseURL } from '../src/installer.js';

test('maps six supported platforms and rejects unknown architectures', () => {
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

test('verifies integrity before writing a release binary', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'meshway-installer-'));
  const data = Buffer.from('test binary bytes');
  const digest = createHash('sha256').update(data).digest('hex');
  const fetchImpl = async url => new Response(url.pathname.endsWith('SHA256SUMS') ? `${digest}  meshway-linux-amd64\n` : data);
  try {
    const binary = await installCore({ base: 'https://releases.example/v0.1.0', directory, platform: 'linux', arch: 'x64', fetchImpl });
    assert.deepEqual(await readFile(binary), data);
    assert.equal((await readFile(`${binary}.sha256`, 'utf8')).trim(), digest);
  } finally { await rm(directory, { recursive: true }); }
});

test('checksum mismatch leaves no installed file', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'meshway-installer-'));
  const fetchImpl = async url => new Response(url.pathname.endsWith('SHA256SUMS') ? `${'a'.repeat(64)}  meshway-linux-amd64\n` : 'corrupt');
  try {
    await assert.rejects(installCore({ base: 'https://example.com/v0.1.0', directory, platform: 'linux', arch: 'x64', fetchImpl }), /checksum mismatch/);
    assert.deepEqual(await readdir(directory), []);
  } finally { await rm(directory, { recursive: true }); }
});

test('rejects HTTPS to HTTP redirect', async () => {
  await assert.rejects(installCore({ base: 'https://example.com/v0.1.0', fetchImpl: async () => new Response(null, { status: 302, headers: { location: 'http://example.com/insecure' } }) }), /Unsafe/);
});
