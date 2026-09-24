#!/usr/bin/env python3
"""Exercise a built Wire watch command against a bounded fake coordinator.

Usage: python3 scripts/pipeline-watch-smoke.py /absolute/path/to/wire
This covers CLI routing, file edits, request shape and SIGINT. Real worker
replacement is covered separately by TestYAMLFileReplacementThroughWorkers.
"""
import http.server
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import tempfile
import threading
import time


def main():
    binary = str(Path(sys.argv[1]).resolve(strict=True))
    requests = queue.Queue()

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_PUT(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            requests.put((self.path, body))
            encoded = json.dumps({"id": "job", "checkpoint_interval": body["interval"]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix="wire-watch-") as directory:
            path = Path(directory) / "pipeline.yaml"
            definition = '''apiVersion: wire/v1
kind: Pipeline
metadata: {name: watched}
spec:
  checkpoint: {interval: 1s}
  sources:
    - {name: input, type: http-api, config: {address: "127.0.0.1:8000", allow_insecure: true}}
  sinks:
    - {name: output, type: http-api, input: input, config: {url: "https://example.invalid"}}
'''

            def replace(contents):
                temporary = path.with_suffix(".next")
                temporary.write_text(contents)
                os.replace(temporary, path)

            replace(definition)
            process = subprocess.Popen(
                [binary, "jobs", "watch", "job", "--file", str(path),
                 "--coordinator", f"http://127.0.0.1:{server.server_port}",
                 "--poll-interval", "10ms"],
                cwd=directory, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            output, errors = queue.Queue(), queue.Queue()

            def collect(stream, destination):
                for line in stream:
                    destination.put(line)

            for stream, destination in ((process.stdout, output), (process.stderr, errors)):
                threading.Thread(target=collect, args=(stream, destination), daemon=True).start()
            assert json.loads(output.get(timeout=10))["kind"] == "unchanged"
            replace("invalid: definition")
            deadline = time.monotonic() + 10
            while "pipeline edit rejected" not in errors.get(timeout=max(0.001, deadline-time.monotonic())):
                if time.monotonic() >= deadline:
                    raise AssertionError("invalid edit was not reported")
            assert requests.empty(), "invalid edit sent a mutation"
            replace(definition.replace("interval: 1s", "interval: 2s"))
            assert requests.get(timeout=10) == ("/api/v1/jobs/job/checkpoint-interval", {"interval": "2s"})
            assert json.loads(output.get(timeout=10))["kind"] == "checkpoint-interval"
            process.send_signal(signal.SIGINT)
            assert process.wait(timeout=10) == 0, "SIGINT was treated as a command failure"
            assert requests.empty(), "watch cancellation sent an extra mutation"
            print("PASS: built CLI watch validates edits, applies interval and stops cleanly")
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait(timeout=10)
        server.shutdown()
        server.server_close()
        thread.join(timeout=10)


if __name__ == "__main__":
    main()
