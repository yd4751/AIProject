#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""下载 HarmonyOS Command Line Tools（内含 hdc / hvigorw / ohpm / SDK）。

直链来自开源项目 hhtczengjing/ohos_command_line_tools 记录的华为官方签名 URL，
版本号 6.1.1.268（2026-04-30）；直链已用 Range 请求实测返回 206 + PK 头。
支持断点续传，完成后校验文件大小与 SHA256。下载位置遵循用户约定：E:\\install 独立目录。
"""
import hashlib
import os
import sys
import time
import urllib.request

URL = (
    'https://contentcenter-vali-drcn.dbankcdn.cn/pvt_2/DeveloperAlliance_package_901_9/52/v3/'
    'DEBhOt6HRlerelmIqscEkQ/commandline-tools-windows-x64-6.1.1.268.zip'
    '?HW-CC-KV=V1&HW-CC-Date=20260518T055125Z&HW-CC-Expire=315360000'
    '&HW-CC-Sign=4E5D72795EF68F11E03E1545C24BF03307CAE195822ED7C9C271DEF21D2E5E4A'
)
OUT = r'E:\install\_downloads\commandline-tools-windows-x64-6.1.1.268.zip'
EXPECTED = 2599585816
EXPECTED_SHA = '40cc0d9d677406f6f8f2704107dcff89e36ed271ead357db5cef62501473f37e'


def probe_range(start):
    req = urllib.request.Request(URL, headers={
        'User-Agent': 'Mozilla/5.0', 'Range': 'bytes=%d-%d' % (start, start + 1023)
    })
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.status


def download():
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    start = os.path.getsize(OUT) if os.path.exists(OUT) else 0
    if start >= EXPECTED:
        print('[SKIP] 文件已完整: %d bytes' % start, flush=True)
        return
    if start > 0:
        # 防「服务端忽略 Range 返回 200」导致重复追加写超
        status = probe_range(start)
        if status != 206:
            print('[WARN] 续传探测返回 %d（忽略 Range），删除分片重新下载' % status, flush=True)
            os.remove(OUT)
            start = 0
    headers = {'User-Agent': 'Mozilla/5.0'}
    if start > 0:
        headers['Range'] = 'bytes=%d-' % start
    req = urllib.request.Request(URL, headers=headers)
    t0 = time.time()
    with urllib.request.urlopen(req, timeout=180) as r:
        if start > 0 and r.status != 206:
            raise RuntimeError('续传时服务端未返回 206（%d），请删除 %s 后重跑' % (r.status, OUT))
        print('[START] status=%d offset=%d' % (r.status, start), flush=True)
        with open(OUT, 'ab') as f:
            total = start
            last = t0
            while True:
                buf = r.read(1024 * 1024)
                if not buf:
                    break
                f.write(buf)
                total += len(buf)
                now = time.time()
                if now - last >= 10:
                    speed = (total - start) / max(now - t0, 0.001) / 1048576
                    print('[PROGRESS] %d/%d (%.1f%%) %.1f MB/s' %
                          (total, EXPECTED, total * 100.0 / EXPECTED, speed), flush=True)
                    last = now
    print('[DONE] size=%d elapsed=%.1fs' % (os.path.getsize(OUT), time.time() - t0), flush=True)


def verify():
    h = hashlib.sha256()
    with open(OUT, 'rb') as f:
        for b in iter(lambda: f.read(1024 * 1024), b''):
            h.update(b)
    digest = h.hexdigest()
    size = os.path.getsize(OUT)
    print('[VERIFY] size=%d expected=%d match=%s' % (size, EXPECTED, size == EXPECTED))
    print('[VERIFY] sha256=%s' % digest)
    print('[VERIFY] sha256 match=%s (expected=%s)' % (digest == EXPECTED_SHA, EXPECTED_SHA))
    return size == EXPECTED


if __name__ == '__main__':
    download()
    ok = verify()
    sys.exit(0 if ok else 1)
