#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
./scripts/build.sh --all
mkdir -p release
python3 - <<'PY'
from pathlib import Path
import hashlib, shutil, tarfile, zipfile
release=Path('release')
checksums=[]
for binary in sorted(Path('dist/bin').glob('meshway-*')):
    shutil.copy2(binary,release/binary.name)
    checksums.append(f'{hashlib.sha256(binary.read_bytes()).hexdigest()}  {binary.name}')
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
(release/'SHA256SUMS').write_text('\n'.join(checksums)+'\n')
print('Local release artifacts created in release/. Nothing was published.')
PY
