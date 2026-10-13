import { build } from 'esbuild';
import { mkdir, readFile, writeFile, copyFile, rm } from 'node:fs/promises';

for (const target of ['chrome', 'firefox']) {
  const output = `dist/${target}`;
  await rm(output, { recursive: true, force: true });
  await mkdir(output, { recursive: true });
  await build({ entryPoints: ['src/background.ts', 'src/popup.ts'], bundle: true, outdir: output, platform: 'browser', target: 'es2022', format: 'iife', define: { FIREFOX: String(target === 'firefox') } });
  await copyFile(`manifests/${target}.json`, `${output}/manifest.json`);
  for (const asset of ['popup.html', 'popup.css']) await copyFile(`public/${asset}`, `${output}/${asset}`);
  // SVG is allowed for Firefox; Chrome toolbar icons are generated as PNG by a tiny pure JS encoder.
  for (const size of [16, 32, 48, 128]) await writeFile(`${output}/icon${size}.png`, iconPNG(size));
}
console.log('Built Chrome MV3 and Firefox MV2 packages under apps/extension/dist.');

function iconPNG(size) {
  // Raster logo: lime rounded square with a dark interlinked hexagon. No external assets or network.
  const raw = Buffer.alloc((size * 4 + 1) * size);
  for (let y = 0; y < size; y++) {
    const offset = y * (size * 4 + 1); raw[offset] = 0;
    for (let x = 0; x < size; x++) {
      const nx = (x + .5) / size, ny = (y + .5) / size;
      const dx = Math.max(.16 - nx, 0, nx - .84), dy = Math.max(.16 - ny, 0, ny - .84);
      const visible = dx * dx + dy * dy < .16 ** 2;
      const inHex = (scale) => Math.abs(nx - .5) < .29 * scale && Math.abs(ny - .5) < .32 * scale && Math.abs(nx - .5) * .58 + Math.abs(ny - .5) < .32 * scale;
      const dark = inHex(1) && !inHex(.49);
      const i = offset + 1 + x * 4; raw[i] = dark ? 33 : 211; raw[i+1] = dark ? 24 : 255; raw[i+2] = dark ? 47 : 115; raw[i+3] = visible ? 255 : 0;
    }
  }
  const zlib = process.getBuiltinModule('node:zlib');
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(size, 0); ihdr.writeUInt32BE(size, 4); ihdr[8] = 8; ihdr[9] = 6;
  return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]), chunk('IHDR', ihdr), chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}
function chunk(name, data) {
  const type = Buffer.from(name); const out = Buffer.alloc(data.length + 12);
  out.writeUInt32BE(data.length, 0); type.copy(out, 4); data.copy(out, 8);
  let crc = 0xffffffff; for (const byte of Buffer.concat([type, data])) { crc ^= byte; for (let i=0;i<8;i++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0); }
  out.writeUInt32BE((crc ^ 0xffffffff) >>> 0, data.length + 8); return out;
}
