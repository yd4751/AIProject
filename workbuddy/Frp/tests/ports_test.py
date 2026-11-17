"""验证实时端口管理：动态新增立即转发、关闭立即断开、重连后回放。"""
import base64
import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EXE = os.path.join(ROOT, "frptun.exe" if os.name == "nt" else "frptun")
ADMIN = "http://127.0.0.1:17500"
AUTH = "Basic " + base64.b64encode(b"admin:test-admin").decode()
DB = os.path.join(ROOT, "tests", "test_ports.db")
procs = []
results = []


def log(m): print(f"[PORT-TEST] {m}", flush=True)
def check(name, cond, detail=""):
    results.append((name, bool(cond)))
    log(f"{'PASS' if cond else 'FAIL'} {name} {detail}")


def start(cmd, logfile):
    f = open(os.path.join(ROOT, "tests", logfile), "w")
    p = subprocess.Popen(cmd, cwd=ROOT, stdout=f, stderr=subprocess.STDOUT)
    procs.append(p)
    return p


def api(path, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Authorization", AUTH)
    if data: req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read().decode())


def probe(port, msg="x"):
    s = socket.create_connection(("127.0.0.1", port), timeout=3)
    s.settimeout(3)
    f = s.makefile("rwb")
    f.write((msg + "\n").encode()); f.flush()
    line = f.readline()
    f.close(); s.close()
    return line == ("echo:" + msg + "\n").encode()


def wait(pred, timeout=12):
    end = time.time() + timeout
    while time.time() < end:
        try:
            if pred(): return True
        except Exception: pass
        time.sleep(0.3)
    return False


def main():
    for f in [DB, DB + "-wal", DB + "-shm"]:
        if os.path.exists(f): os.remove(f)

    # 用专用配置：端口 17001/17501，堆叠 echo 19091
    srv_cfg = os.path.join(ROOT, "tests", "server_ports.json")
    cli_cfg = os.path.join(ROOT, "tests", "client_ports.json")
    with open(srv_cfg, "w") as f:
        json.dump({"role": "server", "bind_addr": "127.0.0.1", "bind_port": 17001,
                   "token": "test-token", "db_path": "tests/test_ports.db", "auth_mode": "off",
                   "admin": {"enabled": True, "addr": "127.0.0.1", "port": 17501,
                             "user": "admin", "password": "test-admin"},
                   "heartbeat_timeout": 30, "max_connections": 100}, f)
    with open(cli_cfg, "w") as f:
        json.dump({"role": "client", "server_addr": "127.0.0.1", "server_port": 17001,
                   "token": "test-token", "heartbeat_interval": 5, "reconnect_interval": 1,
                   "services": [{"name": "base", "local_ip": "127.0.0.1", "local_port": 19091,
                                 "remote_port": 19090}]}, f)
    global ADMIN
    ADMIN = "http://127.0.0.1:17501"

    start([sys.executable, "tests/echo_server.py", "19091"], "echo_ports.log")
    start([EXE, "-c", srv_cfg], "srv_ports.log")
    time.sleep(1)
    start([EXE, "-c", cli_cfg], "cli_ports.log")

    check("客户端上线", wait(lambda: api("/api/overview")["online_clients"] == 1))
    check("基础端口转发", wait(lambda: probe(19090, "base")))

    ports = api("/api/ports")["ports"]
    mid = ports[0]["machine_id"]
    check("列出配置映射", any(p["remote_port"] == 19090 and p["source"] == "static" for p in ports))

    # 1. 实时新增 :19092 -> 19091
    r = api("/api/ports", "POST", {"machine_id": mid, "name": "dyn-test",
        "local_ip": "127.0.0.1", "local_port": 19091, "remote_port": 19092, "note": "e2e"})
    check("新增接口返回 OK", r.get("ok"), str(r))
    check("客户端确认同步", r.get("client_acked"), str(r.get("client_acked")))
    check("新端口立即可转发", wait(lambda: probe(19092, "dyn")))

    ports = api("/api/ports")["ports"]
    check("列表含动态映射", any(p["remote_port"] == 19092 and p["source"] == "dynamic" and p["active"] for p in ports))

    # 2. 实时关闭 :19092
    api("/api/ports?machine_id=" + mid + "&remote_port=19092", "DELETE")
    time.sleep(0.6)
    def refused():
        try:
            probe(19092, "shouldfail"); return False
        except (ConnectionRefusedError, OSError): return True
    check("关闭端口立即拒绝连接", refused())
    ports = api("/api/ports")["ports"]
    check("关闭后映射从列表消失或停用", not any(p["remote_port"] == 19092 and p["active"] for p in ports))

    # 3. 关闭静态端口 :19090，也不应再转发
    api("/api/ports?machine_id=" + mid + "&remote_port=19090", "DELETE")
    time.sleep(0.5)
    try:
        probe(19090, "x"); closed = False
    except (ConnectionRefusedError, OSError): closed = True
    check("静态端口关闭生效", closed)

    # 4. 重新新增一个映射，然后重启客户端，验证回放
    api("/api/ports", "POST", {"machine_id": mid, "name": "persist", "local_ip": "127.0.0.1",
        "local_port": 19091, "remote_port": 19093, "note": "persist"})
    check("新增待回放端口", wait(lambda: probe(19093, "p1")))
    cli = procs[-1]
    cli.terminate()
    try: cli.wait(timeout=5)
    except subprocess.TimeoutExpired: cli.kill()
    time.sleep(0.5)
    start([EXE, "-c", cli_cfg], "cli_ports_re.log")
    check("客户端重连上线", wait(lambda: api("/api/overview")["online_clients"] == 1))
    check("动态端口重连后回放生效", wait(lambda: probe(19093, "p2")))
    time.sleep(0.5)
    try:
        probe(19090, "x"); still_closed = False
    except (ConnectionRefusedError, OSError): still_closed = True
    check("被关闭静态端口重连后仍关闭", still_closed)

    log("=" * 40)
    passed = sum(1 for _, ok in results if ok)
    log(f"总计 {passed}/{len(results)} 通过")
    for n, ok in results:
        if not ok: log(f"  失败: {n}")
    return 0 if passed == len(results) else 1


if __name__ == "__main__":
    try: code = main()
    finally:
        for p in procs:
            if p.poll() is None: p.terminate()
        time.sleep(0.5)
        for p in procs:
            if p.poll() is None: p.kill()
    sys.exit(code)
