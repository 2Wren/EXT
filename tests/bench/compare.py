"""Run after tests/e2e/test.py against the same Oracle fixture."""
import base64
import hashlib
import json
import statistics
import time
from urllib.request import Request, urlopen

KEY="fixture-client-secret-local-only"
BODY=json.dumps({"model":"fixture-chat","stream":True,"stream_options":{"include_usage":True},
    "max_tokens":20,"messages":[{"role":"user","content":"bench"}]}).encode()
DIRECT="http://127.0.0.1:8582"
GATEWAY="http://127.0.0.1:8580"

def sample(endpoint):
    req=Request(endpoint+"/v1/chat/completions",BODY,{"Authorization":"Bearer "+KEY,"Content-Type":"application/json"})
    start=time.perf_counter_ns()
    with urlopen(req,timeout=5) as r:
        headers=time.perf_counter_ns()
        first=r.readline()
        token=time.perf_counter_ns()
        tail=r.read()
        end=time.perf_counter_ns()
    assert b"alpha" in first and b"beta" in tail
    return ((headers-start)/1e6,(token-start)/1e6,(end-token)/1e6,(len(first)+len(tail))*1e9/(end-start))

def p95(samples):
    return statistics.quantiles(samples,n=20,method="inclusive")[18]

basic=base64.b64encode(b"admin:fixture-administrator-secret-local-only").decode()
policy={"id":hashlib.sha256(KEY.encode()).hexdigest(),"weekly":1_000_000_000,"total":1_000_000_000}
credentials={"Authorization":"Basic "+basic,"X-CPA-Ext-Admin":"1","Content-Type":"application/json"}
with urlopen(Request("http://127.0.0.1:8581/api/keys",json.dumps(policy).encode(),credentials,method="PUT"),timeout=5):
    pass
time.sleep(1)
for endpoint in (DIRECT,GATEWAY):
    sample(endpoint)
samples={DIRECT:[],GATEWAY:[]}
for i in range(40):
    pair=(DIRECT,GATEWAY) if i%2 else (GATEWAY,DIRECT)
    for endpoint in pair:
        samples[endpoint].append(sample(endpoint))

for metric,index in (("TTFB ms",0),("first token ms",1),("first token → completion ms",2)):
    direct,ext=[p95([s[index] for s in samples[ep]]) for ep in (DIRECT,GATEWAY)]
    print(f"p95 {metric}: direct {direct:.3f}; through cpa-ext {ext:.3f}; delta {ext-direct:+.3f}")
direct,ext=[statistics.median(s[3] for s in samples[ep]) for ep in (DIRECT,GATEWAY)]
print(f"median response bytes/sec: direct {direct:.0f}; through cpa-ext {ext:.0f}; difference {(ext/direct-1)*100:+.2f}%")
