"""Restart while a stream is in flight.

Run with the drain overlay so the ledger survives the restart:
  sudo docker compose -p cpa-ext-test -f compose.yaml -f drain.compose.yaml up -d
  python3 drain.py
"""
import base64
import json
import subprocess
import threading
import time
from urllib.error import URLError
from urllib.request import urlopen, Request

ADMIN = "http://127.0.0.1:8581"
GATEWAY = "http://127.0.0.1:8580"
KEY = "fixture-client-secret-local-only"
BASIC = base64.b64encode(b"admin:fixture-administrator-secret-local-only").decode()
CONTAINER = "cpa-ext-test-ext-1"

def admin(path, data=None, method="GET"):
    headers = {"Authorization":"Basic "+BASIC,"X-CPA-Ext-Admin":"1","Content-Type":"application/json"}
    return json.load(urlopen(Request(ADMIN+"/api/"+path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method), timeout=5))

def wait(fn, description, tries=100):
    for _ in range(tries):
        try:
            result = fn()
            if result:
                return result
        except OSError:
            pass
        time.sleep(.2)
    raise AssertionError(description)

def healthy():
    state = admin("health")
    return not state["error"] and not state["last_poll"].startswith("0001")

wait(healthy, "CPA collector unhealthy")
admin("keys/sync", {}, "POST")
admin("prices", {"protocol":"chat","model":"fixture-chat","billed_model":"fixture-chat","input":1000,"output":2000,"cached":500,"cache_write":1000,"max_input":0,"max_output":0,"verified":False}, "POST")

data = {"model":"fixture-chat","stream":True,"stream_options":{"include_usage":True},"messages":[{"role":"user","content":"slow"}]}
request = Request(GATEWAY+"/v1/chat/completions", json.dumps(data).encode(), {"Authorization":"Bearer "+KEY,"Content-Type":"application/json","X-Session-ID":"drain-session"})
started = time.monotonic()
response = urlopen(request, timeout=60)
turn_id = response.headers["X-CPA-Ext-Turn"]
assert b"alpha" in response.readline()

# Stop the container while the upstream is still eight seconds from finishing.
stop = {}
def stop_container():
    began = time.monotonic()
    subprocess.run(["sudo", "-n", "docker", "stop", "-t", "150", CONTAINER], check=True, capture_output=True)
    stop["seconds"] = time.monotonic() - began
stopper = threading.Thread(target=stop_container)
stopper.start()

# The listener closes at once: a reverse proxy sees a refused connection and fails over.
def refused():
    try:
        urlopen(Request(GATEWAY+"/v1/models", headers={"Authorization":"Bearer "+KEY}), timeout=2).read()
    except (URLError, OSError):
        return True
    return False
wait(refused, "gateway kept accepting connections while stopping", tries=25)
refused_after = time.monotonic() - started

tail = response.read()
stream_seconds = time.monotonic() - started
assert b"beta" in tail and b"[DONE]" in tail, tail
assert stream_seconds > 7, stream_seconds
assert refused_after < stream_seconds - 2, (refused_after, stream_seconds)
stopper.join()
code = subprocess.run(["sudo", "-n", "docker", "inspect", CONTAINER, "--format", "{{.State.ExitCode}}"], check=True, capture_output=True, text=True).stdout.strip()
assert code == "0", code
assert 4 < stop["seconds"] < 30, stop

# After the restart the turn is archived and its usage, emitted by CPA while
# cpa-ext was down, is collected, priced and linked.
subprocess.run(["sudo", "-n", "docker", "start", CONTAINER], check=True, capture_output=True)
wait(healthy, "collector did not recover")
def settled():
    result = admin("turns/"+turn_id)
    return result if result["turn"]["cost"] is not None else None
result = wait(settled, "usage emitted during the restart was not collected")
assert result["turn"]["audit_state"] == "complete" and result["turn"]["status"] == 200, result["turn"]
assert result["turn"]["cost"] == 28000 and result["turn"]["session"] == "drain-session", result["turn"]
assert result["body"]["output"]["text"] == "alpha beta", result["body"]["output"]
print(f"graceful restart: listener closed after {refused_after:.1f}s, in-flight stream finished at {stream_seconds:.1f}s, container exited 0 after {stop['seconds']:.1f}s, turn archived and settled after restart")
