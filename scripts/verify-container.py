#!/usr/bin/env python3
"""Verify a GHCR manifest anonymously; never use or print registry credentials."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import re
import urllib.error
import urllib.parse
import urllib.request


def request(url, headers=None):
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers or {}), timeout=45) as response:
        data=response.read(2*1024*1024+1)
        if len(data)>2*1024*1024:
            raise ValueError('Registry manifest response exceeds limit')
        return data,response.headers


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('image',help='ghcr.io/OWNER/IMAGE:VERSION')
    parser.add_argument('--output',type=Path,default=Path('artifacts/container-publication.json'))
    args=parser.parse_args()
    match=re.fullmatch(r'ghcr\.io/([a-z0-9._-]+/[a-z0-9._-]+):([A-Za-z0-9._-]+)',args.image)
    if not match:
        parser.error('Use an explicit GHCR owner/image:version')
    repository,tag=match.groups()
    result={'image':args.image,'checked_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'anonymous':True}
    try:
        query=urllib.parse.urlencode({'service':'ghcr.io','scope':f'repository:{repository}:pull'})
        # This is the registry's anonymous, short-lived read capability, never a
        # GitHub personal token. It stays in memory and is never logged.
        data,_=request('https://ghcr.io/token?'+query)
        capability=json.loads(data)['token']
        headers={'Authorization':'Bearer '+capability,'Accept':'application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json'}
        data,response_headers=request(f'https://ghcr.io/v2/{repository}/manifests/{tag}',headers)
        digest='sha256:'+hashlib.sha256(data).hexdigest()
        if response_headers.get('Docker-Content-Digest')!=digest:
            raise ValueError('Registry index digest does not match bytes')
        index=json.loads(data)
        platforms={entry.get('platform',{}).get('architecture'):entry for entry in index.get('manifests',[]) if entry.get('platform',{}).get('os')=='linux'}
        for architecture in ('amd64','arm64'):
            if architecture not in platforms:
                raise ValueError('Published index lacks linux/'+architecture)
            child_digest=platforms[architecture]['digest']
            if not re.fullmatch(r'sha256:[a-f0-9]{64}',child_digest):
                raise ValueError('Invalid child manifest digest')
            child,_=request(f'https://ghcr.io/v2/{repository}/manifests/{child_digest}',headers)
            if 'sha256:'+hashlib.sha256(child).hexdigest()!=child_digest:
                raise ValueError('Platform manifest digest does not match bytes')
        result.update(status='PASS',digest=digest,platforms=['linux/amd64','linux/arm64'])
    except urllib.error.HTTPError as error:
        result.update(status='UNAVAILABLE',anonymous_http_status=error.code,reason='Image absent or registry visibility does not allow anonymous pulling')
    except (ValueError,KeyError,urllib.error.URLError) as error:
        # Do not dump response bodies, request objects or authorization headers.
        result.update(status='FAIL',reason=type(error).__name__)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result,indent=2))
    return 0 if result['status']=='PASS' else 1


if __name__=='__main__':
    raise SystemExit(main())
