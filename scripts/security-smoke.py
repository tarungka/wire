#!/usr/bin/env python3
"""Provision and verify a loopback Wire cluster; see docs/secure-cluster.md."""
import argparse
import contextlib
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, help="New private directory to retain configs, keys and logs")
    options = parser.parse_args()
    binary = options.binary.resolve(strict=True)
    os.umask(0o077)
    with contextlib.ExitStack() as cleanup:
        if options.output:
            root = options.output.resolve()
            root.mkdir(mode=0o700, parents=False, exist_ok=False)
        else:
            root = Path(cleanup.enter_context(tempfile.TemporaryDirectory(prefix="wire-security-")))
        # Never inherit unrelated Wire configuration from the invoking shell.
        env = {k: v for k, v in os.environ.items() if not k.startswith("WIRE_")}

        def write(name, value):
            path = root / name
            path.write_text(value if isinstance(value, str) else json.dumps(value, indent=2)+"\n")
            return str(path)

        def certificate(name, ca, usage):
            key, csr, crt = (str(root / (name+suffix)) for suffix in (".key", ".csr", ".crt"))
            run("openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", key,
                "-out", csr, "-subj", "/CN="+name)
            extension = write(name+".ext", "basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage="+usage+"\nsubjectAltName=DNS:localhost,IP:127.0.0.1\n")
            run("openssl", "x509", "-req", "-in", csr, "-CA", str(root/(ca+".crt")),
                "-CAkey", str(root/(ca+".key")), "-set_serial", str(secrets.randbits(128)),
                "-days", "2", "-extfile", extension, "-out", crt)
            return {"cert": crt, "key": key, "ca_cert": str(root/(ca+".crt"))}

        for ca in ("http-ca", "node-ca"):
            run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2",
                "-keyout", str(root/(ca+".key")), "-out", str(root/(ca+".crt")),
                "-subj", "/CN="+ca, "-addext", "basicConstraints=critical,CA:TRUE",
                "-addext", "keyUsage=critical,keyCertSign,cRLSign")
        http_tls = certificate("http-server", "http-ca", "serverAuth")
        api_client = certificate("api-client", "http-ca", "clientAuth")
        rpc_tls = certificate("coordinator", "node-ca", "serverAuth")
        worker_tls = {name: certificate(name, "node-ca", "serverAuth,clientAuth")
                      for name in ("worker-a", "worker-b")}
        token = "wk_live_"+secrets.token_hex(16)
        key_file = write("viewer.key", token+"\n")
        auth = write("auth.json", {"users": [{"username": "viewer", "role": "viewer", "api_key": token}]})
        # Reserve every port together to avoid selecting duplicates. The small
        # handoff race is local-only; failures are surfaced rather than retried.
        reservations = []
        for _ in range(6):
            sock = socket.socket()
            sock.bind(("127.0.0.1", 0))
            reservations.append(sock)
        ports = [sock.getsockname()[1] for sock in reservations]
        addresses = ["127.0.0.1:"+str(port) for port in ports]
        api_addr, rpc_addr = addresses[:2]
        api_url = "https://"+api_addr
        coordinator = write("coordinator.json", {
            "mode": "coordinator", "listen": rpc_addr,
            "node": {"id": "coordinator", "data_dir": str(root/"coordinator-data"), "rpc_advertise_addr": rpc_addr},
            "http": {"addr": api_addr, "adv_addr": api_url, "tls": {**http_tls, "verify_client": True}},
            "node_tls": {**rpc_tls, "verify_client": True}, "auth": {"file": auth},
            "election": {"backend": "filelock", "lock_path": str(root/"leader.lock")}})
        configs = [("coordinator", coordinator)]
        for index, (name, tls) in enumerate(worker_tls.items()):
            storage = root/name
            for directory in ("store", "artifacts", "staging"):
                (storage/directory).mkdir(parents=True)
            data_addr, replica_addr = addresses[2+index*2:4+index*2]
            configs.append((name, write(name+".json", {
                "mode": "worker", "node_tls": tls,
                "worker": {"worker_id": name, "listen_addr": data_addr, "task_slots": 2,
                    "coordinator_seeds": [api_url], "epoch_path": str(storage/"epoch"),
                    "discovery_http": {"ca_cert": http_tls["ca_cert"], "client_cert": api_client["cert"],
                                       "client_key": api_client["key"], "api_key_file": key_file},
                    "peer_tls": tls, "checkpoint_replica": {"listen_addr": replica_addr,
                        "advertise_addr": replica_addr, "store_root": str(storage/"store"),
                        "artifact_root": str(storage/"artifacts"), "staging_root": str(storage/"staging"), "concurrency": 1}}})))
        processes = []

        def stop():
            for process in reversed(processes):
                if process.poll() is None:
                    process.send_signal(signal.SIGTERM)
            for process in processes:
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
        cleanup.callback(stop)
        for sock in reservations:
            sock.close()
        for name, config in configs:
            log = cleanup.enter_context((root/(name+".log")).open("w"))
            processes.append(subprocess.Popen([str(binary), "--config", config, "--metrics-enabled=false"],
                                              cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT))
        ctx = ssl.create_default_context(cafile=http_tls["ca_cert"])
        ctx.minimum_version = ssl.TLSVersion.TLSv1_3
        ctx.load_cert_chain(api_client["cert"], api_client["key"])
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ctx))

        def request(path, authenticated=True):
            req = urllib.request.Request(api_url+path)
            if authenticated:
                req.add_header("Authorization", "Bearer "+token)
            return opener.open(req, timeout=2)

        deadline = time.monotonic()+30
        while True:
            if any(process.poll() is not None for process in processes):
                raise RuntimeError("node exited; inspect logs in "+str(root))
            try:
                with request("/api/v1/cluster") as response:
                    cluster = json.load(response)
                if cluster["leader"]["ready"] and {w["id"] for w in cluster["workers"] if w["status"] == "ALIVE"} == set(worker_tls):
                    break
            except (OSError, urllib.error.URLError):
                pass
            if time.monotonic() >= deadline:
                raise RuntimeError("secure workers did not register before deadline")
            time.sleep(0.25)
        try:
            with request("/api/v1/jobs", authenticated=False):
                raise RuntimeError("anonymous API request unexpectedly succeeded")
        except urllib.error.HTTPError as error:
            if error.code != 401:
                raise
        for index, address in enumerate(addresses):
            identity = api_client if index == 0 else worker_tls["worker-a"]
            arguments = ["openssl", "s_client", "-connect", address, "-CAfile", identity["ca_cert"],
                         "-verify_ip", "127.0.0.1", "-verify_return_error", "-cert", identity["cert"],
                         "-key", identity["key"], "-brief"]
            result = run(*arguments, "-tls1_3", input="", timeout=5)
            transcript = result.stdout+result.stderr
            if "TLSv1.3" not in transcript or "Verification: OK" not in transcript:
                raise RuntimeError("TLS 1.3 verification failed on "+address)
            without_certificate = ["openssl", "s_client", "-connect", address,
                                   "-CAfile", identity["ca_cert"], "-verify_ip", "127.0.0.1",
                                   "-verify_return_error", "-tls1_3", "-brief", "-ign_eof"]
            missing = subprocess.run(without_certificate, input="", capture_output=True, text=True, timeout=5)
            if missing.returncode == 0 or "certificate required" not in missing.stderr:
                raise RuntimeError("missing client certificate was not rejected on "+address)
            rejected = subprocess.run(arguments+["-tls1_2"], input="", capture_output=True, text=True, timeout=5)
            if rejected.returncode == 0 or "alert protocol version" not in rejected.stderr:
                raise RuntimeError("TLS 1.2 was not explicitly rejected on "+address)
        write("acceptance.json", {"workers": sorted(worker_tls), "tls13_verified_listeners": addresses,
                                  "tls12_rejected": True, "missing_client_certificates_rejected": True, "anonymous_api_status": 401})
        print("PASS: two workers discovered and registered; six TLS 1.3 listeners verified; TLS 1.2 and missing client certificates rejected; API authentication enforced.")
        if options.output:
            print("Private configs, short-lived test keys, logs and acceptance.json:", root)
        stop()


if __name__ == "__main__":
    main()
