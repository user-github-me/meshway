#!/usr/bin/env python3
"""Restrictive dual-NAT and relay loss lab in disposable Docker namespaces.

Every peer has its own network namespace. Each private peer is behind its own
iptables MASQUERADE router. Inbound initiation is denied. Only the public relay
is shared. No host route, DNS, proxy or active VPN is changed.
"""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid
import urllib.request


def run(arguments):
    result = subprocess.run(arguments, capture_output=True, text=True)
    if result.returncode: raise RuntimeError(result.stderr.strip()[-2000:])
    return result.stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='Trusted locally built deploy/Dockerfile.lab image with iptables/ip/tc')
    parser.add_argument('--binary', type=Path, default=Path('.tools/cloud/meshway-linux-arm64'))
    parser.add_argument('--harness', type=Path, default=Path('.tools/cloud/meshway-net-lab-linux-arm64'))
    parser.add_argument('--local-binary', type=Path, default=Path('dist/meshway'))
    parser.add_argument('--output', type=Path, default=Path('artifacts/nat-validation.json'))
    args = parser.parse_args()
    # A development VPN may synthesize reserved DNS addresses. Obtain public DNS
    # answers over authenticated HTTPS and use them only in the disposable exit.
    request=urllib.request.Request('https://cloudflare-dns.com/dns-query?name=example.com&type=A',headers={'accept':'application/dns-json'})
    with urllib.request.urlopen(request,timeout=20) as response:answer=json.load(response)
    public_answers=[str(ipaddress.ip_address(item['data'])) for item in answer.get('Answer',[]) if item.get('type')==1 and ipaddress.ip_address(item['data']).is_global]
    if not public_answers:raise RuntimeError('Public HTTPS DNS answer unavailable')
    prefix = 'meshway-nat-' + uuid.uuid4().hex[:8]
    networks, containers = [], []
    results = {'test':'dual restrictive NAT, explicit circuit relay, reconnect and 3% packet loss', 'host_network_modified':False, 'tests':{}}
    try:
        with tempfile.TemporaryDirectory(prefix=prefix + '-') as temporary:
            directory = Path(temporary)
            # Pick a distinct owned address block for this invocation.
            block = 96 + int(uuid.uuid4().hex[:2],16) % 64
            public, private1, private2 = f'172.{block}.0', f'172.{block}.1', f'172.{block}.2'
            for label, subnet in [('public',public),('client',private1),('exit',private2)]:
                name=prefix+'-'+label
                run(['docker','network','create','--subnet',subnet+'.0/24',name]);networks.append(name)
            for label, network, address in [('router1',networks[0],public+'.2'),('router2',networks[0],public+'.3'),('relay',networks[0],public+'.4'),('client',networks[1],private1+'.3'),('exit',networks[2],private2+'.3')]:
                name=prefix+'-'+label
                state=directory/label;state.mkdir(mode=0o700)
                run(['docker','run','-d','--name',name,'--network',network,'--ip',address,'--cap-add','NET_ADMIN','--sysctl','net.ipv4.ip_forward=1','--user','0:0','-v',f'{state}:/lab','-v',f'{args.binary.resolve()}:/meshway:ro','-v',f'{args.harness.resolve()}:/harness:ro','--entrypoint','/bin/sh',args.image,'-c','sleep 1800']);containers.append(name)
            run(['docker','network','connect','--ip',private1+'.2',networks[1],prefix+'-router1'])
            run(['docker','network','connect','--ip',private2+'.2',networks[2],prefix+'-router2'])
            def shell(label, command):return run(['docker','exec',prefix+'-'+label,'sh','-c',command])
            for index, private in [(1,private1),(2,private2)]:
                shell('router'+str(index),'iptables -P FORWARD DROP; iptables -A FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT; iptables -A FORWARD -s '+private+'.0/24 -d '+public+'.4 -j ACCEPT; iptables -A FORWARD -s '+private+'.0/24 -d 172.16.0.0/12 -j DROP; iptables -A FORWARD -s '+private+'.0/24 -j ACCEPT; iptables -t nat -A POSTROUTING -s '+private+'.0/24 -j MASQUERADE')
            shell('client','ip route replace default via '+private1+'.2')
            shell('exit','ip route replace default via '+private2+'.2')
            configs={}
            ids={}
            for label in ['client','exit','relay']:
                path=directory/label/'config.json'
                run([str(args.local_binary.resolve()),'init','--config',str(path)])
                ids[label]=run([str(args.local_binary.resolve()),'identity','--config',str(path)])
                configuration=json.loads(path.read_text())
                configuration.update(state_dir='/lab/state',listen_addresses=['/ip4/0.0.0.0/tcp/4001'],api_listen='127.0.0.1:7777',socks_listen='127.0.0.1:1080' if label=='client' else '',relay=label=='relay',exit=label=='exit',advertise=True)
                configs[label]=configuration
            relay_address=f'/ip4/{public}.4/tcp/4001/p2p/{ids["relay"]}'
            configs['exit'].update(allowed_peers=[ids['client']],bootstrap_peers=[relay_address])
            configs['client'].update(upstream_peer=ids['exit'],bootstrap_peers=[relay_address])
            for label,configuration in configs.items():
                path=directory/label/'config.json';path.write_text(json.dumps(configuration));os.chmod(path,0o600)
                argument=' --reserve-relay '+relay_address if label=='exit' else ''
                shell(label,'/harness --config /lab/config.json'+argument+' >/lab/log 2>&1 & echo $! >/lab/pid')
            shell('exit',"printf '%s example.com\\n' "+' '.join(public_answers)+" >> /etc/hosts")
            results['dns_resolution']='HTTPS public resolver answers scoped to disposable exit /etc/hosts; reserved synthetic host VPN DNS never allowed'
            def control(operation,target=''):
                return run(['docker','exec',prefix+'-client','/harness','--config','/lab/config.json','--operation',operation,'--target',target])
            for attempt in range(100):
                try:control('status');break
                except RuntimeError:
                    if attempt==99:raise
                    time.sleep(.1)
            # Router denies inbound private destinations, so direct dialing must actually fail.
            direct=f'/ip4/{private2}.3/tcp/4001/p2p/{ids["exit"]}'
            try:control('connect',direct)
            except RuntimeError:results['tests']['direct_inbound_blocked']='PASS'
            else:raise RuntimeError('Restrictive NAT unexpectedly allowed a direct connection')
            circuit=relay_address+'/p2p-circuit/p2p/'+ids['exit']
            for attempt in range(30):
                try:control('connect',circuit);break
                except RuntimeError:
                    if attempt==29:raise
                    time.sleep(1)
            results['tests']['circuit_https']=json.loads(control('https','https://example.com/'))
            time.sleep(1.2)
            logs=(directory/'client'/'log').read_text()
            results['transport_observations']=[json.loads(line) for line in logs.splitlines() if line.startswith('{')]
            if '"limited":true' not in logs or 'p2p-circuit' not in logs:raise RuntimeError('Expected real circuit-limited transport observation')
            results['tests']['actual_circuit_transport']='PASS'
            shell('relay','tc qdisc replace dev eth0 root netem delay 80ms 20ms loss 3%')
            results['tests']['cellular_like_delay_loss']=json.loads(control('https','https://example.com/'))
            shell('relay','tc qdisc del dev eth0 root')
            shell('relay','kill $(cat /lab/pid)')
            for attempt in range(100):
                if not json.loads(control('status'))['upstream_connected']:break
                time.sleep(.1)
            else:raise RuntimeError('Relay failure remained incorrectly connected')
            results['tests']['outage_detected']='PASS'
            shell('relay','/harness --config /lab/config.json >/lab/log 2>&1 & echo $! >/lab/pid')
            for attempt in range(40):
                try:control('connect',circuit);results['tests']['restart_https']=json.loads(control('https','https://example.com/'));break
                except RuntimeError:
                    if attempt==39:raise
                    time.sleep(1)
            results['status']='PASS'
    except Exception as error:
        results.update(status='FAIL',error=str(error));raise
    finally:
        for name in reversed(containers):subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        for name in reversed(networks):subprocess.run(['docker','network','rm',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        results['cleanup']='owned containers and networks removed'
        args.output.parent.mkdir(parents=True,exist_ok=True);args.output.write_text(json.dumps(results,indent=2)+'\n')
        print(json.dumps(results,indent=2))


if __name__=='__main__':main()
