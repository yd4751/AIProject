"""简单的 TCP echo 服务，用于端到端验证端口转发是否连通。"""
import socketserver
import threading
import sys

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 19081


class EchoHandler(socketserver.StreamRequestHandler):
    def handle(self):
        while True:
            data = self.rfile.readline()
            if not data:
                break
            self.wfile.write(b"echo:" + data)


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


if __name__ == "__main__":
    srv = Server(("127.0.0.1", PORT), EchoHandler)
    print(f"echo server on {PORT}", flush=True)
    srv.serve_forever()
