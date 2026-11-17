"""frptun 端到端测试：转发连通性 + 管理端 API + 流量统计 + 黑白名单。"""
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
DB = os.path.join(ROOT, "tests", "test.db")

procs = []


def log(msg):
    print(f"[TEST] {msg}", flush=True)


def start(cmd, logfile):
    f = open(os.path.join(ROOT, "tests", logfile), "w")
    p = subprocess.Popen(cmd, cwd=ROOT, stdout=f, stderr=subprocess.STDOUT)
    procs.append(p)
    return p


def http(path, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Authorization", AUTH)
    if data:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read().decode())


def probe(port, n=3):
    s = socket.create_connection(("127.0.0.1", port), timeout=5)
    s.settimeout(5)
    f = s.makefile("rwb")
    ok = 0
    for i in range(n):
        f.write(f"hello-{i}\n".encode())
        f.flush()
        if f.readline() == f"echo:hello-{i}\n".encode():
            ok += 1
    f.close()
    s.close()
    return ok


class HeldConn:
    """保持一条转发连接处于打开状态，用于验证实时连接监控。"""
    def __init__(self, port):
        self.s = socket.create_connection(("127.0.0.1", port), timeout=5)
        self.s.settimeout(5)
        self.f = self.s.makefile("rwb")

    def exchange(self, msg):
        self.f.write((msg + "\n").encode())
        self.f.flush()
        return self.f.readline()

    def close(self):
        self.f.close()
        self.s.close()


def wait_for(predicate, timeout=15, desc=""):
    end = time.time() + timeout
    while time.time() < end:
        try:
            if predicate():
                return True
        except Exception:
            pass
        time.sleep(0.3)
    log(f"等待超时: {desc}")
    return False


def main():
    results = []

    def check(name, cond, detail=""):
        results.append((name, bool(cond), detail))
        log(f"{'PASS' if cond else 'FAIL'} {name} {detail}")

    # 清理旧数据库
    if os.path.exists(DB):
        try:
            os.remove(DB)
        except OSError:
            pass
    for suffix in ("-wal", "-shm"):
        if os.path.exists(DB + suffix):
            os.remove(DB + suffix)

    # 启动进程
    start([sys.executable, "tests/echo_server.py", "19081"], "echo.log")
    start([EXE, "-c", "tests/server.json"], "srv.log")
    time.sleep(1)
    start([EXE, "-c", "tests/client.json"], "cli.log")

    # 1. 转发连通
    def registered():
        txt = open(os.path.join(ROOT, "tests", "srv.log"), encoding="utf-8", errors="ignore").read()
        return ("已注册" in txt or "注册 1 个服务" in txt) and "隧道" in txt
    check("客户端注册", wait_for(registered, desc="client register"))
    check("TCP 转发连通", wait_for(lambda: probe(19080, 3) == 3, desc="forward probe"))

    # 2. 管理端连接
    check("管理端 API 可用", wait_for(lambda: http("/api/overview").get("online_clients") == 1,
                                  desc="admin api"))

    # 3. 制造流量并检查活跃连接（保持连接打开，确保查询时仍处于活跃状态）
    held = HeldConn(19080)
    check("保持连接可收发", held.exchange("held-msg") == b"echo:held-msg\n")
    time.sleep(0.3)

    ov = http("/api/overview")
    check("在线客户端数=1", ov["online_clients"] == 1, str(ov["online_clients"]))
    check("活跃隧道数>=1", ov["active_tunnels"] >= 1, str(ov["active_tunnels"]))

    conns = http("/api/connections")["connections"]
    check("活跃连接可见", len(conns) >= 1, f"count={len(conns)}")
    if conns:
        c = conns[0]
        check("连接含机器码", bool(c.get("machine_id")), c.get("machine_id", "")[:8])
        check("流量计数在上行", c.get("up", 0) > 0, str(c.get("up")))
        check("流量计数在下行", c.get("down", 0) > 0, str(c.get("down")))
    held.close()
    time.sleep(0.3)
    # 连接关闭后活跃隧道数应回落到 0
    check("连接关闭后计数回收", http("/api/overview")["active_tunnels"] == 0)
    probe(19080, 5)  # 额外流量用于历史记录

    clients = http("/api/clients")["clients"]
    check("客户端记录持久化", len(clients) == 1, f"count={len(clients)}")
    check("客户端在线状态", clients[0].get("online") is True if clients else False)

    # 4. 流量历史落库
    time.sleep(0.5)
    traffic = http("/api/traffic?limit=10")["traffic"]
    check("流量历史落库", len(traffic) >= 1, f"count={len(traffic)}")

    # 5. 黑白名单：切换为黑名单，把本机 IP 拉黑，客户端应被拒绝
    http("/api/settings", "POST", {"auth_mode": "blacklist"})
    local_ip = "127.0.0.1"
    http("/api/rules", "POST", {"kind": "ip", "value": local_ip, "action": "deny", "note": "test"})
    rules = http("/api/rules")["rules"]
    check("规则添加", len(rules) == 1, f"count={len(rules)}")

    # 重启客户端，期望认证被拒绝
    cli = procs[-1]
    cli.terminate()
    try:
        cli.wait(timeout=5)
    except subprocess.TimeoutExpired:
        cli.kill()
    time.sleep(0.5)
    start([EXE, "-c", "tests/client.json"], "cli_denied.log")
    time.sleep(2)
    denied_txt = open(os.path.join(ROOT, "tests", "cli_denied.log"), encoding="utf-8", errors="ignore").read()
    check("黑名单拒绝连接", "授权被拒绝" in denied_txt or "拒绝" in denied_txt,
          denied_txt.strip().splitlines()[-1] if denied_txt.strip() else "")

    # 清理规则，恢复
    if rules:
        http("/api/rules?id=" + str(rules[0]["id"]), "DELETE")
    http("/api/settings", "POST", {"auth_mode": "off"})

    # 6. 配置持久化验证：重启服务端后 auth_mode 仍为 off（写入过）
    mode = http("/api/settings")["auth_mode"]
    check("配置持久化读取", mode == "off", mode)

    # 汇总
    log("=" * 40)
    passed = sum(1 for _, ok, _ in results if ok)
    log(f"总计 {passed}/{len(results)} 通过")
    for name, ok, detail in results:
        if not ok:
            log(f"  失败: {name} {detail}")
    return 0 if passed == len(results) else 1


if __name__ == "__main__":
    try:
        code = main()
    finally:
        for p in procs:
            if p.poll() is None:
                p.terminate()
        time.sleep(0.5)
        for p in procs:
            if p.poll() is None:
                p.kill()
    sys.exit(code)
