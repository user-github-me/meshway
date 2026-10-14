import { createHash } from 'node:crypto';
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const config = JSON.parse(await readFile(join(root, 'config/npm-publication.json'), 'utf8'));
if (config.name !== '@nazifbd/meshway' || !/^\d+\.\d+\.\d+-alpha\.\d+$/.test(config.version) || config.tag !== 'alpha' || config.access !== 'public' || config.registry !== 'https://registry.npmjs.org/') throw new Error('Unexpected npm publication destination');
const source = join(root, 'packages/cli');
const original = JSON.parse(await readFile(join(source, 'package.json'), 'utf8'));
const manifest = { ...original, name: config.name, version: config.version, repository: { type: 'git', url: config.repository }, homepage: config.homepage, bugs: { url: config.bugs }, publishConfig: { access: config.access, tag: config.tag, registry: config.registry } };
const output = join(root, '.tools/release/npm-registry', config.version);
await mkdir(output, { recursive: true });
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, encoding: 'utf8', timeout: 60_000 });
  if (result.error || result.status !== 0) throw new Error(result.error?.message || `${command} failed`);
  return result.stdout;
}
const entries = ['bin/meshway.js', 'src/installer.js', 'src/release.js', 'release-public.pem', 'README.md', 'LICENSE'];
const expected = ['package/package.json', ...entries.map(name => `package/${name}`)].sort();
let first, packedName;
for (let attempt = 0; attempt < 2; attempt++) {
  const stage = await mkdtemp(join(output, '.pack-'));
  try {
    for (const name of original.files) await cp(join(source, name), join(stage, name), { recursive: true });
    await writeFile(join(stage, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
    const response = JSON.parse(run('npm', ['pack', '--ignore-scripts', '--json', '--workspaces=false'], stage));
    const packed = Array.isArray(response) ? response : Object.values(response);
    if (packed.length !== 1 || packed[0].name !== config.name || packed[0].version !== config.version) throw new Error('Packed package identity mismatch');
    packedName = packed[0].filename;
    const archive = join(stage, packedName);
    const listed = run('tar', ['-tzf', archive], stage).trim().split('\n').sort();
    if (JSON.stringify(listed) !== JSON.stringify(expected)) throw new Error('Unexpected npm archive files');
    for (const name of entries) {
      const extracted = spawnSync('tar', ['-xOzf', archive, `package/${name}`], { timeout: 30_000 });
      if (extracted.status !== 0 || !extracted.stdout.equals(await readFile(join(source, name)))) throw new Error(`Archive source bytes changed: ${name}`);
    }
    const packedManifest = JSON.parse(run('tar', ['-xOzf', archive, 'package/package.json'], stage));
    if (JSON.stringify(packedManifest) !== JSON.stringify(manifest) || packedManifest.bin.meshway !== 'bin/meshway.js' || packedManifest.license !== 'Apache-2.0') throw new Error('Archive metadata mismatch');
    const bytes = await readFile(archive);
    if (first && !first.equals(bytes)) throw new Error('Independent npm packs were not byte-identical');
    first ||= bytes;
  } finally { await rm(stage, { recursive: true, force: true }); }
}
await writeFile(join(output, packedName), first);
const result = { file: join(output, packedName), name: config.name, version: config.version, tag: config.tag, sha256: sha(first), source_code_pin_license_identical: true, reproducible_pack: true };
await writeFile(join(output, 'verification.json'), JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result, null, 2));
