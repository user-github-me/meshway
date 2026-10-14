#!/usr/bin/env python3
"""Record exact local native artifact hashes, without touching private state."""
import argparse
import hashlib
import json
from pathlib import Path


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version',required=True)
    parser.add_argument('--output',type=Path,default=Path('release/native-builds.json'))
    parser.add_argument('--include-ios',action='store_true',help='Include locally built iOS developer artifacts only when explicitly requested')
    args=parser.parse_args()
    candidates=[Path('dist/meshway-android-debug.apk'),Path('dist/Meshway.app/Contents/MacOS/Meshway'),Path('dist/Meshway.app/Contents/Resources/meshway')]
    for runtime in ['win-x64','win-arm64']:
        for name in ['Meshway.exe','meshway-daemon.exe','meshway-vpn-helper.exe','wintun.dll','WINTUN-LICENSE.txt']:
            candidates.append(Path('dist')/('Meshway-'+runtime)/name)
    for platform,directory in [('macos','Release'),('ios','Release-iphoneos')]:
        if platform=='ios' and not args.include_ios:continue
        app=Path('dist')/('apple-'+platform)/'Build/Products'/directory/'Meshway.app'
        if platform=='macos':
            candidates.extend([app/'Contents/MacOS/Meshway',app/'Contents/Resources/meshway',app/'Contents/PlugIns/MeshwayPacketTunnel.appex/Contents/MacOS/MeshwayPacketTunnel'])
        else:
            candidates.extend([app/'Meshway',app/'PlugIns/MeshwayPacketTunnel.appex/MeshwayPacketTunnel'])
    entries=[]
    for path in candidates:
        if not path.is_file():continue
        data=path.read_bytes()
        entries.append({'path':str(path),'size':len(data),'sha256':hashlib.sha256(data).hexdigest()})
    result={'version':args.version,'release_type':'Experimental app builds without owner platform certificates; APK debug-signed; Wintun retains upstream vendor signature','artifacts':entries}
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print('Native artifact hashes recorded:',len(entries))


if __name__=='__main__':main()
