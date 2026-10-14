import { createPublicKey, verify } from 'node:crypto';

export function publicKey(pem) {
  if (/PRIVATE KEY/.test(pem.toString())) throw new Error('Use a public publisher key file.');
  const key = createPublicKey(pem);
  if (key.asymmetricKeyType !== 'ed25519') throw new Error('Release publisher key must be Ed25519.');
  return key;
}

export function verifyRelease(bytes, signature, pem, { now = Date.now(), allowExpired = false } = {}) {
  const key = publicKey(pem);
  if (signature.length !== 64 || !verify(null, bytes, key, signature)) throw new Error('Release signature is invalid. Nothing was installed.');
  let release;
  try { release = JSON.parse(bytes.toString('utf8')); } catch { throw new Error('Malformed signed release metadata.'); }
  if (release.schema !== 1 || !/^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?$/.test(release.version) ||
      !Number.isSafeInteger(release.sequence) || release.sequence < 1 ||
      !Number.isFinite(Date.parse(release.created)) || !Number.isFinite(Date.parse(release.expires)) ||
      Date.parse(release.created) > Date.parse(release.expires) || Date.parse(release.created) > now + 300_000 ||
      !release.files || typeof release.files !== 'object' || Array.isArray(release.files) || !Object.keys(release.files).length) {
    throw new Error('Invalid signed release metadata.');
  }
  if (!allowExpired && Date.parse(release.expires) <= now) throw new Error('Signed release metadata has expired.');
  for (const [name, hash] of Object.entries(release.files)) {
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(name) || !/^[a-f0-9]{64}$/.test(hash)) throw new Error('Invalid signed release file entry.');
  }
  return release;
}
