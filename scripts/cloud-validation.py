#!/usr/bin/env python3
"""Opt-in real external-network validation on two temporary Azure Linux VMs.

Requires the owner's explicit authorization to provision temporary resources.
Only Azure CLI session authentication is used; no tokens or Azure keys are read.
No development-host routes, DNS, proxies or active VPNs are changed.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import shlex
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
import urllib.error

sys.path.insert(0, str(Path(__file__).resolve().parent))
from importlib.util import spec_from_file_location, module_from_spec
spec = spec_from_file_location("internet_smoke", Path(__file__).with_name("internet-smoke.py"))
smoke = module_from_spec(spec)
spec.loader.exec_module(smoke)


def execute(arguments, **kwargs):
    result = subprocess.run(arguments, capture_output=True, text=True, **kwargs)
    if result.returncode:
        # Commands never contain credential values. CLI errors may contain subscription IDs.
        raise RuntimeError(result.stderr.strip()[-3000:])
    return result.stdout.strip()


def azure(*args):
    return execute(["az", *args], env={**os.environ, "PYTHONWARNINGS": "ignore"})


def api(configuration, path, body=None):
    try: return smoke.api(configuration, path, body)
    except urllib.error.HTTPError as error:
        message = error.read(4096).decode('utf8', errors='replace')
        raise RuntimeError(f'Local control {path} returned {error.code}: {message}') from None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--provision", action="store_true", help="Explicitly authorize temporary VM provisioning for this invocation")
    parser.add_argument("--resource-group", help="Adopt a previously explicitly created temporary validation group")
    parser.add_argument("--ssh-key", type=Path, help="Explicit SSH private key for an adopted validation group")
    parser.add_argument("--region", default="eastasia")
    parser.add_argument("--ssh-port", type=int, default=443, help="Temporary VM SSH port; 443 supports networks filtering SSH port 22")
    parser.add_argument("--binary", type=Path, default=Path("dist/meshway"))
    parser.add_argument("--linux-binary", type=Path, default=Path("dist/bin/meshway-linux-amd64"))
    parser.add_argument("--harness", type=Path, default=Path(".tools/cloud/meshway-net-lab-local"))
    parser.add_argument("--linux-harness", type=Path, default=Path(".tools/cloud/meshway-net-lab"))
    parser.add_argument("--output", type=Path, default=Path("artifacts/cloud-validation.json"))
    args = parser.parse_args()
    if not (1 <= args.ssh_port <= 65535): parser.error('Invalid SSH port')
    if not args.provision and not args.resource_group:
        parser.error("Use --provision only after explicit owner authorization, or adopt a known temporary group")
    if args.resource_group and not args.resource_group.startswith("meshway-validation-"):
        parser.error("Cleanup is restricted to explicitly named Meshway temporary validation groups")
    for path in [args.binary, args.linux_binary, args.harness, args.linux_harness]:
        if not path.is_file(): parser.error(f"Missing executable {path}")
    group = args.resource_group or "meshway-validation-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d-%H%M%S")
    created = False
    processes = []
    results = {"time": datetime.datetime.now(datetime.timezone.utc).isoformat(), "region": args.region,
               "topology": "macOS client on owner network -> two Azure Linux VMs, separate VNets, public authenticated libp2p direct and circuit-v2",
               "host_network_modified": False, "tests": {}, "resource_group": group}
    results["binary_sha256"] = hashlib.sha256(args.linux_binary.read_bytes()).hexdigest()
    workspace_base = Path(".tools/cloud")
    workspace_base.mkdir(parents=True, exist_ok=True)
    os.chmod(workspace_base, 0o700)
    try:
        with tempfile.TemporaryDirectory(prefix="run-", dir=workspace_base) as temporary:
            directory = Path(temporary).resolve()
            key = args.ssh_key.resolve() if args.ssh_key else directory / "ssh"
            if not args.ssh_key: execute(["ssh-keygen", "-t", "ed25519", "-f", str(key), "-N", "", "-q"])
            if not args.resource_group:
                # Mark ownership before creation so partial provisioning is also cleaned up.
                created = True
                azure("group", "create", "--name", group, "--location", args.region, "--tags", "project=meshway", "purpose=temporary-validation", "lifetime=one-hour", "-o", "none")
                cloud_init = directory / "cloud-init.yaml"
                cloud_init.write_text(f"#cloud-config\nruncmd:\n  - [sh, -c, '/usr/sbin/sshd -p {args.ssh_port}']\n")
                print("Provisioning two temporary B1s VMs; teardown is mandatory.", flush=True)
                def provision(name):
                    azure("vm", "create", "-g", group, "-n", name, "--location", args.region, "--image", "Ubuntu2204", "--size", "Standard_B1s", "--admin-username", "meshwaylab", "--ssh-key-values", str(key) + ".pub", "--nsg-rule", "NONE", "--public-ip-sku", "Standard", "--storage-sku", "Standard_LRS", "--os-disk-size-gb", "30", "--security-type", "TrustedLaunch", "--custom-data", str(cloud_init), "--tags", "project=meshway", "purpose=temporary-validation", "lifetime=one-hour", "-o", "none")
                with ThreadPoolExecutor(2) as pool: list(pool.map(provision, ["exit", "relay"]))
                with urllib.request.urlopen("https://api.ipify.org", timeout=15) as response: own_ip = str(ipaddress.ip_address(response.read().decode().strip()))
                for name in ["exit", "relay"]:
                    azure("network", "nsg", "rule", "create", "-g", group, "--nsg-name", name + "NSG", "-n", "MeshwaySSH", "--priority", "100", "--protocol", "Tcp", "--source-address-prefixes", own_ip + "/32", "--destination-port-ranges", str(args.ssh_port), "-o", "none")
                    azure("network", "nsg", "rule", "create", "-g", group, "--nsg-name", name + "NSG", "-n", "MeshwayPeer", "--priority", "110", "--protocol", "*", "--source-address-prefixes", "*", "--destination-port-ranges", "4001", "-o", "none")
            else:
                created = True
            machines = json.loads(azure("vm", "list", "-g", group, "--show-details", "--query", "[].{name:name,ip:publicIps}", "-o", "json"))
            ips = {vm["name"]: vm["ip"] for vm in machines}
            if set(ips) != {"exit", "relay"}: raise RuntimeError("Expected exactly two dedicated temporary VMs")
            ssh_options = ["-F", "/dev/null", "-i", str(key), "-o", "Port=" + str(args.ssh_port), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + str(directory / "known_hosts"), "-o", "ConnectTimeout=10"]
            def ssh(name, command): return execute(["ssh", *ssh_options, "meshwaylab@" + ips[name], command])
            def scp(name, source, target): return execute(["scp", *ssh_options, str(source), "meshwaylab@" + ips[name] + ":" + target])
            for name in ["exit", "relay"]:
                print("Connecting to temporary " + name + " VM and uploading verified binaries.", flush=True)
                for attempt in range(30):
                    try: ssh(name, "mkdir -p /home/meshwaylab/lab && chmod 700 /home/meshwaylab/lab"); break
                    except RuntimeError:
                        if attempt == 29: raise
                        time.sleep(2)
                scp(name, args.linux_binary.resolve(), "lab/meshway")
                scp(name, args.linux_harness.resolve(), "lab/meshway-net-lab")
                ssh(name, "chmod 700 lab/meshway lab/meshway-net-lab")
            cpath, configuration = smoke.initialize(str(args.binary.resolve()), directory, "client")
            client_id = execute([str(args.binary.resolve()), "identity", "--config", str(cpath)])
            remote_config = {}
            remote_id = {}
            for name in ["exit", "relay"]:
                ssh(name, "lab/meshway init --config /home/meshwaylab/lab/config.json >/dev/null")
                # Pull private config only into private temporary state; never emit content.
                config_path = directory / (name + ".json")
                execute(["scp", *ssh_options, "meshwaylab@" + ips[name] + ":lab/config.json", str(config_path)])
                os.chmod(config_path, 0o600)
                config = json.loads(config_path.read_text())
                config.update(listen_addresses=["/ip4/0.0.0.0/tcp/4001"], socks_listen="", api_listen="127.0.0.1:7777",
                              exit=name == "exit", relay=name == "relay", advertise=True, server_label="Temporary external " + name,
                              allowed_peers=[client_id] if name == "exit" else [])
                remote_id[name] = ssh(name, "lab/meshway identity --config lab/config.json").strip()
                remote_config[name] = config
            relay_address = f'/ip4/{ips["relay"]}/tcp/4001/p2p/{remote_id["relay"]}'
            remote_config["exit"]["bootstrap_peers"] = [relay_address]
            for name in ["relay", "exit"]:
                config_path = directory / (name + ".json")
                smoke.save(config_path, remote_config[name]); scp(name, config_path, "lab/config.json")
                arguments = " --reserve-relay " + shlex.quote(relay_address) if name == "exit" else ""
                ssh(name, "cd lab; nohup ./meshway-net-lab --config config.json" + arguments + " >lab.log 2>&1 < /dev/null & echo $! >pid")
            for attempt in range(30):
                relay_log=ssh('exit','cat lab/lab.log')
                if '"success":true' in relay_log:
                    results['tests']['public_relay_reservation']={'result':'PASS'}
                    break
                if attempt==29:raise RuntimeError('Public exit relay reservation failed: '+relay_log[-2500:])
                time.sleep(2)
            for name in ["relay", "exit"]:
                local_port = smoke.port()
                process = subprocess.Popen(["ssh", *ssh_options, "-N", "-L", f"127.0.0.1:{local_port}:127.0.0.1:7777", "meshwaylab@" + ips[name]], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                processes.append(process)
                remote_config[name]["api_listen"] = f"127.0.0.1:{local_port}"
                for attempt in range(100):
                    try: api(remote_config[name], "status"); break
                    except OSError:
                        if attempt == 99: raise
                        time.sleep(.2)
            configuration.update(upstream_peer=remote_id["exit"], bootstrap_peers=[relay_address])
            smoke.save(cpath, configuration)
            log = directory / "client.log"
            log_file = log.open("w")
            process = subprocess.Popen([str(args.harness.resolve()), "--config", str(cpath)], stdout=log_file, stderr=log_file)
            processes.append(process)
            for attempt in range(100):
                try: api(configuration, "status"); break
                except OSError:
                    if attempt == 99: raise
                    time.sleep(.1)
            def https(name):
                result = execute(["curl", "-fsS", "--max-time", "40", "--socks5-hostname", configuration["socks_listen"], "--noproxy", "", "https://example.com/"])
                if "Example Domain" not in result: raise RuntimeError("Unexpected HTTPS response")
                results["tests"][name] = {"result": "PASS", "response_bytes": len(result.encode())}
            direct_address = f'/ip4/{ips["exit"]}/tcp/4001/p2p/{remote_id["exit"]}'
            api(configuration, "connect", {"address": direct_address})
            https("direct_external_https")
            print("PASS: actual external direct peer HTTPS.", flush=True)
            # Persist sanitized evidence before later stages/cleanup for diagnosis.
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(results, indent=2) + "\n")
            # Drop only port 4001 on the temporary exit VM. Its outbound public relay remains allowed.
            # Restrict NEW inbound initiation while preserving the existing
            # reservation transport, which can itself be inbound on port 4001.
            ssh("exit", "sudo iptables -I INPUT 1 -p tcp --dport 4001 -m conntrack --ctstate NEW -j DROP; sudo iptables -I INPUT 1 -p udp --dport 4001 -m conntrack --ctstate NEW -j DROP")
            api(configuration, "shutdown", {})
            process.wait(timeout=15)
            process = subprocess.Popen([str(args.harness.resolve()), "--config", str(cpath)], stdout=log_file, stderr=log_file)
            processes.append(process)
            for attempt in range(100):
                try: api(configuration, "status"); break
                except OSError:
                    if attempt == 99: raise
                    time.sleep(.1)
            circuit = relay_address + "/p2p-circuit/p2p/" + remote_id["exit"]
            for attempt in range(30):
                try: api(configuration, "connect", {"address": circuit}); break
                except (OSError, RuntimeError):
                    if attempt == 29: raise
                    time.sleep(2)
            https("forced_external_circuit_https")
            print("PASS: actual external forced circuit peer HTTPS.", flush=True)
            time.sleep(1.2)
            log_file.flush()
            observations = [json.loads(line) for line in log.read_text().splitlines() if line.startswith('{')]
            results['transport_observations'] = observations
            limited = any(connection.get("limited") and connection.get("peer") == remote_id["exit"] for observation in observations for connection in observation.get("connections", []))
            if not limited: raise RuntimeError("Expected authenticated connection explicitly marked circuit-limited")
            results["tests"]["actual_circuit_transport"] = {"result":"PASS", "limited":True}
            args.output.write_text(json.dumps(results, indent=2) + "\n")
            # Loss is confined to the disposable relay VM's interface.
            relay_interface = ssh("relay", "ip -j route show default")
            relay_interface = json.loads(relay_interface)[0]['dev']
            if not relay_interface.replace('-','').replace('_','').isalnum():raise RuntimeError('Unexpected relay interface name')
            ssh("relay", "sudo modprobe sch_netem; sudo tc qdisc replace dev " + relay_interface + " root netem delay 80ms 20ms loss 3%")
            try:
                https("relay_delay_packet_loss_https")
                results["tests"]["relay_delay_packet_loss_https"].update(delay_ms=80, jitter_ms=20, loss_percent=3)
            finally:
                ssh("relay", "sudo tc qdisc del dev " + relay_interface + " root")
            time.sleep(1)
            log_file.flush()
            observations = [json.loads(line) for line in log.read_text().splitlines() if line.startswith('{')]
            limited = any(connection.get("limited") and connection.get("peer") == remote_id["exit"] for observation in observations for connection in observation.get("connections", []))
            if not limited: raise RuntimeError("Expected authenticated connection explicitly marked circuit-limited")
            results["tests"]["actual_circuit_transport"] = {"result":"PASS", "limited":True}
            # Relay failure is a real separate-host process outage and must be observable.
            ssh("relay", "kill $(cat lab/pid)")
            disconnected = False
            for attempt in range(100):
                if not api(configuration, "status")["upstream_connected"]: disconnected = True; break
                time.sleep(.2)
            if not disconnected: raise RuntimeError("Relay outage was not reflected in client state")
            results["tests"]["relay_outage_detected"] = {"result":"PASS"}
            ssh("relay", "cd lab; nohup ./meshway-net-lab --config config.json >lab.log 2>&1 < /dev/null & echo $! >pid")
            for attempt in range(40):
                try:
                    api(configuration, "connect", {"address": circuit}); https("relay_restart_https"); break
                except (OSError, RuntimeError):
                    if attempt == 39: raise
                    time.sleep(2)
            results["tests"]["api_exposed_publicly"] = False
            results["status"] = "PASS"
            log_file.close()
    except Exception as error:
        if 'ssh' in locals():
            for name in ['exit','relay']:
                try:
                    remote_log=ssh(name,'tail -20 lab/lab.log')
                    results[name+'_observations']=[json.loads(line) for line in remote_log.splitlines() if line.startswith('{')]
                except Exception: pass
        results["status"] = "FAIL"
        results["error"] = str(error)
        raise
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
                try: process.wait(timeout=10)
                except subprocess.TimeoutExpired: process.kill(); process.wait()
        if created:
            print("Deleting temporary Azure resource group.", flush=True)
            try:
                azure("group", "delete", "--name", group, "--yes")
                exists = azure("group", "exists", "--name", group, "-o", "tsv")
                results["cleanup"] = "confirmed deleted" if exists.lower() == "false" else "DELETE NOT CONFIRMED"
            except Exception as error:
                results["cleanup"] = "FAILED: " + str(error)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(results, indent=2) + "\n")
        print(json.dumps(results, indent=2))


if __name__ == "__main__": main()
