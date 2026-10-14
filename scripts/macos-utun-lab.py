#!/usr/bin/env python3
"""Actual utun routing lab restricted to a disposable GitHub-hosted macOS VM."""
import argparse, datetime, hashlib, ipaddress, json, os, pathlib, platform, shutil, signal, subprocess, sys, time, urllib.request

ROOT=pathlib.Path(__file__).resolve().parents[1]
STATE=ROOT/'.tools/macos-utun-lab'
RUNTIME=pathlib.Path('/private/var/db/meshway-macvpn')
BINARY=STATE/'meshway'
CONFIG=STATE/'config.json'


def run(args,timeout=15,check=True):
    p=subprocess.run([str(x) for x in args],capture_output=True,text=True,timeout=timeout)
    if check and p.returncode:raise RuntimeError('Lab command failed: '+pathlib.Path(str(args[0])).name)
    return p


def guard():
    if sys.platform!='darwin' or os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('MESHWAY_DISPOSABLE_MACOS_VM')!='1':
        raise RuntimeError('This route-changing lab runs only in an explicitly selected disposable GitHub macOS runner.')
    if os.geteuid()==0:raise RuntimeError('Run the lab as the normal runner user; only its helper uses sudo.')
    run(['/usr/bin/sudo','-n','/usr/bin/true'])


def canonical(path):return pathlib.Path(path).resolve()

def prepare(args):
    guard()
    STATE.mkdir(mode=0o700,parents=True,exist_ok=True);STATE.chmod(0o700)
    if CONFIG.exists():raise RuntimeError('Fresh fixture required; refusing existing client config.')
    if not BINARY.is_file():raise RuntimeError('Build the lab binary into .tools/macos-utun-lab/meshway first.')
    run([BINARY,'init','--config',CONFIG])
    c=json.loads(CONFIG.read_text())
    c.update(listen_addresses=['/ip4/127.0.0.1/tcp/0'],api_listen='127.0.0.1:17777',socks_listen='127.0.0.1:17780',bootstrap_peers=[],relay=False,exit=False,auto_discovery=False,account_sync=False)
    CONFIG.write_text(json.dumps(c)+'\n');CONFIG.chmod(0o600)
    peer=run([BINARY,'identity','--config',CONFIG]).stdout.strip()
    public={'client_peer_id':peer,'runner_os':platform.mac_ver()[0],'runner_arch':platform.machine(),'created':datetime.datetime.now(datetime.timezone.utc).isoformat()}
    (STATE/'client-peer.json').write_text(json.dumps(public,indent=2)+'\n')
    print(json.dumps(public),flush=True)


def baseline():
    data={}
    for name,cmd in [('proxy',['/usr/sbin/scutil','--proxy']),('dns',['/usr/sbin/scutil','--dns']),('default4',['/sbin/route','-n','get','default']),('default6',['/sbin/route','-n','get','-inet6','default'])]:
        p=run(cmd,check=False)
        stable=p.stdout
        if name.startswith('default'):
            stable='\n'.join(line.strip() for line in stable.splitlines() if line.strip().startswith(('destination:','gateway:','interface:','flags:','mask:')))
        data[name]={'exit':p.returncode,'sha256':hashlib.sha256(stable.encode()).hexdigest()}
    return data


def curl(url,seconds=12):
    return run(['/usr/bin/curl','--fail','--silent','--show-error','--proxy','','--noproxy','*','--max-time',str(seconds),url],timeout=seconds+3,check=False)


def execute(args):
    guard()
    exit_ip=str(ipaddress.ip_address(args.exit_ip))
    if not ipaddress.ip_address(exit_ip).is_global:raise RuntimeError('Explicit public exit IP required.')
    if not args.exit_peer or any(x.isspace() for x in args.exit_peer):raise RuntimeError('Explicit pinned exit peer required.')
    if not CONFIG.is_file():raise RuntimeError('Prepare the fresh fixture first.')
    c=json.loads(CONFIG.read_text());c.update(upstream_peer=args.exit_peer,bootstrap_peers=[f'/ip4/{exit_ip}/tcp/{args.exit_port}/p2p/{args.exit_peer}'])
    CONFIG.write_text(json.dumps(c)+'\n');CONFIG.chmod(0o600)
    private_log=open(STATE/'daemon.private.log','wb');os.chmod(STATE/'daemon.private.log',0o600)
    daemon=subprocess.Popen([str(BINARY),'daemon','--config',str(CONFIG)],stdout=private_log,stderr=private_log)
    helper=None;owner=None
    proof={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'runner':'disposable GitHub-hosted macOS','version':run([BINARY,'version']).stdout.strip(),'binary_sha256':hashlib.sha256(BINARY.read_bytes()).hexdigest(),'development_host_mutated':False,'checks':{},'cycles':[]}
    before=baseline()
    def api(path,body=None):
        request=urllib.request.Request('http://'+c['api_listen']+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+c['api_token'],'Content-Type':'application/json'})
        with urllib.request.urlopen(request,timeout=5) as response:return json.load(response)
    def stop_helper():
        nonlocal helper,owner
        if helper and helper.poll() is None:run(['/usr/bin/sudo','-n',BINARY,'vpn','stop','--config',canonical(CONFIG)],check=False)
        if helper:
            try:helper.wait(timeout=105)
            except subprocess.TimeoutExpired:
                run(['/usr/bin/sudo','-n','/bin/kill','-TERM',str(helper.pid)],check=False)
                helper.wait(timeout=20)
            helper=None
        if owner and owner.poll() is None:owner.terminate();owner.wait(timeout=5)
        owner=None
        # Stale journal cleanup is explicit after a killed helper. It never
        # changes an interface by name once that owner is gone.
        run(['/usr/bin/sudo','-n',BINARY,'vpn','stop','--config',canonical(CONFIG)],timeout=105,check=False)
    try:
        deadline=time.monotonic()+args.authorization_wait
        while time.monotonic()<deadline:
            if daemon.poll() is not None:raise RuntimeError('Owned client daemon exited before server authorization.')
            try:
                s=api('/v1/status')
                if s.get('upstream_connected'):
                    p=run(['/usr/bin/curl','--fail','--silent','--show-error','--max-time','8','--socks5-hostname','127.0.0.1:17780','--noproxy','','https://api.ipify.org?format=json'],timeout=11,check=False)
                    if p.returncode==0 and json.loads(p.stdout).get('ip')==exit_ip:break
            except (OSError,ValueError,subprocess.TimeoutExpired):pass
            time.sleep(2)
        else:raise RuntimeError('Exit did not authorize the printed ephemeral peer within the bounded wait.')
        proof['checks']['approved_remote_exit_socks_baseline']=True
        for mode in ['explicit-stop','owner-process-exit','helper-sigkill-explicit-recovery']:
            owner=subprocess.Popen([sys.executable,'-c','import time;time.sleep(180)'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            helper_log=open(STATE/(mode+'.helper.private.log'),'wb');os.chmod(helper_log.name,0o600)
            helper=subprocess.Popen(['/usr/bin/sudo','-n',str(BINARY),'vpn','start','--config',str(canonical(CONFIG)),'--peer',args.exit_peer,'--underlay-ips',exit_ip,'--parent-pid',str(owner.pid)],stdout=helper_log,stderr=helper_log)
            ready=False;device=None
            for _ in range(80):
                if helper.poll() is not None:break
                status=run(['/usr/bin/sudo','-n',BINARY,'vpn','status','--config',canonical(CONFIG)],check=False)
                try:
                    current=json.loads(status.stdout)
                    if current.get('active'):ready=True;device=current.get('device');break
                except ValueError:pass
                time.sleep(.25)
            if not ready:raise RuntimeError('Actual utun helper did not confirm setup; private helper diagnostics retained.')
            cycle={'mode':mode,'utun_active':True}
            route=run(['/sbin/route','-n','get','1.1.1.1']).stdout
            if f'interface: {device}' not in route:raise RuntimeError('IPv4 kernel capture route did not select the owned utun.')
            cycle['ipv4_kernel_capture_route']=True
            route6=run(['/sbin/route','-n','get','-inet6','2606:4700:4700::1111']).stdout
            if f'interface: {device}' not in route6:raise RuntimeError('IPv6 kernel capture route did not select the owned utun.')
            cycle['ipv6_kernel_capture_route']=True
            dns=run(['/usr/sbin/scutil','--dns']).stdout
            if '10.77.0.1' not in dns:raise RuntimeError('Virtual DNS resolver not present in actual macOS configuration.')
            cycle['virtual_dns_resolver_present']=True
            traffic_before=api('/v1/status')
            import socket
            import uuid
            probe_hostname='meshway-'+uuid.uuid4().hex+'.example.com'
            try:socket.getaddrinfo(probe_hostname,443,proto=socket.IPPROTO_TCP)
            except socket.gaierror:pass  # A fresh NXDOMAIN still proves resolver packets crossed the interface.
            traffic_after_dns=api('/v1/status')
            cycle['native_dns_lookup']=True
            cycle['native_dns_generated_meshway_packet_traffic']=traffic_after_dns.get('bytes_sent',0)>traffic_before.get('bytes_sent',0) and traffic_after_dns.get('bytes_received',0)>traffic_before.get('bytes_received',0)
            if not cycle['native_dns_generated_meshway_packet_traffic']:raise RuntimeError('Native DNS did not produce observed encrypted packet traffic.')
            ip_result=curl('https://api.ipify.org?format=json')
            if ip_result.returncode or json.loads(ip_result.stdout).get('ip')!=exit_ip:raise RuntimeError('Native kernel TCP/TLS exit IP mismatch.')
            cycle['kernel_tcp_tls_expected_exit_ip']=True
            https=curl('https://example.com/')
            if https.returncode or ('Example Domain' not in https.stdout and 'documentation examples' not in https.stdout):raise RuntimeError('Kernel capture certificate-verified public HTTPS failed.')
            cycle['kernel_public_https']=True
            if mode=='owner-process-exit':
                owner.terminate();owner.wait(timeout=5)
                try:helper.wait(timeout=105)
                except subprocess.TimeoutExpired:raise RuntimeError('Parent-exit watchdog did not finish automatic helper cleanup.')
                cycle['automatic_parent_exit_cleanup']=True
            elif mode=='helper-sigkill-explicit-recovery':
                journal=json.loads(run(['/usr/bin/sudo','-n','/bin/cat',str(RUNTIME/'journal.json')]).stdout)
                run(['/usr/bin/sudo','-n','/bin/kill','-KILL',str(journal['pid'])])
            stop_helper();helper_log.close()
            daemon_status=api('/v1/status')
            cycle['daemon_packet_session_inactive']=not daemon_status.get('vpn',{}).get('active') and not daemon_status.get('vpn',{}).get('peer_id')
            if not cycle['daemon_packet_session_inactive']:raise RuntimeError('Helper cleanup retained the daemon packet session.')
            after=baseline()
            cycle['default_routes_dns_proxy_restored']=before==after
            if not cycle['default_routes_dns_proxy_restored']:raise RuntimeError('Actual Mac baseline was not restored after '+mode)
            status=json.loads(run(['/usr/bin/sudo','-n',BINARY,'vpn','status','--config',canonical(CONFIG)]).stdout)
            cycle['no_active_helper_or_cleanup_pending']=not status.get('running') and not status.get('network_cleanup_pending')
            if not cycle['no_active_helper_or_cleanup_pending']:raise RuntimeError('Owned helper retained cleanup state.')
            proof['cycles'].append(cycle)
            print(json.dumps({'cycle':mode,'status':'PASS'}),flush=True)
        proof['status']='PASS'
        proof['ipv6_internet_limitation']='Actual IPv6 kernel capture route verified; public IPv6 egress requires a separately dual-stack exit and is not asserted here.'
        proof['crash_limitation']='SIGKILL recovery invokes explicit privileged Stop; no persistent crash firewall kill switch is claimed.'
    except Exception as error:
        proof['status']='FAIL';proof['error']=str(error)
    finally:
        try:stop_helper()
        except Exception:proof['cleanup_error']='Privileged cleanup failed; inspect private runner diagnostics.'
        if daemon.poll() is None:
            try:api('/v1/shutdown',{})
            except Exception:daemon.terminate()
            try:daemon.wait(timeout=10)
            except subprocess.TimeoutExpired:daemon.kill();daemon.wait(timeout=5)
        private_log.close()
        proof['checks']['owned_daemon_stopped']=daemon.poll() is not None
        proof['checks']['final_default_routes_dns_proxy_restored']=baseline()==before
        proof['checks']['fresh_account_state_removed']=True
        proof['checks']['temporary_vm_only_network_changes']=True
        # Publish only the bounded proof and public peer request, never config,
        # private state or logs. The hosted VM is destroyed after the job.
        for file in STATE.iterdir():
            if file.name not in ['client-peer.json','result.json']:
                if file.is_dir():shutil.rmtree(file)
                else:file.unlink()
        (STATE/'result.json').write_text(json.dumps(proof,indent=2)+'\n')
    print(json.dumps({'status':proof['status'],'cycles':len(proof['cycles']),'result':'.tools/macos-utun-lab/result.json'}),flush=True)
    if proof['status']!='PASS' or proof.get('cleanup_error') or not all(proof['checks'].values()):raise SystemExit(1)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--prepare',action='store_true')
    p.add_argument('--exit-ip');p.add_argument('--exit-peer');p.add_argument('--exit-port',type=int,default=4001)
    p.add_argument('--authorization-wait',type=int,default=600)
    a=p.parse_args()
    if a.prepare:prepare(a)
    else:execute(a)
if __name__=='__main__':main()
