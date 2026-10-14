#!/usr/bin/env python3
"""Full production macOS helper on disposable hosted VM and local peers only."""
import pathlib,sys,os,json,time,socket,struct,threading,subprocess,urllib.request,urllib.error,uuid,signal,hashlib
ROOT=pathlib.Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'scripts'))
import importlib.util
spec=importlib.util.spec_from_file_location('maclab',ROOT/'scripts/macos-utun-lab.py');lab=importlib.util.module_from_spec(spec);spec.loader.exec_module(lab)
lab.guard()
STATE=ROOT/'.tools/utun-loopback';STATE.mkdir(parents=True,exist_ok=True,mode=0o700);STATE.chmod(0o700)
BINARY=ROOT/'.tools/macos-utun-lab/meshway'
processes=[];helper=None;owner=None
proof={'status':'FAIL','scope':'full privileged production helper on disposable hosted Mac with loopback encrypted peers/mock DNS','development_host_modified':False,'checks':{}}
proof['binary_sha256']=hashlib.sha256(BINARY.read_bytes()).hexdigest()
proof['source_commit']=os.environ.get('MESHWAY_TESTED_REF','unknown')
before=lab.baseline()
def api(c,path,body=None):
 r=urllib.request.Request('http://'+c['api_listen']+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+c['api_token'],'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(r,timeout=5) as f:return json.load(f)
 except urllib.error.HTTPError as e:
  message=json.loads(e.read(2048)).get('error','control request rejected')
  raise RuntimeError('local API '+path+' returned '+str(e.code)+': '+str(message)[:512]) from None
def config(name):
 d=STATE/name;d.mkdir(mode=0o700);p=d/'config.json';lab.run([BINARY,'init','--config',p]);c=json.loads(p.read_text());c.update(listen_addresses=['/ip4/127.0.0.1/tcp/0'],bootstrap_peers=[],socks_listen='',api_listen='127.0.0.1:0',relay=False,exit=False,auto_discovery=False,account_sync=False);return p,c
def save(p,c):p.write_text(json.dumps(c)+'\n');p.chmod(0o600)
def launch(p):
 log=open(p.parent/'daemon.private.log','wb');os.chmod(log.name,0o600)
 process=subprocess.Popen([str(BINARY),'daemon','--config',str(p)],stdout=log,stderr=log);processes.append((process,log))
 # APIport must be fixed for control after startup.
 return process
try:
 dns_script=STATE/'dns-fixture.py'
 dns_script.write_text("import socket\ns=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('127.0.0.1',53))\nwhile True:\n q,a=s.recvfrom(1500)\n if len(q)<12:continue\n r=bytearray(q);r[2]=0x81;r[3]=0x83;r[6:12]=bytes(6);s.sendto(r,a)\n")
 dns_script.chmod(0o600)
 dns_counter=STATE/'dns-count.txt'
 script=dns_script.read_text().replace('while True:',"counter=0\nwhile True:").replace("r=bytearray(q)","counter+=1;open("+repr(str(dns_counter))+",'w').write(str(counter));r=bytearray(q)")
 dns_script.write_text(script)
 dns_process=subprocess.Popen(['/usr/bin/sudo','-n',sys.executable,str(dns_script)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 processes.append((dns_process,open(os.devnull,'wb')))
 time.sleep(.3)
 if dns_process.poll() is not None:raise RuntimeError('owned loopback DNS53 fixture could not start')
 cp,client=config('client');sp,server=config('exit')
 def port():
  s=socket.socket();s.bind(('127.0.0.1',0));n=s.getsockname()[1];s.close();return n
 client['api_listen']='127.0.0.1:'+str(port());server['api_listen']='127.0.0.1:'+str(port());save(cp,client);save(sp,server)
 peer=lab.run([BINARY,'identity','--config',cp]).stdout.strip()
 server.update(exit=True,advertise=True,allow_private_targets=True,allowed_peers=[peer],vpn={'enabled':True,'internet':True,'mtu':1280,'dns_resolver':'127.0.0.1:53'})
 save(sp,server);launch(sp);launch(cp)
 for _ in range(60):
  try:ss=api(server,'/v1/status');cs=api(client,'/v1/status');break
  except OSError:time.sleep(.2)
 else:raise RuntimeError('loopback peers did not start')
 api(client,'/v1/connect',{'address':ss['addresses'][0]});api(client,'/v1/select',{'peer_id':ss['peer_id']})
 owner=subprocess.Popen([sys.executable,'-c','import time;time.sleep(180)'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 log=open(STATE/'helper.private.log','wb');os.chmod(log.name,0o600)
 helper=subprocess.Popen(['/usr/bin/sudo','-n','/usr/bin/env','GITHUB_ACTIONS=true','MESHWAY_DISPOSABLE_MACOS_VM=1','MESHWAY_UTUN_PACKET_DIAGNOSTICS=1',str(BINARY),'vpn','start','--config',str(cp.resolve()),'--peer',ss['peer_id'],'--underlay-ips','192.0.2.123','--parent-pid',str(owner.pid)],stdout=log,stderr=log)
 for _ in range(100):
  if helper.poll() is not None:raise RuntimeError('full helper setup exited: '+str(lab.safe_helper_failure(pathlib.Path(log.name))))
  current=lab.run(['/usr/bin/sudo','-n',BINARY,'vpn','status','--config',cp.resolve()],check=False)
  try:
   if json.loads(current.stdout).get('active'):device=json.loads(current.stdout)['device'];break
  except ValueError:pass
  time.sleep(.2)
 else:raise RuntimeError('full helper setup not confirmed')
 proof['checks']['full_helper_verified_setup']=True
 for family,target in [('ipv4','10.77.0.1'),('ipv6','fd77::1')]:
  route=lab.run(['/sbin/route','-n','get',*(['-inet6'] if family=='ipv6' else []),target]).stdout
  proof['checks'][family+'_virtual_dns_route']=('interface: '+device in route)
 before_dns=api(client,'/v1/status');before_server=api(server,'/v1/status')
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(6)
 q=struct.pack('!HHHHHH',18723,256,1,0,0,0)+b'\x07example\x03com\x00'+struct.pack('!HH',1,1)
 s.sendto(q,('10.77.0.1',53))
 try:a,_=s.recvfrom(1500);proof['checks']['full_helper_unbound_udp_dns_reply']=len(a)>=12 and struct.unpack('!H',a[:2])[0]==18723
 except OSError:proof['checks']['full_helper_unbound_udp_dns_reply']=False
 s.close();after=api(client,'/v1/status');proof['packet_counters']={'sent_delta':after['bytes_sent']-before_dns['bytes_sent'],'received_delta':after['bytes_received']-before_dns['bytes_received']}
 after_server=api(server,'/v1/status');proof['server_packet_counters']={'sent_delta':after_server['bytes_sent']-before_server['bytes_sent'],'received_delta':after_server['bytes_received']-before_server['bytes_received']}
 proof['mock_dns_queries_received']=int(dns_counter.read_text()) if dns_counter.exists() else 0
 if not proof['checks']['full_helper_unbound_udp_dns_reply']:raise RuntimeError('full helper UDP DNS has no reply')
 concurrent=[]
 def dns_probe(family,index):
  sock=socket.socket(family,socket.SOCK_DGRAM);sock.settimeout(6)
  ident=20000+index;query=struct.pack('!HHHHHH',ident,256,1,0,0,0)+b'\x07example\x03com\x00'+struct.pack('!HH',1,1)
  try:
   sock.sendto(query,('10.77.0.1' if family==socket.AF_INET else 'fd77::1',53));answer,_=sock.recvfrom(1500);concurrent.append({'family':4 if family==socket.AF_INET else 6,'reply':len(answer)>=12 and struct.unpack('!H',answer[:2])[0]==ident})
  except OSError:concurrent.append({'family':4 if family==socket.AF_INET else 6,'reply':False})
  finally:sock.close()
 workers=[threading.Thread(target=dns_probe,args=(socket.AF_INET if i%2==0 else socket.AF_INET6,i)) for i in range(12)]
 for worker in workers:worker.start()
 for worker in workers:worker.join(timeout=8)
 proof['concurrent_dns']=concurrent
 proof['checks']['concurrent_ipv4_ipv6_dns_replies']=len(concurrent)==12 and all(item['reply'] for item in concurrent)
 if not proof['checks']['concurrent_ipv4_ipv6_dns_replies']:raise RuntimeError('concurrent IPv4/IPv6 virtual DNS reply failed')
 native_before=api(client,'/v1/status')
 p=lab.run([sys.executable,'-c','import socket,sys\ntry:socket.getaddrinfo(sys.argv[1],443)\nexcept socket.gaierror:pass','meshway-'+uuid.uuid4().hex+'.example.com'],timeout=20,check=False)
 native_after=api(client,'/v1/status')
 proof['checks']['native_resolver_completed']=p.returncode==0
 proof['checks']['native_resolver_encrypted_traffic']=native_after['bytes_sent']>native_before['bytes_sent'] and native_after['bytes_received']>native_before['bytes_received']
 proof['status']='PASS'
except Exception as e:proof['error']=str(e)
finally:
 if helper:
  try:
   lines=pathlib.Path(STATE/'helper.private.log').read_text().splitlines()
   proof['packet_diagnostics']=[json.loads(l) for l in lines if l.startswith('{"meshway_packet_diagnostic":') or ('meshway_packet_diagnostic' in l and l.startswith('{'))][:8]
  except (OSError,ValueError):pass
  stop=lab.run(['/usr/bin/sudo','-n',BINARY,'vpn','stop','--config',cp.resolve()],timeout=105,check=False)
  try:helper.wait(timeout=105)
  except subprocess.TimeoutExpired:proof['cleanup_error']='helper did not stop';lab.run(['/usr/bin/sudo','-n','/bin/kill','-TERM',str(helper.pid)],check=False)
  proof['checks']['helper_stopped']=helper.poll() is not None
  proof['checks']['stop_request_accepted']=stop.returncode==0
  status=lab.run(['/usr/bin/sudo','-n',BINARY,'vpn','status','--config',cp.resolve()],timeout=15,check=False)
  try:state=json.loads(status.stdout);proof['checks']['journal_inactive']=not state.get('active') and not state.get('running') and not state.get('network_cleanup_pending')
  except ValueError:proof['checks']['journal_inactive']=False
  try:state=api(client,'/v1/status');proof['checks']['packet_session_inactive']=not state.get('vpn',{}).get('active') and not state.get('vpn',{}).get('peer_id')
  except OSError:proof['checks']['packet_session_inactive']=False
 if owner and owner.poll() is None:owner.terminate();owner.wait(timeout=5)
 for process,log in processes:
  try:process.terminate()
  except PermissionError:lab.run(['/usr/bin/sudo','-n','/bin/kill','-TERM',str(process.pid)],check=False)
  try:process.wait(timeout=10)
  except subprocess.TimeoutExpired:lab.run(['/usr/bin/sudo','-n','/bin/kill','-KILL',str(process.pid)],check=False);process.wait(timeout=5)
  log.close()
 proof['checks']['baseline_restored']=lab.baseline()==before
 if not all(proof['checks'].values()):proof['status']='FAIL'
 (STATE/'result.json').write_text(json.dumps(proof,indent=2)+'\n')
 print(json.dumps(proof),flush=True)
if proof['status']!='PASS':raise SystemExit(1)
