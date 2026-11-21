import json
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = "fixture-chat"

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if not data.get("stream"):
            payload = json.dumps({
                "id": "chatcmpl-"+uuid.uuid4().hex, "object": "chat.completion",
                "created": int(time.time()), "model": MODEL,
                "choices": [{"index":0,"message":{"role":"assistant","content":"alpha beta"},"finish_reason":"stop"}],
                "usage": {"prompt_tokens":20,"completion_tokens":4,"total_tokens":24},
            }).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()

        response_id = "chatcmpl-"+uuid.uuid4().hex

        def chunk(delta=None, usage=None, finish=None):
            item = {"id":response_id,"object":"chat.completion.chunk","created":int(time.time()),"model":MODEL}
            if usage is None:
                item["choices"] = [{"index":0,"delta":delta,"finish_reason":finish}]
            else:
                item["choices"] = []
                item["usage"] = usage
            self.wfile.write(("data: "+json.dumps(item)+"\n\n").encode())
            self.wfile.flush()

        chunk({"role":"assistant","content":"alpha "})
        time.sleep(.05 if data.get("messages",[{}])[-1].get("content")=="bench" else 2)
        chunk({"content":"beta"})
        chunk({}, finish="stop")
        chunk(usage={"prompt_tokens":20,"completion_tokens":4,"total_tokens":24})
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
