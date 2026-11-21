"""Run after tests/e2e/test.py with the official CPA integration stack."""
import base64
import hashlib
import json
import time
from urllib.request import Request, urlopen

ADMIN="http://127.0.0.1:8581/api/"
GATEWAY="http://127.0.0.1:8580"
KEY="fixture-client-secret-local-only"
BASIC=base64.b64encode(b"admin:fixture-administrator-secret-local-only").decode()

def admin(path, data=None, method="GET"):
    headers={"Authorization":"Basic "+BASIC,"X-CPA-Ext-Admin":"1","Content-Type":"application/json"}
    return json.load(urlopen(Request(ADMIN+path,None if data is None else json.dumps(data).encode(),headers,method=method),timeout=5))

admin("keys",{"id":hashlib.sha256(KEY.encode()).hexdigest(),"weekly":1_000_000_000,"total":1_000_000_000},"PUT")
calls=[
    ("responses","/v1/responses",{"model":"fixture-chat","input":"bench","max_output_tokens":20,"stream":True}),
    ("messages","/v1/messages",{"model":"fixture-chat","messages":[{"role":"user","content":"bench"}],"max_tokens":20,"stream":True}),
    ("gemini","/v1beta/models/fixture-chat:streamGenerateContent?alt=sse",{"contents":[{"role":"user","parts":[{"text":"bench"}]}],"generationConfig":{"maxOutputTokens":20}}),
]
for protocol,path,data in calls:
    admin("prices",{"protocol":protocol,"model":"fixture-chat","billed_model":"fixture-chat","input":1000,"output":2000,"cached":500,"cache_write":1000,"max_input":1000,"max_output":20,"verified":True},"POST")
    time.sleep(.6)
    auth={"X-Api-Key":KEY,"anthropic-version":"2023-06-01"} if protocol=="messages" else ({"X-Goog-Api-Key":KEY} if protocol=="gemini" else {"Authorization":"Bearer "+KEY})
    headers={**auth,"Content-Type":"application/json","X-Session-ID":"fixture-"+protocol}
    normal=path.replace(":streamGenerateContent?alt=sse",":generateContent")
    for endpoint,request_body in ((path,data),(normal,{k:v for k,v in data.items() if k!="stream"})):
        with urlopen(Request(GATEWAY+endpoint,json.dumps(request_body).encode(),headers),timeout=8) as r:
            turn=r.headers["X-CPA-Ext-Turn"]
            body=r.read()
            assert b"alpha " in body and b"beta" in body,(protocol,endpoint,body)
        for _ in range(40):
            time.sleep(.2)
            try:
                row=admin("turns/"+turn)
            except OSError:
                continue
            if row["turn"]["cost"] is not None:
                break
        else:
            raise AssertionError((protocol,row,admin("events")["rows"][:4]))
        assert row["turn"]["cost"]==28000,(protocol,row["turn"])
        assert row["body"]["output"]["text"]=="alpha beta",(protocol,row["body"]["output"])
    print(protocol+": key passthrough, streaming and non-streaming capture and billing passed")
