"""Run after test.py/protocols.py against the isolated official-CPA fixture."""
import base64
import hashlib
import json
from urllib.error import HTTPError
from urllib.request import Request, urlopen
KEY="fixture-client-secret-local-only"
BASIC=base64.b64encode(b"admin:fixture-administrator-secret-local-only").decode()
def api(path,data=None,method="GET"):
    headers={"Authorization":"Basic "+BASIC,"X-CPA-Ext-Admin":"1","Content-Type":"application/json"}
    return json.load(urlopen(Request("http://127.0.0.1:8581/api/"+path,None if data is None else json.dumps(data).encode(),headers,method=method),timeout=10))
state=api("keys/sync",{},"POST")
assert not state["error"] and state["count"]==1,state
keys=api("keys")
assert KEY not in json.dumps(keys)
assert keys[0]["id"]==hashlib.sha256(KEY.encode()).hexdigest() and keys[0]["cpa_present"] is True
assert api("models")[0]["id"]=="fixture-chat"
prices=api("prices")
reference=next(p for p in prices if p["model"]=="gpt-4o" and p["protocol"]=="chat")
assert reference["input"]==2500 and reference["is_reference"] and not reference["verified"] and reference["source_url"]
api("prices",{"protocol":"chat","model":"gpt-4o","billed_model":"gpt-4o","input":1234,"output":5678,"cached":0,"cache_write":0,"max_input":0,"max_output":0,"verified":False,"is_reference":True,"source":"spoofed"},"POST")
prices=api("prices")
active=[p for p in prices if p["protocol"]=="chat" and p["model"]=="gpt-4o" and p["active"]]
assert len(active)==1 and active[0]["input"]==1234 and not active[0]["is_reference"] and active[0]["source"]=="manual"
assert next(p for p in prices if p["id"]==reference["id"])["input"]==2500
try:
    api("keys",{"id":"0"*64,"weekly":0,"total":0},"PUT")
    raise AssertionError("unknown CPA key accepted")
except HTTPError as e:
    assert e.code==404,e.code
print("Key synchronization, masking, models, reference pricing, manual precedence and immutable history passed")
