#!/usr/bin/env node
import { createHash, createPrivateKey, createPublicKey, generateKeyPairSync, sign } from 'node:crypto';
import { chmod, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { verifyRelease } from '../packages/cli/src/release.js';

export async function generateReleaseKey(privatePath, publicPath) {
  if (resolve(privatePath) === resolve(publicPath)) throw new Error('Private and public key paths must differ.');
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  await writeFile(privatePath, privateKey.export({ type: 'pkcs8', format: 'pem' }), { flag: 'wx', mode: 0o600 });
  await writeFile(publicPath, publicKey.export({ type: 'spki', format: 'pem' }), { flag: 'wx', mode: 0o644 });
  await chmod(privatePath, 0o600);
}

export async function signRelease(directory, keyPath, { version, sequence, expires, created = new Date().toISOString() }) {
  const information = await stat(keyPath);
  if (process.platform !== 'win32' && (information.mode & 0o077)) throw new Error('Signing private key must have private permissions (chmod 600).');
  const privateKey = createPrivateKey(await readFile(keyPath));
  if (privateKey.asymmetricKeyType !== 'ed25519') throw new Error('Signing key must be Ed25519.');
  const files = {};
  for (const name of (await readdir(directory)).sort()) {
    if (['RELEASE.json', 'RELEASE.json.sig', 'SHA256SUMS'].includes(name)) continue;
    const path = join(directory, name);
    if (!(await stat(path)).isFile()) continue;
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(name) || /\.(pem|key|pfx|p12|jks|keystore)$/i.test(name)) throw new Error('Unsafe release filename or signing credential in artifact directory.');
    files[name] = createHash('sha256').update(await readFile(path)).digest('hex');
  }
  const sums = Object.entries(files).map(([name, hash]) => `${hash}  ${name}`).join('\n') + '\n';
  await writeFile(join(directory, 'SHA256SUMS'), sums);
  files.SHA256SUMS = createHash('sha256').update(sums).digest('hex');
  const metadata = Buffer.from(JSON.stringify({ schema: 1, version, sequence: Number(sequence), created, expires, files }, null, 2) + '\n');
  const signature = sign(null, metadata, privateKey);
  verifyRelease(metadata, signature, createPublicKey(privateKey).export({ type: 'spki', format: 'pem' }));
  await writeFile(join(directory, 'RELEASE.json'), metadata);
  await writeFile(join(directory, 'RELEASE.json.sig'), signature);
}

export async function verifyArtifacts(directory, keyPath) {
  const release = verifyRelease(await readFile(join(directory, 'RELEASE.json')), await readFile(join(directory, 'RELEASE.json.sig')), await readFile(keyPath));
  for (const [name, digest] of Object.entries(release.files)) {
    if (createHash('sha256').update(await readFile(join(directory, name))).digest('hex') !== digest) throw new Error(`Artifact checksum mismatch: ${name}`);
  }
  const actual = (await readdir(directory)).filter(name => !['RELEASE.json', 'RELEASE.json.sig'].includes(name));
  for (const name of actual) if (!(name in release.files)) throw new Error(`Unsigned extra artifact: ${name}`);
  return release;
}

async function main() {
  const [command, ...args] = process.argv.slice(2);
  if (command === 'keygen' && args.length === 2) await generateReleaseKey(...args);
  else if (command === 'sign' && args.length === 5) await signRelease(args[0], args[1], { version: args[2], sequence: args[3], expires: args[4] });
  else if (command === 'verify' && args.length === 2) await verifyArtifacts(...args);
  else throw new Error('Usage: release-tool.mjs keygen PRIVATE_FILE PUBLIC_FILE | sign ARTIFACT_DIR PRIVATE_FILE VERSION SEQUENCE EXPIRES_ISO | verify ARTIFACT_DIR PUBLIC_FILE');
  console.log(`Release ${command} complete. Private key material was not displayed.`);
}
if (import.meta.url === pathToFileURL(process.argv[1] || '').href) main().catch(error => { console.error(error.message); process.exitCode = 1; });
