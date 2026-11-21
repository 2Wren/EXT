import base64
import hashlib
import concurrent.futures
import json
import time
from urllib.error import HTTPError
from urllib.request import urlopen, Request

ADMIN = "http://127.0.0.1:8581"
GATEWAY = "http://127.0.0.1:8580"
KEY = "fixture-client-secret-local-only"
BASIC = base64.b64encode(b"admin:fixture-administrator-secret-local-only").decode()

def admin(path, data=None, method="GET"):
    headers = {"Authorization":"Basic "+BASIC,"X-CPA-Ext-Admin":"1","Content-Type":"application/json"}
    return json.load(urlopen(Request(ADMIN+"/api/"+path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method), timeout=5))

def wait(fn, description):
    for _ in range(50):
        try:
            result = fn()
            if result:
                return result
        except OSError:
            pass
        time.sleep(.2)
    raise AssertionError(description)

wait(lambda: bool(admin("health").get("last_poll")) and not admin("health")["error"], "CPA collector unhealthy")
admin("keys/sync",{},"POST")
assert KEY not in json.dumps(admin("keys")), "client secret leaked"
assert next(x for x in admin("keys") if x["id"]==hashlib.sha256(KEY.encode()).hexdigest())["cpa_present"]
admin("prices",{"protocol":"chat","model":"fixture-chat","billed_model":"fixture-chat","input":1000,"output":2000,"cached":500,"cache_write":1000,"max_input":1000,"max_output":20,"verified":True},"POST")
key = admin("keys",{"id":hashlib.sha256(KEY.encode()).hexdigest(),"name":"Fixture","daily":3_000_000,"total":3_000_000,"enabled":True},"PUT")
wait(lambda: next(x for x in admin("keys") if x["id"]==key["id"])["admission_capacity"]==3_000_000,"lease not ready")

def stream():
    data={"model":"fixture-chat","stream":True,"stream_options":{"include_usage":True},"max_tokens":20,"messages":[{"role":"user","content":"hello"}]}
    req=Request(GATEWAY+"/v1/chat/completions",json.dumps(data).encode(),{"Authorization":"Bearer "+KEY,"Content-Type":"application/json","X-Session-ID":"fixture-session"})
    try:
        start=time.monotonic()
        with urlopen(req,timeout=8) as response:
            first=response.readline()
            latency=time.monotonic()-start
            tail=response.read()
            assert b"alpha" in first and b"beta" in tail and latency<1.6,(latency,first,tail)
            return (response.status,response.headers["X-CPA-Ext-Turn"])
    except HTTPError as error:
        return error.code, error.read().decode()

with concurrent.futures.ThreadPoolExecutor() as pool:
    results=list(pool.map(lambda _:stream(),range(2)))
assert sorted(x[0] for x in results)==[200,429],results
turn_id=next(x[1] for x in results if x[0]==200)
def settled():
    data=admin("turns/"+turn_id)
    if data["turn"]["cost"] is None:
        return None
    return data
try:
    data=wait(settled,"usage did not settle")
except AssertionError:
    print(json.dumps({"events":admin("events"),"turn":admin("turns/"+turn_id)["turn"]}))
    raise
assert data["turn"]["cost"]==28000,data["turn"]
assert data["body"]["output"]["text"]=="alpha beta",data["body"]["output"]
assert b"hello" in base64.b64decode(data["body"]["request_raw"])
assert admin("sessions")[0]["session"]=="fixture-session"
state=admin("keys")[0]
assert state["spent"]==28000 and state["admission_capacity"]==2_972_000,state
print("official CPA v8: SSE immediate forwarding, 429 under concurrency, settlement, transcript and session passed")
