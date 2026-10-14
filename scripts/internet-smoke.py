#!/usr/bin/env python3
"""Opt-in test of actual internet HTTPS through isolated Meshway proxy nodes.

No system routes, DNS, VPN, or proxy settings are changed. Configs/keys are
temporary and never printed. This proves internet egress, not cross-network NAT.
"""
import argparse
import json
import ipaddress
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request


def port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


def api(config, path, body=None):
    request = urllib.request.Request(
        f'http://{config["api_listen"]}/v1/{path}',
        data=None if body is None else json.dumps(body).encode(),
        headers={'Authorization': 'Bearer ' + config['api_token'], 'Content-Type': 'application/json'})
    # ProxyHandler({}) bypasses ambient proxy variables for local control traffic.
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=10) as response:
        return json.load(response)


def initialize(binary, directory, name):
    path = directory / name / 'config.json'
    subprocess.run([binary, 'init', '--config', str(path)], check=True, capture_output=True)
    config = json.loads(path.read_text())
    config.update(listen_addresses=['/ip4/127.0.0.1/tcp/0'],
                  socks_listen=f'127.0.0.1:{port()}', api_listen=f'127.0.0.1:{port()}')
    return path, config


def save(path, config):
    path.write_text(json.dumps(config))
    os.chmod(path, 0o600)


def start(binary, path, config, processes):
    process = subprocess.Popen([binary, 'daemon', '--config', str(path)],
                               stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    processes.append(process)
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError('Temporary daemon stopped: ' + process.stderr.read().decode())
        try:
            return api(config, 'status')
        except OSError:
            time.sleep(0.1)
    raise RuntimeError('Temporary daemon startup timed out')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='dist/meshway')
    parser.add_argument('--url', default='https://example.com/')
    parser.add_argument('--container-image', help='Optional trusted local Docker image containing meshway; tests a separate Linux network namespace')
    parser.add_argument('--container-public-host', action='append', default=[], help='Optional HOST:PUBLIC_IPV4 entry scoped to disposable container when host VPN supplies synthetic reserved DNS answers')
    args = parser.parse_args()
    if not args.url.startswith('https://'):
        parser.error('Use an HTTPS target')
    for entry in args.container_public_host:
        try:
            name, address=entry.rsplit(':',1)
            if not name or not ipaddress.ip_address(address).is_global:raise ValueError()
        except ValueError:parser.error('Container DNS override must contain a hostname and public IP')
    binary = str(Path(args.binary).resolve())
    processes = []
    try:
        with tempfile.TemporaryDirectory(prefix='meshway-internet-') as workspace:
            directory = Path(workspace)
            cpath, client = initialize(binary, directory, 'client')
            peer_id = subprocess.check_output([binary, 'identity', '--config', str(cpath)], text=True).strip()
            spath, server = initialize(binary, directory, 'exit')
            server.update(exit=True, advertise=True, server_label='Isolated internet test', allowed_peers=[peer_id], socks_listen='')
            if args.container_image:
                published_port=port()
                server_id=subprocess.check_output([binary,'identity','--config',str(spath)],text=True).strip()
                server.update(state_dir='/var/lib/meshway/state',listen_addresses=['/ip4/0.0.0.0/tcp/14001'],api_listen='')
                save(spath,server)
                command=['docker','run','--rm','--user',f'{os.getuid()}:{os.getgid()}',
                         '-v',f'{spath.parent}:/var/lib/meshway','-p',f'127.0.0.1:{published_port}:14001',
                         '--entrypoint','meshway',args.container_image,'daemon','--config','/var/lib/meshway/config.json']
                for entry in args.container_public_host:command[2:2]=['--add-host',entry]
                process=subprocess.Popen(command,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
                processes.append(process)
                for _ in range(100):
                    if process.poll() is not None: raise RuntimeError('Container gateway stopped: '+process.stderr.read().decode())
                    try:
                        with socket.create_connection(('127.0.0.1',published_port),timeout=0.2): break
                    except OSError: time.sleep(0.1)
                status={'peer_id':server_id,'addresses':[f'/ip4/127.0.0.1/tcp/{published_port}/p2p/{server_id}']}
            else:
                save(spath, server)
                status = start(binary, spath, server, processes)
            client.update(upstream_peer=status['peer_id'], bootstrap_peers=[status['addresses'][0]])
            save(cpath, client)
            start(binary, cpath, client, processes)
            api(client, 'connect', {'address': status['addresses'][0]})
            command = ['curl', '--silent', '--show-error', '--fail', '--max-time', '45',
                       '--socks5-hostname', client['socks_listen'], '--noproxy', '', args.url]
            result = subprocess.run(command, capture_output=True)
            if result.returncode:
                raise RuntimeError(f'HTTPS proxy request failed ({result.returncode}): {result.stderr.decode().strip()}')
            for _ in range(30):
                status = api(client, 'status')
                if status['bytes_sent'] > 0 and status['bytes_received'] > 0:
                    break
                time.sleep(0.1)
            assert result.stdout and status['bytes_sent'] > 0 and status['bytes_received'] > 0
            print(json.dumps({'test': 'HTTPS over SOCKS5 over encrypted peer', 'target': args.url,
                              'response_bytes': len(result.stdout), 'bytes_sent': status['bytes_sent'],
                              'bytes_received': status['bytes_received'], 'result': 'PASS',
                              'gateway_platform': 'Linux container' if args.container_image else 'native local daemon',
                              'system_network_settings_changed': False}, indent=2))
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
                try: process.wait(timeout=10)
                except subprocess.TimeoutExpired: process.kill(); process.wait()


if __name__ == '__main__':
    main()
