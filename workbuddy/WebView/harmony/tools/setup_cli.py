#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""解压 HarmonyOS Command Line Tools 到 E:\\install\\harmony-commandline-tools，并汇总工具链。

前置：先跑 fetch_cli.py 把 zip 下载完整。解压后调用 device_check.py 验证真机链路。
"""
import os
import subprocess
import sys
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
ZIP = r'E:\install\_downloads\commandline-tools-windows-x64-6.1.1.268.zip'
DEST = r'E:\install\harmony-commandline-tools'
EXPECTED = 2599585816
TOOLS = ['hdc.exe', 'hvigorw.bat', 'hvigorw', 'ohpm.bat', 'sdkmgr.bat', 'codelinter']


def extract():
    if not os.path.exists(ZIP):
        print('[FAIL] zip 不存在: ' + ZIP)
        return False
    size = os.path.getsize(ZIP)
    print('[INFO] zip size=%d expected=%d match=%s' % (size, EXPECTED, size == EXPECTED))
    if size != EXPECTED:
        print('[FAIL] zip 未下载完整，请先重跑 fetch_cli.py（支持断点续传）')
        return False
    os.makedirs(DEST, exist_ok=True)
    with zipfile.ZipFile(ZIP) as z:
        names = z.namelist()
        print('[INFO] zip entries=%d' % len(names))
        errors = 0
        for i, name in enumerate(names):
            try:
                z.extract(name, DEST)
            except Exception as e:
                errors += 1
                if errors <= 5:
                    print('[WARN] extract failed %s -> %s' % (name, str(e)[:160]))
            if (i + 1) % 2000 == 0:
                print('[PROGRESS] %d/%d' % (i + 1, len(names)), flush=True)
        print('[INFO] extract done, errors=%d' % errors)
    return True


def find_tools():
    found = {}
    for root, dirs, files in os.walk(DEST):
        depth = root[len(DEST):].count(os.sep)
        if depth > 9:
            dirs[:] = []
            continue
        for t in TOOLS:
            if t in files and t not in found:
                found[t] = os.path.join(root, t)
    for t in TOOLS:
        print('[TOOL] %-14s %s' % (t, found.get(t, '<not found>')))
    return found


def sdk_dirs():
    hits = []
    for root, dirs, files in os.walk(DEST):
        depth = root[len(DEST):].count(os.sep)
        if depth > 7:
            dirs[:] = []
            continue
        low = root.lower()
        if 'toolchains' in low or os.sep + 'ets' in low or 'api' in os.path.basename(root).lower():
            hits.append(root)
    print('[SDK] 可疑 SDK/toolchains 目录 %d 个:' % len(hits))
    for h in hits[:15]:
        print('       ' + h)
    for marker in ['26.0.0', 'api26', '26']:
        n = sum(1 for h in hits if marker in h.lower())
        print('[SDK] 含 %s 的目录数: %d' % (marker, n))


def main():
    if not extract():
        return 2
    find_tools()
    sdk_dirs()
    print('--- 真机链路自检 ---')
    dev = os.path.join(HERE, 'device_check.py')
    p = subprocess.run([sys.executable, dev], capture_output=True)
    sys.stdout.write((p.stdout or b'').decode('utf-8', 'replace'))
    sys.stdout.write((p.stderr or b'').decode('utf-8', 'replace'))
    return 0


if __name__ == '__main__':
    sys.exit(main())
