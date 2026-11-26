"""Exercise an externally built Querier with the standalone example config."""

import json
import subprocess
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.error import URLError
from urllib.request import urlopen


class PrometheusStub(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/api/v1/query":
            self.send_error(404)
            return
        self.rfile.read(int(self.headers["Content-Length"]))
        payload = json.dumps({
            "status": "success",
            "data": {
                "resultType": "vector",
                "result": [{"metric": {"job": "smoke"}, "value": [1700000000, "7"]}],
            },
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


def verify_query(process):
    deadline = time.monotonic() + 10
    while process.poll() is None:
        try:
            with urlopen(
                "http://127.0.0.1:9095/api/v1/query?query=up&time=1700000000",
                timeout=2,
            ) as response:
                result = json.load(response)
            break
        except URLError as error:
            if time.monotonic() >= deadline or hasattr(error, "code"):
                raise
            time.sleep(0.1)
    else:
        raise RuntimeError(f"Querier exited with status {process.returncode}")
    expected = [{"metric": {"job": "smoke"}, "value": [1700000000, "7"]}]
    if result["status"] != "success" or result["data"]["result"] != expected:
        raise RuntimeError(f"unexpected query result: {result!r}")


if __name__ == "__main__":
    with ThreadingHTTPServer(("127.0.0.1", 0), PrometheusStub) as upstream:
        config = Path("config.yaml").read_text()
        config = config.replace("127.0.0.1:9090", f"127.0.0.1:{upstream.server_port}")
        Path("smoke-config.yaml").write_text(config)
        thread = Thread(target=upstream.serve_forever, daemon=True)
        thread.start()
        try:
            with subprocess.Popen(
                ["./dist/custom-querier", "--config", "smoke-config.yaml"]
            ) as process:
                try:
                    verify_query(process)
                finally:
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
        finally:
            upstream.shutdown()
