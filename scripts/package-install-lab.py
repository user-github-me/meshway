#!/usr/bin/env python3
"""Execute Debian/RPM installation and removal only inside owned containers."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import re
import subprocess
import uuid


def run(*args):
    result=subprocess.run(args,text=True,capture_output=True,timeout=300)
    if result.returncode:
        raise RuntimeError(result.stderr[-2000:] or result.stdout[-2000:])
    return result.stdout


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--format',choices=['deb','rpm'],required=True)
    parser.add_argument('--image',required=True,help='Explicit trusted distro image; architecture must match package')
    parser.add_argument('--package',required=True,type=Path)
    parser.add_argument('--version',default='0.1.0-alpha.1')
    parser.add_argument('--output',type=Path)
    args=parser.parse_args()
    if not args.package.is_file():parser.error('Package must exist')
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.]+)?',args.version):parser.error('Use a semantic version')
    output=args.output or Path('artifacts')/f'{args.format}-install-validation.json'
    name='meshway-package-lab-'+uuid.uuid4().hex[:10]
    result={'format':args.format,'image':args.image,'package':args.package.name,'package_sha256':hashlib.sha256(args.package.read_bytes()).hexdigest(),'version':args.version,'checked_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
    if args.format=='deb':
        install='apt-get update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y /package.deb'
        installed='dpkg-query -W meshway >/dev/null'
        remove='dpkg --remove meshway >/dev/null'
        extension='deb'
    else:
        install='dnf install -y /package.rpm'
        installed='rpm -q meshway >/dev/null'
        remove='rpm -e meshway >/dev/null'
        extension='rpm'
    script=f'''set -eu
stage=dependencies
trap 'printf "package validation stopped at %s\\n" "$stage"' EXIT
{install}
stage=installed
{installed}
stage=version
test "$(meshway version)" = 'Meshway {args.version}'
stage=private-state
getent passwd meshway >/dev/null
test "$(stat -c %a /var/lib/meshway)" = 700
test "$(stat -c %U /var/lib/meshway/config.json)" = meshway
test "$(stat -c %a /var/lib/meshway/config.json)" = 600
command -v ip >/dev/null
command -v nft >/dev/null
stage=service-disabled
test ! -e /etc/systemd/system/multi-user.target.wants/meshway.service
stage=remove
{remove}
test ! -e /usr/bin/meshway
test -f /var/lib/meshway/config.json
echo PACKAGE_INSTALL_REMOVE_PASS
'''
    try:
        # Network/DNS/route changes and distro dependencies remain container-local.
        run('docker','create','--name',name,'--user','0:0','--entrypoint','/bin/sh',args.image,'-c',script)
        run('docker','cp',str(args.package.resolve()),name+':/package.'+extension)
        # A fixed internal package path avoids exposing the entire release/state directory.
        run('docker','start',name)
        exit_code=run('docker','wait',name).strip()
        logged=subprocess.run(['docker','logs',name],text=True,capture_output=True,timeout=30)
        logs=logged.stdout+logged.stderr
        if exit_code!='0' or 'PACKAGE_INSTALL_REMOVE_PASS' not in logs:
            errors=[line for line in logs.splitlines() if any(term in line.lower() for term in ('error','failed','denied','meshway','runuser'))]
            raise RuntimeError('\n'.join(errors[-12:]) or logs[-2000:])
        result.update(status='PASS',native_version=True,private_state_permissions=True,vpn_dependencies=True,no_automatic_service_start=True,removal_preserves_account=True)
    except (RuntimeError,subprocess.TimeoutExpired) as error:
        result.update(status='FAIL',reason=str(error)[-2000:])
    finally:
        subprocess.run(['docker','rm','-f',name],capture_output=True,timeout=30)
        result['owned_container_removed']=subprocess.run(['docker','inspect',name],capture_output=True,timeout=30).returncode!=0
    output.parent.mkdir(parents=True,exist_ok=True)
    output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result,indent=2))
    return 0 if result['status']=='PASS' else 1


if __name__=='__main__':raise SystemExit(main())
