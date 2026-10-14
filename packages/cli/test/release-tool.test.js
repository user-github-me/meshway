import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm, stat, chmod } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { generateReleaseKey, signRelease, verifyArtifacts } from '../../../scripts/release-tool.mjs';

test('release tooling signs all artifacts, rejects tampering and keeps private keys private', async () => {
  const temp = await mkdtemp(join(tmpdir(), 'meshway-release-'));
  const artifacts = join(temp, 'release');
  const privatePath = join(temp, 'signing.pem');
  const publicPath = join(temp, 'public.pem');
  try {
    await mkdir(artifacts);
    await generateReleaseKey(privatePath, publicPath);
    if (process.platform !== 'win32') assert.equal((await stat(privatePath)).mode & 0o777, 0o600);
    await writeFile(join(artifacts, 'meshway-linux-amd64'), 'fixture');
    await signRelease(artifacts, privatePath, { version: '0.1.0', sequence: 1, expires: '2999-01-01T00:00:00Z' });
    assert.equal((await verifyArtifacts(artifacts, publicPath)).version, '0.1.0');
    await writeFile(join(artifacts, 'meshway-linux-amd64'), 'tampered');
    await assert.rejects(verifyArtifacts(artifacts, publicPath), /checksum mismatch/);
    if (process.platform !== 'win32') {
      await chmod(privatePath, 0o644);
      await assert.rejects(signRelease(artifacts, privatePath, { version: '0.1.0', sequence: 2, expires: '2999-01-01T00:00:00Z' }), /permissions/);
    }
  } finally { await rm(temp, { recursive: true }); }
});
