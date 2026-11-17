"""向指定端口发送若干行并校验 echo 回包，验证转发链路。"""
import socket
import sys

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 19080
N = int(sys.argv[2]) if len(sys.argv) > 2 else 3

try:
    s = socket.create_connection(("127.0.0.1", PORT), timeout=5)
    s.settimeout(5)
    f = s.makefile("rwb")
    ok = 0
    for i in range(N):
        f.write(f"hello-{i}\n".encode())
        f.flush()
        line = f.readline()
        if line == f"echo:hello-{i}\n".encode():
            ok += 1
        else:
            print(f"FAIL reply: {line!r}")
            break
    f.close()
    s.close()
    print(f"RESULT {ok}/{N}")
    sys.exit(0 if ok == N else 1)
except Exception as e:
    print(f"ERROR {e}")
    sys.exit(1)
