import json, os, signal, socketserver, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
state, commit, service = sys.argv[1], sys.argv[2], sys.argv[3]
data = os.path.join(state, "data")
os.makedirs(data, exist_ok=True)
os.chdir(data)  # like the real daemon: its cwd names its data dir
db = os.path.join(data, "ao.db")
# A "migration": every release appends its commit to the database once.
lines = open(db).read().split() if os.path.exists(db) else []
if not lines or lines[-1] != commit:
    with open(db, "a") as f:
        f.write(commit + "\n")
run = os.path.join(state, "running.json")
class Server(HTTPServer):
    def server_bind(self):  # skip HTTPServer's slow getfqdn() lookup
        socketserver.TCPServer.server_bind(self)
srv = Server(("127.0.0.1", 0), None)
def stop(*_):
    for p in (run, os.path.join(data, "daemon.lock")):
        try: os.remove(p)
        except FileNotFoundError: pass
    os._exit(0)
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        if self.path not in ("/healthz", "/readyz"):
            self.send_response(404); self.end_headers(); return
        body = json.dumps({"status": "ok", "service": service, "pid": os.getpid(), "build": {"commit": commit}}).encode()
        self.send_response(200); self.send_header("content-type", "application/json"); self.end_headers(); self.wfile.write(body)
    def do_POST(self):
        # Like a signed daemon enforcing its peer check (B631): the installer is not a process
        # Tallpine started, so its API calls are refused. It must stop the daemon with SIGTERM.
        with open(os.path.join(state, "shutdown-refused"), "a") as f: f.write(self.path + "\n")
        body = json.dumps({"code": "PEER_NOT_ADMITTED", "message": "refused"}).encode()
        self.send_response(403); self.send_header("content-type", "application/json"); self.end_headers(); self.wfile.write(body)
srv.RequestHandlerClass = H
signal.signal(signal.SIGTERM, stop)
with open(os.path.join(data, "daemon.lock"), "w") as f: f.write(str(os.getpid()))
with open(run + ".tmp", "w") as f: json.dump({"pid": os.getpid(), "port": srv.server_address[1]}, f)
os.rename(run + ".tmp", run)
srv.serve_forever()
