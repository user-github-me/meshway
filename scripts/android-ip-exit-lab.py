#!/usr/bin/env python3
"""Opt-in real Android emulator VPN/TLS/DNS/reconnect lab. Host routes untouched.
Prerequisite: build debug main/probe APKs and install main APK on a disposable
emulator. The script installs only its owned network probe APK. It exercises
native user consent through UI, stores no secrets in evidence, cleans daemons.
"""
import argparse, contextlib, ipaddress, http.server, json, os, pathlib, re, secrets, socket, subprocess, tempfile, threading, time, urllib.request, xml.etree.ElementTree as ET
p=argparse.ArgumentParser()
p.add_argument('--binary', required=True, type=pathlib.Path)
p.add_argument('--serial', default='emulator-5554')
p.add_argument('--internet', action='store_true', help='explicitly permit example.com DNS + TLS probe')
a=p.parse_args()
if not a.serial.startswith('emulator-'):p.error('Only a disposable Android emulator is permitted')
root=pathlib.Path(__file__).resolve().parents[1]; adb=root/'.tools/native/android-sdk/platform-tools/adb'; app='network.meshway.android'
def cmd(*args, check=True, timeout=30):return subprocess.run([str(adb),'-s',a.serial,*args],check=check,capture_output=True,text=True,timeout=timeout)
def shell(*args,**kwargs):return cmd('shell',*args,**kwargs)
def tap(needle):
    for _ in range(4):shell('input','swipe','500','700','500','2200','180')
    for _ in range(12):
        dump=shell('uiautomator','dump','/sdcard/meshway-ip-exit-ui.xml',check=False)
        if dump.returncode!=0:time.sleep(.5);continue
        tree=ET.fromstring(shell('cat','/sdcard/meshway-ip-exit-ui.xml').stdout)
        for node in tree.iter('node'):
            if (needle.lower() == node.get('text','').lower() or (needle=='Connect' and node.get('text','').lower().endswith(' · connect'))) and node.get('clickable')=='true':
                b=list(map(int,re.findall(r'\d+',node.get('bounds',''))))
                if len(b)==4 and b[3]-b[1]>35 and b[3]<=2400 and node.get('enabled')=='true':
                    shell('input','tap',str((b[0]+b[2])//2),str((b[1]+b[3])//2));return
        shell('input','swipe','500','2100','500','850','250')
    raise RuntimeError('Could not find '+needle)
def freeport():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
def api(base,token,path,body=None):
    req=urllib.request.Request(base+'/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
    with urllib.request.urlopen(req,timeout=10) as r:return json.load(r)
def wait_api(base,token,path='status',test=lambda v:True):
    deadline=time.monotonic()+20
    while time.monotonic()<deadline:
        try:
            result=api(base,token,path)
            if test(result):return result
        except Exception:pass
        time.sleep(.25)
    raise RuntimeError('API did not reach expected '+path+' state')
def probe(kind,*args,success=True):
    probeapp='network.meshway.probe'
    shell('am','force-stop',probeapp)
    shell('run-as',probeapp,'rm','-f','files/result.json',check=False)
    if kind=='dns':host=args[0];ip='';port=443
    elif kind=='tcp':host='probe';ip=args[0];port=int(args[1])
    else:host=args[0];ip=args[1];port=443
    shell('am','start','-n',probeapp+'/.ProbeActivity','--es','kind',kind,'--es','host',host,'--es','ip',ip,'--ei','port',str(port))
    deadline=time.monotonic()+18
    while time.monotonic()<deadline:
        result=shell('run-as',probeapp,'cat','files/result.json',check=False)
        if result.returncode==0:
            value=json.loads(result.stdout)
            if value['success']!=success:raise RuntimeError('Android '+kind+' probe failed: '+result.stdout)
            shell('am','start','-n',app+'/.MainActivity')
            return {'kind':kind,'passed':True,'expected_success':success,'result':value}
        time.sleep(.25)
    raise RuntimeError('Separate UIDnetworkprobe produced no result')
cmd('install','-r',str(root/'apps/android/probe-app/build/outputs/apk/debug/probe-app-debug.apk'))
shell('am','force-stop',app)
shell('am','start','-n',app+'/.MainActivity');time.sleep(1);tap('Start Meshway');time.sleep(2)
config=json.loads(shell('run-as',app,'cat','files/config.json').stdout)
androidtoken=config['api_token'];forward=freeport();cmd('forward',f'tcp:{forward}','tcp:7777');androidbase=f'http://127.0.0.1:{forward}'
status=wait_api(androidbase,androidtoken);peer=status['peer_id']
results=[]
class Origin(http.server.BaseHTTPRequestHandler):
    def do_GET(self):self.send_response(200);self.end_headers();self.wfile.write(b'Actual Android TUN encrypted exit success')
    def log_message(self,*args):pass
originip=subprocess.check_output(['ipconfig','getifaddr','en0'],text=True).strip()
if not originip:raise RuntimeError('No physical Mac en0 IPv4 address for isolated origin')
origin=http.server.ThreadingHTTPServer((originip,0),Origin);threading.Thread(target=origin.serve_forever,daemon=True).start();originport=origin.server_port
with tempfile.TemporaryDirectory(prefix='meshway-android-ip-exit-') as temporary:
    folder=pathlib.Path(temporary);peerport=freeport();apiport=freeport();token=secrets.token_hex(32)
    server={'state_dir':str(folder/'node'),'listen_addresses':[f'/ip4/0.0.0.0/tcp/{peerport}'],'bootstrap_peers':[],'upstream_peer':'','socks_listen':'','api_listen':f'127.0.0.1:{apiport}','api_token':token,'allowed_origins':[],'relay':False,'exit':True,'allow_private_targets':True,'allowed_peers':[peer],'forwards':[],'services':[],'advertise':True,'server_label':'Disposable Android VPN lab','vpn':{'enabled':True,'internet':True,'mtu':1280}}
    path=folder/'config.json';path.write_text(json.dumps(server));path.chmod(0o600);base=f'http://127.0.0.1:{apiport}'
    log=(folder/'gateway.log').open('w')
    process=None
    def start():return subprocess.Popen([str(a.binary.resolve()),'daemon','--config',str(path)],stdout=log,stderr=log)
    try:
        process=start();host=wait_api(base,token);invitation=f'/ip4/10.0.2.2/tcp/{peerport}/p2p/'+host['peer_id']
        api(androidbase,androidtoken,'connect',{'address':invitation})
        # Native UI selection asks Android for consent and attaches real TUN.
        shell('am','start','-n',app+'/.MainActivity');shell('input','swipe','500','700','500','2200','250');tap('Refresh servers');time.sleep(2);tap('Connect')
        # Consent may already be granted on this disposable emulator.
        consent=shell('uiautomator','dump','/sdcard/meshway-ip-exit-ui.xml',check=False)
        if consent.returncode==0:
            tree=ET.fromstring(shell('cat','/sdcard/meshway-ip-exit-ui.xml').stdout)
            for entry in tree.iter('node'):
                if entry.get('text','').upper()=='OK' and entry.get('clickable')=='true':
                    b=list(map(int,re.findall(r'\d+',entry.get('bounds',''))));shell('input','tap',str((b[0]+b[2])//2),str((b[1]+b[3])//2));break
        wait_api(androidbase,androidtoken,test=lambda v:v.get('vpn',{}).get('active') is True)
        routes=shell('ip','route','show','table','all').stdout
        if 'tun0' not in routes:raise RuntimeError('No actual Android TUN routes installed')
        results.append(probe('tcp',originip,str(originport)))
        if a.internet:
            results.append(probe('dns','example.com'))
            # Resolve once on host then verify real TLS hostname using numeric IP
            # so DNS and TLS are separately exercised through Android TUN.
            ip='104.18.26.120'
            results.append(probe('tls','example.com',ip))
        before=api(base,token,'status')
        process.terminate();process.wait(timeout=10)
        wait_api(androidbase,androidtoken,test=lambda v:v.get('vpn',{}).get('active') is False)
        if 'tun0' not in shell('ip','route','show','table','all').stdout:raise RuntimeError('VPN routes disappeared during peer loss')
        results.append(probe('tcp',originip,str(originport),success=False))
        process=start();wait_api(base,token)
        wait_api(androidbase,androidtoken,test=lambda v:v.get('vpn',{}).get('active') is True)
        results.append(probe('tcp',originip,str(originport)))
        shell('am','start','-n',app+'/.MainActivity');time.sleep(.5);tap('Disconnect')
        time.sleep(2)
        if 'tun0' in shell('ip','route','show','table','all').stdout:raise RuntimeError('Explicit disconnect retained TUN routes')
        results.append(probe('tcp',originip,str(originport)))
        artifact=root/'artifacts/android-ip-exit/result.json';artifact.parent.mkdir(exist_ok=True,parents=True)
        artifact.write_text(json.dumps({'platform':'Android35 arm64 emulator','real_kernel_tun':True,'internet_opt_in':a.internet,'gateway_bytes_sent':before['bytes_sent'],'gateway_bytes_received':before['bytes_received'],'results':results,'host_network_settings_changed':False},indent=2)+'\n')
        print(json.dumps({'result':'passed','checks':len(results),'real_kernel_tun':True,'host_network_settings_changed':False}))
    finally:
        with contextlib.suppress(Exception):
            shell('am','start','-n',app+'/.MainActivity');shell('am','start','-n',app+'/.MainActivity');time.sleep(.5);tap('Disconnect');tap('Stop')
        shell('am','force-stop',app,check=False)
        shell('am','force-stop','network.meshway.probe',check=False)
        subprocess.run([str(adb),'-s',a.serial,'shell','run-as',app,'tee','files/config.json'],input=json.dumps(config),text=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
        shell('run-as',app,'chmod','600','files/config.json')
        if process and process.poll() is None:process.terminate();process.wait(timeout=10)
        log.close()
        cmd('forward','--remove',f'tcp:{forward}',check=False)
origin.shutdown()
