import base64
import hashlib
import gzip
import concurrent.futures
import json
import time
from urllib.error import HTTPError
import urllib.parse
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
key = admin("keys",{"id":hashlib.sha256(KEY.encode()).hexdigest(),"weekly":0,"total":0},"PUT")
assert admin("keys",{"id":key["id"],"weekly":0,"total":0,"week_reset":"0 18:00"},"PUT")["week_reset"]=="0 18:00"
assert next(x for x in admin("keys") if x["id"]==key["id"])["week_start"]

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

# Limits count recorded spend, so concurrent requests below the limit all pass.
with concurrent.futures.ThreadPoolExecutor() as pool:
    results=list(pool.map(lambda _:stream(),range(2)))
assert [x[0] for x in results]==[200,200],results
turn_id=results[0][1]
def settled():
    data=admin("turns/"+turn_id)
    if data["turn"]["cost"] is None:
        return None
    return data
try:
    data=wait(settled,"usage did not settle")
except AssertionError:
    print(json.dumps({"events":admin("events")["rows"],"turn":admin("turns/"+turn_id)["turn"]}))
    raise
assert data["turn"]["cost"]==28000,data["turn"]
assert data["body"]["output"]["text"]=="alpha beta",data["body"]["output"]
assert b"hello" in base64.b64decode(data["body"]["request_raw"])
listed=admin("sessions")
assert listed["total"]>=1 and listed["rows"][0]["session"]=="fixture-session"
assert admin("sessions?to=1")["total"]==0 and admin("sessions?size=1&page=%d"%(listed["total"]+1))["rows"]==[]
turns=admin("sessions?"+urllib.parse.urlencode({"key":listed["rows"][0]["key_id"],"session":"fixture-session","size":1}))
assert turns["total"]==listed["rows"][0]["turns"] and len(turns["rows"])==1
assert admin("overview?to=1")==[] and admin("series?to=1")==[]
assert sum(r["requests"] for r in admin("series?unit=hour"))==sum(r["requests"] for r in admin("overview"))>=1
def spent():
    state=admin("keys")[0]
    return state if state["spent"]==56000 else None
state=wait(spent,"both streams were not charged")
assert state["spent_week"]==56000 and "admission_capacity" not in state and "enabled" not in state,state
listed=admin("events?size=1")
assert listed["total"]>=1 and len(listed["rows"])==1 and listed["page"]==1 and listed["rows"][0]["http_status"]==200,listed
assert admin("events?to=1")["total"]==0 and admin("events?from=%d"%(listed["rows"][0]["happened"]))["total"]>=1

def call(model="fixture-chat"):
    data={"model":model,"max_tokens":20,"messages":[{"role":"user","content":"hello"}]}
    try:
        with urlopen(Request(GATEWAY+"/v1/chat/completions",json.dumps(data).encode(),{"Authorization":"Bearer "+KEY,"Content-Type":"application/json"}),timeout=8) as response:
            return response.status,response.read().decode()
    except HTTPError as error:
        return error.code,error.read().decode()
def models():
    with urlopen(Request(GATEWAY+"/v1/models",headers={"Authorization":"Bearer "+KEY}),timeout=8) as response:
        return [m["id"] for m in json.load(response)["data"]]
policy={"id":key["id"],"weekly":0,"total":0}
# Whichever limit is used up first refuses the key; raising it lets requests through again.
for weekly,total,word in ((56000,0,"weekly"),(0,56000,"total"),(56000,1_000_000,"weekly"),(1_000_000,56000,"total"),(1,1,"total")):
    admin("keys",{**policy,"weekly":weekly,"total":total},"PUT")
    refused=call()
    assert refused[0]==429 and "limit_reached" in refused[1] and word+" limit reached" in refused[1],(weekly,total,refused)
    assert models(),"a key at its limit can still list models"
admin("keys",{**policy,"weekly":56001,"total":56001},"PUT")
assert call()[0]==200
wait(lambda: admin("keys")[0]["spent"]>56000,"non-stream call was not charged")
assert call()[0]==429
admin("keys",policy,"PUT")
assert call()[0]==200
assert "fixture-chat" in models()
assert admin("keys",{**policy,"models":["other-*"," Fixture-Else "]},"PUT")["models"]==["other-*","Fixture-Else"]
denied=call()
assert denied[0]==403 and "model_not_allowed" in denied[1],denied
assert models()==[],models()
assert admin("keys")[0]["models"]==["other-*","Fixture-Else"]
admin("keys",{**policy,"models":["FIXTURE-*"]},"PUT")
assert call()[0]==200 and models()==["fixture-chat"],(call(),models())
assert call("unlisted")[0]==403
admin("keys",policy,"PUT")
assert admin("keys")[0]["models"]==[] and call()[0]==200
print("official CPA v8: SSE immediate forwarding, weekly and total limits, settlement, transcript, session, paged request list and per-key model restriction passed")

# The stored JSON can exceed the inline preview budget even when the request
# itself is under 32 MiB, because the archive encodes the request as base64.
large_prompt = "x" * (25 * 1024 * 1024)
payload = json.dumps({"model":"fixture-chat","messages":[{"role":"user","content":large_prompt}]}).encode()
req = Request(GATEWAY+"/v1/chat/completions",payload,{"Authorization":"Bearer "+KEY,"Content-Type":"application/json"})
with urlopen(req,timeout=30) as response:
    assert response.status==200 and json.load(response)["choices"]
    large_turn = response.headers["X-CPA-Ext-Turn"]
large = wait(lambda: admin("turns/"+large_turn), "large turn was not archived")
assert large["body"] is None and large["body_status"]=="too_large",large
archive_url = ADMIN+"/api/turns/"+large_turn+"/archive"
try:
    urlopen(archive_url,timeout=5)
except HTTPError as error:
    assert error.code==401,error.code
else:
    raise AssertionError("archive download did not require authentication")
with urlopen(Request(archive_url,headers={"Authorization":"Basic "+BASIC}),timeout=10) as response:
    assert response.headers.get_content_type()=="application/gzip"
    assert response.headers["Content-Disposition"].startswith("attachment;")
    archive=json.loads(gzip.decompress(response.read()))
assert json.loads(base64.b64decode(archive["request_raw"]))["messages"][0]["content"]==large_prompt
print("Large request forwarded intact; summary and authenticated archive download passed")
