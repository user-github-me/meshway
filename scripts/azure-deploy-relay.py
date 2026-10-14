#!/usr/bin/env python3
"""Owner-authorized persistent public encrypted relay deployment on Azure.

Uses the existing az login; never reads Azure credentials. Successful resources
stay running and incur charges. Only a newly created dedicated resource group is
rolled back on failure. No development-host networking settings are changed.
"""
import argparse
import datetime
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import signal
import socket
import socketserver
import threading
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
PRIVATE = ROOT / ".tools/deploy/live"
PURPOSE = "owner-managed-public-relay"


def run(arguments, timeout=900):
    result = subprocess.run(arguments, capture_output=True, text=True, timeout=timeout,
                            env={**os.environ, "PYTHONWARNINGS": "ignore"})
    if result.returncode:
        # Arguments and remote commands contain no secret values.
        raise RuntimeError(result.stderr.strip()[-2000:] or "Command failed")
    return result.stdout.strip()


def az(*arguments):
    return run(["az", *arguments])


def write_json(path, value, private=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix="." + path.name + "-", dir=path.parent)
    temporary = Path(name)
    try:
        os.fchmod(fd, 0o600 if private else 0o644)
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def api(configuration, path, body=None):
    request = urllib.request.Request("http://" + configuration["api_listen"] + "/v1/" + path,
              data=None if body is None else json.dumps(body).encode(),
              headers={"Authorization": "Bearer " + configuration["api_token"], "Content-Type":"application/json"})
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=5) as response:
        return json.load(response)


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def validate_local(harness, binary, invitation, expected_id):
    # Only ephemeral loopback listeners and a real encrypted outgoing peer socket.
    with tempfile.TemporaryDirectory(prefix="verify-", dir=PRIVATE) as temporary:
        directory = Path(temporary)
        config_path = directory / "config.json"
        run([str(binary), "init", "--config", str(config_path)])
        configuration = json.loads(config_path.read_text())
        configuration.update(listen_addresses=["/ip4/127.0.0.1/tcp/0"], socks_listen="",
                             api_listen="127.0.0.1:" + str(free_port()), bootstrap_peers=[invitation])
        write_json(config_path, configuration, private=True)
        log_path = directory / "lab.log"
        with log_path.open("w+") as log:
            process = subprocess.Popen([str(harness), "--config", str(config_path),
                       "--reserve-relay", invitation], stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 75
                connected = reserved = False
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError("Isolated relay verification client stopped")
                    try:
                        status = api(configuration, "status")
                        connected = any(peer["id"] == expected_id for peer in status["peers"])
                    except (OSError, ValueError):
                        pass
                    observations = [json.loads(line) for line in log_path.read_text().splitlines()
                                    if line.startswith("{")]
                    reserved = any(item.get("event") == "reservation" and item.get("success")
                                   for item in observations)
                    if connected and reserved:
                        return {"encrypted_peer_connection": "PASS", "circuit_v2_reservation": "PASS"}
                    time.sleep(.5)
                raise RuntimeError("Public relay connection/reservation did not pass")
            finally:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()



def validate_circuit(harness, binary, invitation):
    """Two isolated local peers, real public relay, owned loopback TCP fixture."""
    class Echo(socketserver.BaseRequestHandler):
        def handle(self):
            self.request.settimeout(15)
            while True:
                block = self.request.recv(65536)
                if not block:
                    return
                self.request.sendall(block)
    class EchoServer(socketserver.ThreadingTCPServer):
        daemon_threads = True
        allow_reuse_address = False
    with EchoServer(("127.0.0.1", 0), Echo) as echo:
        thread = threading.Thread(target=echo.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory(prefix="circuit-", dir=PRIVATE) as temporary:
                directory = Path(temporary)
                configs = {}
                paths = {}
                identities = {}
                processes = []
                logs = []
                for name in ["client", "server"]:
                    paths[name] = directory / name / "config.json"
                    run([str(binary), "init", "--config", str(paths[name])])
                    configs[name] = json.loads(paths[name].read_text())
                    configs[name].update(listen_addresses=["/ip4/127.0.0.1/tcp/0"], socks_listen="",
                       api_listen="127.0.0.1:" + str(free_port()), bootstrap_peers=[invitation])
                    identities[name] = run([str(binary), "identity", "--config", str(paths[name])])
                forward_port = free_port()
                configs["server"].update(allowed_peers=[identities["client"]],
                    services=[{"name":"echo","target":"127.0.0.1:" + str(echo.server_address[1])}])
                configs["client"]["forwards"] = [{"name":"echo","listen":"127.0.0.1:" + str(forward_port),
                    "service":"echo","peer":identities["server"]}]
                try:
                    for name in ["server", "client"]:
                        write_json(paths[name], configs[name], private=True)
                        log = (directory / (name + ".log")).open("w+")
                        logs.append(log)
                        processes.append(subprocess.Popen([str(harness), "--config", str(paths[name]),
                            "--reserve-relay", invitation], stdout=log, stderr=subprocess.STDOUT))
                    deadline = time.monotonic() + 60
                    for name in ["server", "client"]:
                        while time.monotonic() < deadline:
                            observations = [json.loads(line) for line in (directory / (name + ".log")).read_text().splitlines()
                                            if line.startswith("{")]
                            if any(item.get("event") == "reservation" and item.get("success") for item in observations):
                                break
                            if any(process.poll() is not None for process in processes):
                                raise RuntimeError("Circuit validation peer stopped")
                            time.sleep(.3)
                        else:
                            raise RuntimeError("Circuit fixture reservation failed")
                    circuit = invitation + "/p2p-circuit/p2p/" + identities["server"]
                    api(configs["client"], "connect", {"address": circuit})
                    payload = os.urandom(32768)
                    with socket.create_connection(("127.0.0.1", forward_port), timeout=15) as connection:
                        connection.sendall(payload)
                        received = bytearray()
                        while len(received) < len(payload):
                            block = connection.recv(len(payload) - len(received))
                            if not block:
                                raise RuntimeError("Relayed fixture ended before full echo")
                            received.extend(block)
                    if bytes(received) != payload:
                        raise RuntimeError("Relayed payload integrity failed")
                    for attempt in range(20):
                        observations = [json.loads(line) for line in (directory / "client.log").read_text().splitlines()
                                        if line.startswith("{")]
                        limited = any(connection.get("peer") == identities["server"] and connection.get("limited")
                            for item in observations for connection in item.get("connections", []))
                        if limited:
                            return {"result":"PASS","payload_bytes":len(payload),"actual_circuit_limited":True,
                                    "topology":"loopback client -> public Azure encrypted circuit relay -> separate loopback peer -> owned TCP echo"}
                        time.sleep(.2)
                    raise RuntimeError("Payload succeeded without an observed circuit-limited peer connection")
                finally:
                    for process in reversed(processes):
                        if process.poll() is None:
                            process.terminate()
                            try:
                                process.wait(timeout=10)
                            except subprocess.TimeoutExpired:
                                process.kill()
                                process.wait()
                    for log in logs:
                        log.close()
        finally:
            echo.shutdown()
            thread.join(timeout=5)


REMOTE_INSTALL = r"""set -eu
actual=$(sha256sum /home/meshwayadmin/meshway-upload | cut -d ' ' -f 1)
[ "$actual" = __SHA__ ]
sudo install -o root -g root -m 0755 /home/meshwayadmin/meshway-upload /usr/local/bin/.meshway-next
sudo mv -f /usr/local/bin/.meshway-next /usr/local/bin/meshway
rm /home/meshwayadmin/meshway-upload
if ! id meshway >/dev/null 2>&1; then sudo useradd --system --home-dir /var/lib/meshway --shell /usr/sbin/nologin meshway; fi
sudo install -d -o meshway -g meshway -m 0700 /var/lib/meshway
if ! sudo test -f /var/lib/meshway/config.json; then
  sudo -u meshway /usr/local/bin/meshway init --config /var/lib/meshway/config.json >/dev/null
fi
sudo python3 - <<'CONFIG'
import json,os
p='/var/lib/meshway/config.json'
c=json.load(open(p))
c.update(state_dir='/var/lib/meshway/state',listen_addresses=['/ip4/0.0.0.0/tcp/4001','/ip4/0.0.0.0/udp/4001/quic-v1'],
         bootstrap_peers=[],upstream_peer='',socks_listen='',api_listen='127.0.0.1:7777',
         relay=True,exit=False,allow_private_targets=False,allowed_peers=[],services=[],forwards=[],
         vpn=None,account_sync=False,personal_server=False,advertise=True,auto_discovery=False,
         server_label='Meshway public encrypted relay',server_country='HK',device_label='Public relay')
with open(p,'w') as f:json.dump(c,f,indent=2)
os.chmod(p,0o600)
CONFIG
sudo chown meshway:meshway /var/lib/meshway/config.json
sudo tee /etc/systemd/system/meshway-relay.service >/dev/null <<'SERVICE'
[Unit]
Description=Meshway public encrypted circuit relay (no internet exit)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5
[Service]
Type=simple
User=meshway
Group=meshway
ExecStart=/usr/local/bin/meshway daemon --config /var/lib/meshway/config.json
Restart=on-failure
RestartSec=5
TimeoutStopSec=20
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/meshway
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictRealtime=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
MemoryMax=768M
TasksMax=256
LimitNOFILE=4096
[Install]
WantedBy=multi-user.target
SERVICE
sudo systemctl daemon-reload
sudo systemctl enable meshway-relay.service >/dev/null
sudo systemctl restart meshway-relay.service
sudo systemctl is-active --quiet meshway-relay.service
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--deploy", action="store_true", help="Authorized create/deploy; successful VM remains billable")
    parser.add_argument("--verify-existing", action="store_true", help="Verify and restart the previously deployed owned relay")
    parser.add_argument("--update-existing", action="store_true", help="Replace owned relay binary, verify, restore prior binary on failure")
    parser.add_argument("--region", default="eastasia")
    parser.add_argument("--linux-binary", type=Path, default=ROOT / "dist/bin/meshway-linux-amd64")
    parser.add_argument("--local-binary", type=Path, default=ROOT / ".tools/deploy-relay-cli")
    parser.add_argument("--harness", type=Path, default=ROOT / ".tools/deploy-relay-net-lab")
    parser.add_argument("--output", type=Path, default=ROOT / "artifacts/public-relay.json")
    args = parser.parse_args()
    if sum([args.deploy, args.verify_existing, args.update_existing]) != 1:
        parser.error("Choose --deploy, --verify-existing or --update-existing")
    for path in [args.linux_binary, args.local_binary, args.harness]:
        if not path.is_file():
            parser.error("Missing executable " + str(path))
    PRIVATE.mkdir(parents=True, exist_ok=True)
    os.chmod(PRIVATE, 0o700)
    metadata_path = PRIVATE / "deployment.json"
    key = PRIVATE / "ssh"
    if args.deploy and metadata_path.exists():
        parser.error("An owned relay already exists; use --verify-existing, or explicitly decommission it before another deploy")
    created = False
    updated = False
    completed = False
    group = None
    try:
        if args.deploy:
            group = "meshway-public-relay-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d-%H%M%S")
            with urllib.request.urlopen("https://api.ipify.org", timeout=15) as response:
                owner_ip = ipaddress.ip_address(response.read(128).decode().strip())
            source = str(owner_ip) + ("/32" if owner_ip.version == 4 else "/128")
            if not key.exists():
                run(["ssh-keygen", "-t", "ed25519", "-f", str(key), "-N", "", "-q"])
            os.chmod(key, 0o600)
            cloud_init = PRIVATE / "cloud-init.yaml"
            cloud_init.write_text("#cloud-config\nwrite_files:\n  - path: /etc/ssh/sshd_config.d/05-meshway.conf\n    permissions: '0600'\n    content: |\n      Port 443\n      PasswordAuthentication no\n      PermitRootLogin no\nruncmd:\n  - [systemctl, restart, ssh]\n")
            os.chmod(cloud_init, 0o600)
            print("Creating one persistent Standard_B1s public encrypted relay; no internet exit.", flush=True)
            created = True
            az("group", "create", "--name", group, "--location", args.region, "--tags",
               "project=meshway", "purpose=" + PURPOSE, "lifecycle=persistent", "-o", "none")
            az("vm", "create", "-g", group, "-n", "relay", "--location", args.region,
               "--image", "Ubuntu2204", "--size", "Standard_B1s", "--admin-username", "meshwayadmin",
               "--ssh-key-values", str(key) + ".pub", "--nsg-rule", "NONE", "--public-ip-sku", "Standard",
               "--storage-sku", "Standard_LRS", "--os-disk-size-gb", "30", "--security-type", "TrustedLaunch",
               "--custom-data", str(cloud_init), "--tags", "project=meshway", "purpose=" + PURPOSE,
               "lifecycle=persistent", "-o", "none")
            az("network", "nsg", "rule", "create", "-g", group, "--nsg-name", "relayNSG", "-n", "OwnerSSH",
               "--priority", "100", "--protocol", "Tcp", "--source-address-prefixes", source,
               "--destination-port-ranges", "443", "-o", "none")
            for protocol, priority in [("Tcp", "110"), ("Udp", "120")]:
                az("network", "nsg", "rule", "create", "-g", group, "--nsg-name", "relayNSG", "-n", "Peer" + protocol,
                   "--priority", priority, "--protocol", protocol, "--source-address-prefixes", "*",
                   "--destination-port-ranges", "4001", "-o", "none")
            public_ip = az("vm", "show", "-d", "-g", group, "-n", "relay", "--query", "publicIps", "-o", "tsv")
            ipaddress.IPv4Address(public_ip)
            metadata = {"resource_group": group, "region": args.region, "public_ip": public_ip,
                        "ssh_source": source, "purpose": PURPOSE, "provisioning_complete": False}
            write_json(metadata_path, metadata, private=True)
        else:
            metadata = json.loads(metadata_path.read_text())
            group = metadata["resource_group"]
            public_ip = metadata["public_ip"]
            if not re.fullmatch(r"meshway-public-relay-[0-9]{8}-[0-9]{6}", group):
                raise RuntimeError("Unexpected resource group; refusing external mutation")
            purpose = az("group", "show", "-n", group, "--query", "tags.purpose", "-o", "tsv")
            if purpose != PURPOSE:
                raise RuntimeError("Resource group is not the owned persistent relay")
        ssh_options = ["-F", "/dev/null", "-i", str(key), "-o", "Port=443", "-o", "BatchMode=yes",
                       "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + str(PRIVATE / "known_hosts"),
                       "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3"]
        def ssh(command):
            return run(["ssh", *ssh_options, "meshwayadmin@" + public_ip, command], timeout=120)
        print("Waiting for restricted SSH and installing the verified unprivileged relay service.", flush=True)
        for attempt in range(45):
            try:
                ssh("true")
                break
            except (RuntimeError, subprocess.TimeoutExpired):
                if attempt == 44:
                    raise RuntimeError("Restricted SSH unavailable") from None
                time.sleep(2)
        prior_peer_id = None
        if args.update_existing:
            prior_status = json.loads(ssh("sudo -u meshway /usr/local/bin/meshway status --config /var/lib/meshway/config.json"))
            prior_peer_id = prior_status["peer_id"]
        binary_sha = hashlib.sha256(args.linux_binary.read_bytes()).hexdigest()
        if args.verify_existing:
            binary_sha = ssh("sha256sum /usr/local/bin/meshway | cut -d ' ' -f 1")
            if not re.fullmatch(r"[0-9a-f]{64}", binary_sha):
                raise RuntimeError("Remote installed binary checksum unavailable")
        if args.deploy or args.update_existing:
            run(["scp", *ssh_options, str(args.linux_binary.resolve()), "meshwayadmin@" + public_ip + ":meshway-upload"], timeout=120)
            if args.update_existing:
                ssh("sudo test ! -e /usr/local/bin/.meshway-previous && sudo cp -p /usr/local/bin/meshway /usr/local/bin/.meshway-previous")
                updated = True
            ssh(REMOTE_INSTALL.replace("__SHA__", shlex.quote(binary_sha)))
            installed_sha = ssh("sha256sum /usr/local/bin/meshway | cut -d ' ' -f 1")
            if installed_sha != binary_sha:
                raise RuntimeError("Installed binary integrity validation failed")
        for attempt in range(30):
            try:
                remote_status = json.loads(ssh("sudo -u meshway /usr/local/bin/meshway status --config /var/lib/meshway/config.json"))
                break
            except (RuntimeError, ValueError):
                if attempt == 29:
                    raise
                time.sleep(.3)
        peer_id = remote_status["peer_id"]
        if prior_peer_id is not None and peer_id != prior_peer_id:
            raise RuntimeError("Relay identity changed across binary replacement")
        if not re.fullmatch(r"[A-Za-z0-9]{40,100}", peer_id) or remote_status["roles"] != {"relay": True, "exit": False}:
            raise RuntimeError("Remote identity or role validation failed")
        if remote_status.get("api_listen") != "127.0.0.1:7777" or remote_status.get("socks_listen") != "":
            raise RuntimeError("Remote API/SOCKS exposure validation failed")
        invitation = "/ip4/" + public_ip + "/tcp/4001/p2p/" + peer_id
        print("Validating a real encrypted public connection and circuit-v2 reservation.", flush=True)
        tests = validate_local(args.harness.resolve(), args.local_binary.resolve(), invitation, peer_id)
        quic_invitation = "/ip4/" + public_ip + "/udp/4001/quic-v1/p2p/" + peer_id
        tests["quic"] = validate_local(args.harness.resolve(), args.local_binary.resolve(), quic_invitation, peer_id)
        ssh("sudo systemctl restart meshway-relay.service; sudo systemctl is-active --quiet meshway-relay.service")
        for attempt in range(30):
            try:
                restarted = json.loads(ssh("sudo -u meshway /usr/local/bin/meshway status --config /var/lib/meshway/config.json"))
                break
            except (RuntimeError, ValueError):
                if attempt == 29:
                    raise
                time.sleep(.3)
        if restarted["peer_id"] != peer_id:
            raise RuntimeError("Relay identity changed across service restart")
        tests["restart_identity_persisted"] = "PASS"
        tests["post_restart"] = validate_local(args.harness.resolve(), args.local_binary.resolve(), invitation, peer_id)
        print("Validating actual encrypted payload through the public circuit relay.", flush=True)
        tests["relayed_payload"] = validate_circuit(args.harness.resolve(), args.local_binary.resolve(), invitation)
        exposed = json.loads(az("network", "nsg", "rule", "list", "-g", group, "--nsg-name", "relayNSG", "-o", "json"))
        if {rule["destinationPortRange"] for rule in exposed} != {"443", "4001"} or len(exposed) != 3:
            raise RuntimeError("Unexpected public inbound rules")
        for rule in exposed:
            expected_source = metadata["ssh_source"] if rule["name"] == "OwnerSSH" else "*"
            if rule["sourceAddressPrefix"] != expected_source or rule["access"] != "Allow" or rule["direction"] != "Inbound":
                raise RuntimeError("Unexpected public inbound scope")
        tests["public_api_and_socks_disabled"] = "PASS"
        result = {"status": "PASS", "time": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  "resource_group": group, "region": metadata["region"], "size": "Standard_B1s",
                  "public_ip": public_ip, "peer_id": peer_id, "bootstrap_peers": [invitation,
                  "/ip4/" + public_ip + "/udp/4001/quic-v1/p2p/" + peer_id],
                  "roles": {"relay": True, "exit": False}, "persistent": True,
                  "binary_sha256": binary_sha, "host_network_modified": False, "tests": tests,
                  "limits": {"max_reservations":128,"max_circuits":16,"circuit_seconds":120,"circuit_bytes":67108864},
                  "cost": {"currency":"USD","approximate_monthly_range":[15,25],"note":"Compute, disk and public IPv4 incur ongoing charges; outgoing bandwidth is additional. Verify actual Azure billing/pricing."}}
        write_json(args.output, result)
        write_json(ROOT / "config/public-bootstrap.json", {"bootstrap_peers": result["bootstrap_peers"],
                   "roles": result["roles"], "label":"Meshway public encrypted relay", "country":"HK"})
        metadata["provisioning_complete"] = True
        write_json(metadata_path, metadata, private=True)
        completed = True
        if updated:
            ssh("sudo rm -f /usr/local/bin/.meshway-previous")
        print(json.dumps(result, indent=2))
    finally:
        if updated and not completed:
            print("Update failed; restoring the prior owned relay binary and service.", flush=True)
            ssh("sudo mv -f /usr/local/bin/.meshway-previous /usr/local/bin/meshway; sudo systemctl restart meshway-relay.service; sudo systemctl is-active --quiet meshway-relay.service")
        if created and not completed:
            print("Deployment failed; deleting only the new dedicated relay resource group.", flush=True)
            az("group", "delete", "--name", group, "--yes")
            if az("group", "exists", "--name", group, "-o", "tsv").lower() != "false":
                raise RuntimeError("Failed deployment cleanup was not confirmed")
            metadata_path.unlink(missing_ok=True)


if __name__ == "__main__":
    def terminate(signum, frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, terminate)
    main()
