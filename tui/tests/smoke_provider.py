#!/usr/bin/env python3
"""A scripted OpenAI-compatible stub for the nvchat visual smoke.

Not part of the product: the smoke uses it to make the model delegate to the
subagent tool (so a real job streams through the link), answer the child's
brief, and speak after the job completes. Anything else is echoed.
"""

import json
from http.server import BaseHTTPRequestHandler, HTTPServer


def answer(last: str) -> str:
    if "delegate" in last:
        return json.dumps({"tool": "subagent", "args": {"brief": "answer the count"}})
    if "answer the count" in last:
        return "the count is three"
    if "job." in last:
        return "the delegation finished"
    return "stub says: " + last


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length", 0))
        body = json.loads(self.rfile.read(length) or b"{}")
        last = ""
        for message in body.get("messages", []):
            if message.get("role") == "user":
                last = message.get("content", "")
        out = json.dumps(
            {"choices": [{"message": {"role": "assistant", "content": answer(last)}}]}
        ).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, fmt, *args):
        print(fmt % args, flush=True)


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", 8099), Handler).serve_forever()
