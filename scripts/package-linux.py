#!/usr/bin/env python3
"""Create a Debian package without installing anything on the development host."""
import argparse
import io
import os
from pathlib import Path
import re
import tarfile
import tempfile
import time


def tar(entries):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, content, mode in entries:
            entry = tarfile.TarInfo(name)
            entry.size, entry.mode, entry.mtime = len(content), mode, 0
            entry.uid = entry.gid = 0
            entry.uname = entry.gname = "root"
            archive.addfile(entry, io.BytesIO(content))
    return output.getvalue()


def ar(entries):
    output = bytearray(b"!<arch>\n")
    for name, content in entries:
        header = f"{name + '/':<16}{0:<12}{0:<6}{0:<6}{'100644':<8}{len(content):<10}`\n"
        output.extend(header.encode("ascii"))
        output.extend(content)
        if len(content) % 2:
            output.extend(b"\n")
    return bytes(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--arch", required=True, choices=["amd64", "arm64"])
    parser.add_argument("--output", type=Path, default=Path("release"))
    args = parser.parse_args()
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[A-Za-z0-9.]+)?", args.version):
        parser.error("Invalid package version")
    if not args.binary.read_bytes().startswith(b"\x7fELF"):
        parser.error("Expected a Linux ELF binary")
    root = Path(__file__).resolve().parents[1]
    control = f"Package: meshway\nVersion: {args.version}\nSection: net\nPriority: optional\nArchitecture: {args.arch}\nMaintainer: Meshway contributors\nDepends: adduser, systemd\nDescription: Encrypted peer VPN, SOCKS and tunnel daemon\n Internet exits require explicit operator consent and peer authorization.\n"
    postinst = b'''#!/bin/sh
set -eu
if ! getent passwd meshway >/dev/null; then
  adduser --system --group --home /var/lib/meshway --no-create-home meshway
fi
install -d -m 0700 -o meshway -g meshway /var/lib/meshway
if [ ! -f /var/lib/meshway/config.json ]; then
  runuser -u meshway -- /usr/bin/meshway init --config /var/lib/meshway/config.json
fi
if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload || true; fi
echo 'Meshway installed. Configure roles, then explicitly enable meshway.service.'
'''
    prerm = b'''#!/bin/sh
set -eu
if [ "$1" = remove ] && command -v systemctl >/dev/null 2>&1; then
  systemctl stop meshway.service || true
fi
'''
    service = (root / "deploy/systemd/meshway.service").read_bytes().replace(b"/usr/local/bin/meshway", b"/usr/bin/meshway")
    package = ar([
        ("debian-binary", b"2.0\n"),
        ("control.tar.gz", tar([("control", control.encode(), 0o644), ("postinst", postinst, 0o755), ("prerm", prerm, 0o755)])),
        ("data.tar.gz", tar([("usr/bin/meshway", args.binary.read_bytes(), 0o755), ("lib/systemd/system/meshway.service", service, 0o644), ("usr/share/doc/meshway/copyright", (root / "LICENSE").read_bytes(), 0o644)])),
    ])
    args.output.mkdir(parents=True, exist_ok=True)
    destination = args.output / f"meshway_{args.version}_{args.arch}.deb"
    destination.write_bytes(package)
    print(f"Created {destination}; no package was installed")


if __name__ == "__main__":
    main()
