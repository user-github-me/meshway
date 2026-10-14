#!/usr/bin/env python3
"""Collect exact pinned dependency license texts from public source caches."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',type=Path,default=Path('THIRD_PARTY_NOTICES.txt'))
    args=parser.parse_args()
    root=Path(__file__).resolve().parents[1]
    local=root/'.tools/go/bin/go'
    go=os.environ.get('GO_BINARY',str(local) if local.is_file() else 'go')
    subprocess.run([go,'mod','download','all'],cwd=root,check=True)
    data=subprocess.check_output([go,'list','-m','-json','all'],cwd=root,text=True)
    decoder=json.JSONDecoder();modules=[]
    while data.strip():
        value,count=decoder.raw_decode(data.lstrip());modules.append(value);data=data.lstrip()[count:]
    sections=['Meshway third-party notices\n\nThese notices reproduce upstream license files for the exact pinned Go module graph and bundled web/desktop runtime dependencies. They preserve upstream terms; inclusion does not imply every dependency is linked into every artifact. Meshway is licensed under Apache-2.0.\n']
    def add(label,path):
        sections.append('\n'+'='*78+'\n'+label+'\n'+'='*78+'\n'+path.read_text(errors='replace'))
    for module in sorted(modules,key=lambda value:value['Path']):
        if module.get('Main'):continue
        directory=Path(module['Dir'])
        files=sorted(path for path in directory.iterdir() if path.is_file() and re.match(r'^(licen[sc]e|copying|copyright|notice)',path.name,re.I))
        if not files:raise SystemExit('Missing upstream license: '+module['Path'])
        for path in files:add(module['Path']+'@'+module['Version']+' / '+path.name,path)
    package=root/'package-lock.json'
    lock=json.loads(package.read_text())
    for name,metadata in sorted(lock.get('packages',{}).items()):
        if not name.startswith('node_modules/') or metadata.get('dev') or metadata.get('link'):continue
        directory=root/name
        if not directory.is_dir():continue
        files=sorted(path for path in directory.iterdir() if path.is_file() and re.match(r'^(licen[sc]e|copying|copyright|notice|third.?party)',path.name,re.I))
        if not files:raise SystemExit('Missing upstream license: '+name)
        for path in files:add(name.removeprefix('node_modules/')+'@'+metadata.get('version','')+' / '+path.name,path)
    goroot=Path(subprocess.check_output([go,'env','GOROOT'],text=True).strip())
    for name in ('LICENSE','PATENTS'):
        if (goroot/name).is_file():add('Go standard library / '+name,goroot/name)
    dotnet=root/'.tools/native/dotnet'
    for name in ('LICENSE.txt','ThirdPartyNotices.txt'):
        if (dotnet/name).is_file():add('Microsoft .NET bundled desktop runtime / '+Path(name).name,dotnet/name)
    for sdk in sorted(dotnet.glob('sdk/*/Sdks/Microsoft.NET.Sdk.WindowsDesktop')):
        for name in ('LICENSE.TXT','THIRD-PARTY-NOTICES.TXT'):
            if (sdk/name).is_file():add('Microsoft .NET Windows desktop runtime / '+name,sdk/name)
    wintun=root/'dist/Meshway-win-x64/WINTUN-LICENSE.txt'
    if wintun.is_file():add('Wintun 0.14.1 / upstream license',wintun)
    output=root/args.output if not args.output.is_absolute() else args.output
    output.parent.mkdir(parents=True,exist_ok=True)
    output.write_text('\n'.join(sections)+'\n')
    print('Generated third-party notices:',len(modules)-1,'Go modules; output',args.output)


if __name__=='__main__':main()
