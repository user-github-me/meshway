#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
export MESHWAY_DIST_DIR=${MESHWAY_DIST_DIR:-dist}
export MESHWAY_RELEASE_DIR=${MESHWAY_RELEASE_DIR:-release}
./scripts/build.sh --all
mkdir -p "$MESHWAY_RELEASE_DIR"
# New metadata must be signed after all packaging steps finish.
python3 - <<'PY'
from pathlib import Path
import os
release=Path(os.environ['MESHWAY_RELEASE_DIR'])
for name in ('RELEASE.json','RELEASE.json.sig'):
    (release/name).unlink(missing_ok=True)
if os.environ.get('MESHWAY_INCLUDE_IOS_ARTIFACTS') != '1':
    for name in ('Meshway-ios-unsigned.ipa','Meshway-ios-unsigned.zip'):
        (release/name).unlink(missing_ok=True)
PY
python3 - <<'PY'
from pathlib import Path
import hashlib, shutil, tarfile, zipfile, os
release=Path(os.environ['MESHWAY_RELEASE_DIR'])
dist=Path(os.environ['MESHWAY_DIST_DIR'])
for binary in sorted((dist/'bin').glob('meshway-*')):
    shutil.copy2(binary,release/binary.name)
    name=binary.name.removesuffix('.exe')
    if binary.suffix=='.exe':
        with zipfile.ZipFile(release/f'{name}.zip','w',zipfile.ZIP_DEFLATED) as archive:
            archive.write(binary,'meshway.exe')
            archive.write('LICENSE','LICENSE')
            archive.write('README.md','README.md')
            if Path('THIRD_PARTY_NOTICES.txt').is_file(): archive.write('THIRD_PARTY_NOTICES.txt','THIRD_PARTY_NOTICES.txt')
    else:
        with tarfile.open(release/f'{name}.tar.gz','w:gz') as archive:
            archive.add(binary,arcname='meshway')
            archive.add('LICENSE',arcname='LICENSE')
            archive.add('README.md',arcname='README.md')
            if Path('THIRD_PARTY_NOTICES.txt').is_file(): archive.add('THIRD_PARTY_NOTICES.txt',arcname='THIRD_PARTY_NOTICES.txt')
if Path('THIRD_PARTY_NOTICES.txt').is_file(): shutil.copy2('THIRD_PARTY_NOTICES.txt',release/'THIRD_PARTY_NOTICES.txt')
shutil.copy2('LICENSE',release/'LICENSE')
print(f'Local release artifacts created in {release}. Nothing was published.')
PY
# Package the complete native bundles when they exist. No partial GUI-only
# Windows zip may omit its daemon, helper, signed driver and license.
python3 - <<'PY'
from pathlib import Path
import subprocess, zipfile, os
release=Path(os.environ['MESHWAY_RELEASE_DIR'])
dist=Path(os.environ['MESHWAY_DIST_DIR'])
for runtime in ('win-x64','win-arm64'):
    package=dist/f'Meshway-{runtime}'
    if not package.is_dir(): continue
    required=('Meshway.exe','meshway-daemon.exe','meshway-vpn-helper.exe','wintun.dll','WINTUN-LICENSE.txt')
    if any(not (package/name).is_file() for name in required):
        raise SystemExit(f'Incomplete Windows bundle: {package}')
    import shutil
    architecture='amd64' if runtime=='win-x64' else 'arm64'
    shutil.copy2((dist/'bin')/f'meshway-windows-{architecture}.exe',package/'meshway-daemon.exe')
    if Path('THIRD_PARTY_NOTICES.txt').is_file(): shutil.copy2('THIRD_PARTY_NOTICES.txt',package/'THIRD_PARTY_NOTICES.txt')
    with zipfile.ZipFile(release/f'Meshway-{runtime}.zip','w',zipfile.ZIP_DEFLATED) as archive:
        for file in sorted(package.rglob('*')):
            if file.is_file(): archive.write(file, str(Path('Meshway')/file.relative_to(package)))
for apk in dist.glob('meshway-android-*.apk'):
    import shutil
    shutil.copy2(apk,release/apk.name)
native=dist/'apple-macos/Build/Products/Release/Meshway.app'
simple=dist/'Meshway.app'
app=native if native.is_dir() else simple
if app.is_dir() and __import__('sys').platform=='darwin':
    if native.is_dir():
        daemon=native/'Contents/Resources/meshway'
        architectures=set(subprocess.check_output(['lipo','-archs',str(daemon)],text=True).split())
        if not {'x86_64','arm64'}.issubset(architectures):
            raise SystemExit('Freeze and sign the complete Mac app with a universal daemon before packaging.')
        # Preserve the validated bundle: replacing a sealed daemon here would
        # invalidate its existing code-signing resource seal.
        subprocess.run(['codesign','--verify','--deep','--strict',str(native)],check=True)
    subprocess.run(['ditto','-c','-k','--keepParent',str(app),str(release/'Meshway-macos.zip')],check=True)
    if native.is_dir() and simple.is_dir():
        subprocess.run(['ditto','-c','-k','--keepParent',str(simple),str(release/'Meshway-macos-arm64-daemon-client.zip')],check=True)
        (release/'Meshway-macos-daemon-client.zip').unlink(missing_ok=True)
ios=dist/'apple-ios/Build/Products/Release-iphoneos/Meshway.app'
if ios.is_dir() and __import__('sys').platform=='darwin' and __import__('os').environ.get('MESHWAY_INCLUDE_IOS_ARTIFACTS')=='1':
    subprocess.run(['ditto','-c','-k','--keepParent',str(ios),str(release/'Meshway-ios-unsigned.zip')],check=True)
    import tempfile, shutil
    with tempfile.TemporaryDirectory(prefix='meshway-ipa-') as staging:
        payload=Path(staging)/'Payload'/'Meshway.app'
        shutil.copytree(ios,payload,symlinks=True)
        if Path('THIRD_PARTY_NOTICES.txt').is_file(): shutil.copy2('THIRD_PARTY_NOTICES.txt',payload/'THIRD_PARTY_NOTICES.txt')
        subprocess.run(['ditto','-c','-k',staging,str(release/'Meshway-ios-unsigned.ipa')],check=True)
for browser in ('chrome','firefox'):
    extension=Path('apps/extension/dist')/browser
    if not extension.is_dir(): continue
    with zipfile.ZipFile(release/f'Meshway-extension-{browser}.zip','w',zipfile.ZIP_DEFLATED) as archive:
        for file in sorted(extension.rglob('*')):
            if file.is_file(): archive.write(file,str(file.relative_to(extension)))
if Path('THIRD_PARTY_NOTICES.txt').is_file():
    for name in ('Meshway-macos.zip','Meshway-macos-arm64-daemon-client.zip','Meshway-ios-unsigned.zip','Meshway-extension-chrome.zip','Meshway-extension-firefox.zip'):
        archive_path=release/name
        if not archive_path.is_file(): continue
        with zipfile.ZipFile(archive_path,'a',zipfile.ZIP_DEFLATED) as archive:
            archive.write('THIRD_PARTY_NOTICES.txt','THIRD_PARTY_NOTICES.txt')
# Checksums are regenerated after packaging, never hash an older SHA256SUMS.
import hashlib
checksums=[f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}' for path in sorted(release.iterdir()) if path.is_file() and path.name!='SHA256SUMS']
(release/'SHA256SUMS').write_text('\n'.join(checksums)+'\n')
PY
release_version=${MESHWAY_RELEASE_VERSION:-$(node -p "JSON.parse(require('fs').readFileSync('package.json','utf8')).version")}
if [ "${MESHWAY_INCLUDE_IOS_ARTIFACTS:-0}" = 1 ]; then
  python3 scripts/native-manifest.py --version "$release_version" --dist "$MESHWAY_DIST_DIR" --output "$MESHWAY_RELEASE_DIR/native-builds.json" --include-ios
else
  python3 scripts/native-manifest.py --version "$release_version" --dist "$MESHWAY_DIST_DIR" --output "$MESHWAY_RELEASE_DIR/native-builds.json"
fi
if [ -n "${MESHWAY_RELEASE_KEY_FILE:-}" ]; then
  : "${MESHWAY_RELEASE_VERSION:?Set release version}"
  : "${MESHWAY_RELEASE_SEQUENCE:?Set monotonically increasing release sequence}"
  : "${MESHWAY_RELEASE_EXPIRES:?Set release metadata expiration in ISO UTC}"
  node scripts/release-tool.mjs sign "$MESHWAY_RELEASE_DIR" "$MESHWAY_RELEASE_KEY_FILE" "$MESHWAY_RELEASE_VERSION" "$MESHWAY_RELEASE_SEQUENCE" "$MESHWAY_RELEASE_EXPIRES"
fi
