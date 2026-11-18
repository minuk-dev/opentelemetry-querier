"""Build and start a distribution from installed modules outside the workspace."""

import json
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen


class Upstream(BaseHTTPRequestHandler):
    def do_GET(self):
        self.do_POST()

    def do_POST(self):
        payload = b'{"status":"success","data":{"resultType":"vector","result":[]}}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(payload)


def build(manifest, error=None):
    Path("builder.yaml").write_text(manifest)
    result = subprocess.run([builder, "--build", "--config", "builder.yaml"],
                            capture_output=True, text=True, timeout=180)
    if error is None:
        assert result.returncode == 0, result.stderr
    else:
        assert result.returncode != 0 and error in result.stderr, result.stderr


builder, example_path, plugin = sys.argv[1:]
example = Path(example_path)
manifest = (example / "builder.yaml").read_text() + f"\nprocessors:\n  - gomod: {plugin}\n"
build(manifest.replace("core_version: v0.1.0", "core_version: latest"), "canonical version")
build(manifest + f"  - gomod: {plugin}\n", "duplicate component or import alias")
build(manifest.replace(plugin, plugin.split("@")[0] + "@v0.1.99"), "go mod download")
build(manifest.replace(plugin, "github.com/go-viper/mapstructure/v2@v2.5.0"), "go build")

out = Path("dist")
(out / "go.mod").write_text("module example.com/user-owned\n")
build(manifest, "go.mod belongs to a different distribution")
assert (out / "go.mod").read_text() == "module example.com/user-owned\n"
(out / "go.mod").unlink()

build(manifest)
expected = {"go.mod", "go.sum", "main.go", "components.go", "custom-querier"}
assert {p.name for p in out.iterdir()} == expected
assert "module example.com/acme/custom-querier" in (out / "go.mod").read_text()
assert "customprocessor.NewFactory()" in (out / "components.go").read_text()
assert "lokiacceptor" not in (out / "components.go").read_text()
modules = subprocess.check_output(["go", "list", "-m", "all"], cwd=out, text=True)
assert plugin.replace("@", " ") in modules

(out / "keep.txt").write_text("keep")
build(manifest)
assert (out / "keep.txt").read_text() == "keep"
build(manifest.replace(plugin, plugin.split("@")[0] + "@v0.1.99"), "go mod download")
assert (out / "custom-querier").is_file()

with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    port = listener.getsockname()[1]

with ThreadingHTTPServer(("127.0.0.1", 0), Upstream) as upstream:
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    config = (example / "config.yaml").read_text().replace("9095", str(port))
    config = config.replace("9090", str(upstream.server_port))
    config = config.replace("      dispatchers:", "      processors: [custom]\n      dispatchers:")
    config = config.replace("dispatchers:", "processors:\n  custom: {}\n\ndispatchers:", 1)
    Path("config.yaml").write_text(config)
    command = [str(out / "custom-querier"), "--config", "config.yaml"]
    with subprocess.Popen(command) as service:
        try:
            url = f"http://127.0.0.1:{port}/api/v1/query?query=up"
            for _ in range(60):
                assert service.poll() is None, "distribution exited"
                try:
                    with urlopen(url, timeout=1) as response:
                        assert json.load(response)["status"] == "success"
                    break
                except ConnectionError:
                    time.sleep(0.1)
                except URLError as err:
                    if not isinstance(err.reason, ConnectionRefusedError):
                        raise
                    time.sleep(0.1)
            else:
                raise AssertionError("distribution did not start")
        finally:
            service.terminate()
            service.wait(timeout=10)

    Path("config.yaml").write_text(config.replace("[custom]", "[loki]"))
    result = subprocess.run(command, capture_output=True, text=True, timeout=10)
    assert result.returncode != 0 and "unknown processor" in result.stderr, result.stderr
