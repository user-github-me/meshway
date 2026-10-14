#!/usr/bin/env python3
"""Prepare the upstream signed Wintun userspace DLL; never install a driver."""
import argparse
import hashlib
import io
from pathlib import Path
import urllib.request
import zipfile

VERSION = '0.14.1'
SHA256 = '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
# Pin the release archive before extracting its architecture-specific signed DLL.
# Supply a reviewed hash; this command rejects unpinned downloads.
p = argparse.ArgumentParser()
p.add_argument('--sha256', default=SHA256, help='pinned SHA-256 of upstream wintun-0.14.1.zip')
p.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
if len(a.sha256) != 64 or any(c not in '0123456789abcdefABCDEF' for c in a.sha256):
    p.error('--sha256 must be a reviewed 64-character digest')
url = f'https://www.wintun.net/builds/wintun-{VERSION}.zip'
with urllib.request.urlopen(url, timeout=30) as response:
    data = response.read(10 * 1024 * 1024 + 1)
if len(data) > 10 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != a.sha256.lower():
    raise SystemExit('Wintun archive checksum mismatch; no DLL written')
with zipfile.ZipFile(io.BytesIO(data)) as archive:
    dll = archive.read(f'wintun/bin/{a.arch}/wintun.dll')
    license_text = archive.read('wintun/LICENSE.txt')
a.output.parent.mkdir(parents=True, exist_ok=True)
a.output.write_bytes(dll)
(a.output.parent / 'WINTUN-LICENSE.txt').write_bytes(license_text)
print(f'Prepared upstream {a.arch} Wintun library at {a.output}; verify Authenticode signature on Windows before release.')
