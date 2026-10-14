#!/usr/bin/env python3
"""Build/mount-verify a DMG around the existing tested universal native app."""
import argparse, hashlib, json, os, plistlib, shutil, subprocess, tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parent.parent

def run(args):
    p=subprocess.run(args,capture_output=True,check=True)
    return p.stdout

def files(root):
    result={}
    for p in sorted(root.rglob('*')):
        if p.is_symlink(): result[str(p.relative_to(root))]={'symlink':os.readlink(p)}
        elif p.is_file(): result[str(p.relative_to(root))]={'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'size':p.stat().st_size}
    return result

def main():
    ap=argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--app',type=Path,default=ROOT/'dist/apple-macos/Build/Products/Release/Meshway.app')
    ap.add_argument('--output',type=Path,default=ROOT/'.tools/release/installers/Meshway-macos.dmg')
    ap.add_argument('--version',default='0.1.0-alpha.2')
    a=ap.parse_args();a.output.parent.mkdir(parents=True,exist_ok=True)
    if not a.app.is_dir():raise RuntimeError('Tested native app missing')
    expected=files(a.app)
    for executable in ('Contents/MacOS/Meshway','Contents/Resources/meshway','Contents/PlugIns/MeshwayPacketTunnel.appex/Contents/MacOS/MeshwayPacketTunnel'):
        if set(run(['/usr/bin/lipo','-archs',str(a.app/executable)]).decode().split())!={'x86_64','arm64'}:raise RuntimeError('Universal app/core required')
    devices=[]
    with tempfile.TemporaryDirectory(prefix='meshway-dmg-') as temp:
        staging=Path(temp)/'image';staging.mkdir()
        run(['/usr/bin/ditto',str(a.app),str(staging/'Meshway.app')])
        os.symlink('/Applications',staging/'Applications')
        shutil.copy2(ROOT/'LICENSE',staging/'LICENSE')
        shutil.copy2(ROOT/'THIRD_PARTY_NOTICES.txt',staging/'THIRD_PARTY_NOTICES.txt')
        (staging/'READ ME.txt').write_text(f'Meshway {a.version}\n\nDrag Meshway.app to Applications, then choose a server and Connect. SOCKS and personal SSH tunnels work without Apple VPN signing. System proxy is an optional explicit setting for supported applications.\nThis is an unsigned experimental build. The Apple Network Extension requires legitimate Apple Developer signing and entitlements. A separate administrator utun helper is being qualified; its source and isolated tests do not establish working full-device VPN on a physical Mac.\nSource and build instructions: https://github.com/user-github-me/meshway\n')
        a.output.unlink(missing_ok=True)
        run(['/usr/bin/hdiutil','create','-volname','Meshway','-srcfolder',str(staging),'-format','UDZO','-ov',str(a.output)])
        try:
            mounted=plistlib.loads(run(['/usr/bin/hdiutil','attach','-readonly','-nobrowse','-plist',str(a.output)]))
            entities=mounted['system-entities'];devices=[e['dev-entry'] for e in entities if e.get('dev-entry')]
            mount=Path(next(e['mount-point'] for e in entities if e.get('mount-point')))
            if files(mount/'Meshway.app')!=expected:raise RuntimeError('Mounted app differs from tested native bundle')
            if os.readlink(mount/'Applications')!='/Applications':raise RuntimeError('Applications shortcut invalid')
            for name in ('LICENSE','THIRD_PARTY_NOTICES.txt'):
                if (mount/name).read_bytes()!=(ROOT/name).read_bytes():raise RuntimeError('Notice differs')
            result={'status':'PASS','artifact':str(a.output.relative_to(ROOT)),'bytes':a.output.stat().st_size,'sha256':hashlib.sha256(a.output.read_bytes()).hexdigest(),'mounted_app_exact_hashes':True,'universal_gui_and_daemon':True,'applications_shortcut':True,'license_notices':True,'host_network_modified':False,'limitation':'Unsigned experimental app. Apple Network Extension operation requires legitimate signing and entitlements; the separate administrator utun helper has not yet passed physical Mac route, resolver and failure-recovery qualification.'}
        finally:
            if devices:run(['/usr/bin/hdiutil','detach',devices[0]])
    result['mount_and_temporary_files_removed']=True
    out=ROOT/'artifacts/macos-dmg-validation.json';out.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result,indent=2))
if __name__=='__main__':main()
