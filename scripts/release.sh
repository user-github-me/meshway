#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
./scripts/build.sh --all
mkdir -p release
# New metadata must be signed after all packaging steps finish.
python3 - <<'PY'
from pathlib import Path
for name in ('RELEASE.json','RELEASE.json.sig'):
    (Path('release')/name).unlink(missing_ok=True)
PY
python3 - <<'PY'
from pathlib import Path
import hashlib, shutil, tarfile, zipfile
release=Path('release')
for binary in sorted(Path('dist/bin').glob('meshway-*')):
    shutil.copy2(binary,release/binary.name)
    name=binary.name.removesuffix('.exe')
    if binary.suffix=='.exe':
        with zipfile.ZipFile(release/f'{name}.zip','w',zipfile.ZIP_DEFLATED) as archive:
            archive.write(binary,'meshway.exe')
            archive.write('LICENSE','LICENSE')
            archive.write('README.md','README.md')
    else:
        with tarfile.open(release/f'{name}.tar.gz','w:gz') as archive:
            archive.add(binary,arcname='meshway')
            archive.add('LICENSE',arcname='LICENSE')
            archive.add('README.md',arcname='README.md')
print('Local release artifacts created in release/. Nothing was published.')
PY
# Package the complete native bundles when they exist. No partial GUI-only
# Windows zip may omit its daemon, helper, signed driver and license.
python3 - <<'PY'
from pathlib import Path
import subprocess, zipfile
release=Path('release')
for runtime in ('win-x64','win-arm64'):
    package=Path('dist')/f'Meshway-{runtime}'
    if not package.is_dir(): continue
    required=('Meshway.exe','meshway-daemon.exe','meshway-vpn-helper.exe','wintun.dll','WINTUN-LICENSE.txt')
    if any(not (package/name).is_file() for name in required):
        raise SystemExit(f'Incomplete Windows bundle: {package}')
    import shutil
    architecture='amd64' if runtime=='win-x64' else 'arm64'
    shutil.copy2(Path('dist/bin')/f'meshway-windows-{architecture}.exe',package/'meshway-daemon.exe')
    with zipfile.ZipFile(release/f'Meshway-{runtime}.zip','w',zipfile.ZIP_DEFLATED) as archive:
        for file in sorted(package.rglob('*')):
            if file.is_file(): archive.write(file, str(Path('Meshway')/file.relative_to(package)))
for apk in Path('dist').glob('meshway-android-*.apk'):
    import shutil
    shutil.copy2(apk,release/apk.name)
native=Path('dist/apple-macos/Build/Products/Release/Meshway.app')
simple=Path('dist/Meshway.app')
app=native if native.is_dir() else simple
if app.is_dir() and __import__('sys').platform=='darwin':
    subprocess.run(['ditto','-c','-k','--keepParent',str(app),str(release/'Meshway-macos.zip')],check=True)
    if native.is_dir() and simple.is_dir():
        daemon=Path('dist/bin/meshway-darwin-arm64')
        import shutil
        shutil.copy2(daemon,simple/'Contents/Resources/meshway')
        subprocess.run(['ditto','-c','-k','--keepParent',str(simple),str(release/'Meshway-macos-daemon-client.zip')],check=True)
ios=Path('dist/apple-ios/Build/Products/Release-iphoneos/Meshway.app')
if ios.is_dir() and __import__('sys').platform=='darwin':
    subprocess.run(['ditto','-c','-k','--keepParent',str(ios),str(release/'Meshway-ios-unsigned.zip')],check=True)
for browser in ('chrome','firefox'):
    extension=Path('apps/extension/dist')/browser
    if not extension.is_dir(): continue
    with zipfile.ZipFile(release/f'Meshway-extension-{browser}.zip','w',zipfile.ZIP_DEFLATED) as archive:
        for file in sorted(extension.rglob('*')):
            if file.is_file(): archive.write(file,str(file.relative_to(extension)))
# Checksums are regenerated after packaging, never hash an older SHA256SUMS.
import hashlib
checksums=[f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}' for path in sorted(release.iterdir()) if path.is_file() and path.name!='SHA256SUMS']
(release/'SHA256SUMS').write_text('\n'.join(checksums)+'\n')
PY
release_version=${MESHWAY_RELEASE_VERSION:-$(node -p "JSON.parse(require('fs').readFileSync('package.json','utf8')).version")}
python3 scripts/native-manifest.py --version "$release_version"
if [ -n "${MESHWAY_RELEASE_KEY_FILE:-}" ]; then
  : "${MESHWAY_RELEASE_VERSION:?Set release version}"
  : "${MESHWAY_RELEASE_SEQUENCE:?Set monotonically increasing release sequence}"
  : "${MESHWAY_RELEASE_EXPIRES:?Set release metadata expiration in ISO UTC}"
  node scripts/release-tool.mjs sign release "$MESHWAY_RELEASE_KEY_FILE" "$MESHWAY_RELEASE_VERSION" "$MESHWAY_RELEASE_SEQUENCE" "$MESHWAY_RELEASE_EXPIRES"
fi
