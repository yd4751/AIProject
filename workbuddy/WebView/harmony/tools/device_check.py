#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""鸿蒙真机链路自检：定位 hdc.exe → 列设备 → 读系统版本/型号 → 可选安装 HAP。

用法：
  python harmony/tools/device_check.py
  python harmony/tools/device_check.py --install <已签名.hap>
"""
import os
import subprocess
import sys
from shutil import which

CANDIDATES = [
    r'E:\install\DevEco Studio',
    r'E:\install\harmony-commandline-tools',
    r'E:\install\harmony-sdk',
    r'C:\Program Files\Huawei',
    os.path.expandvars(r'%LOCALAPPDATA%\Huawei'),
]
MAX_DEPTH = 8


def find_hdc():
    p = which('hdc') or which('hdc.exe')
    if p:
        return p
    for base in CANDIDATES:
        if not os.path.isdir(base):
            continue
        for root, dirs, files in os.walk(base):
            depth = root[len(base):].count(os.sep)
            if depth > MAX_DEPTH:
                dirs[:] = []
                continue
            if 'hdc.exe' in files:
                return os.path.join(root, 'hdc.exe')
    return ''


def run(hdc, args, timeout=25):
    try:
        p = subprocess.run([hdc] + args, capture_output=True, timeout=timeout)
        out = (p.stdout or b'').decode('utf-8', 'replace')
        err = (p.stderr or b'').decode('utf-8', 'replace')
        return (out + err).strip(), p.returncode
    except Exception as e:
        return 'ERR: ' + str(e), 1


def main():
    hdc = find_hdc()
    if not hdc:
        print('[FAIL] 未找到 hdc.exe。请先安装 DevEco Studio 26 或解压 Command Line Tools 到 E:\\install。')
        print('[HINT] 本机候选目录：' + ', '.join(CANDIDATES))
        return 2
    print('[OK] hdc = ' + hdc)

    ver, _ = run(hdc, ['-v'])
    print('[INFO] hdc -v: ' + ver.replace('\n', ' | '))

    # hdc server 偶发「Connect server failed」（手动执行正常、脚本管道执行时高发），重试并重启服务。
    # 重启服务不会删除手机端已保存的授权记录，可安全执行。
    out, code = '', 1
    for _attempt in range(4):
        out, code = run(hdc, ['list', 'targets', '-v'])
        if out.strip() and 'Connect server failed' not in out:
            break
        run(hdc, ['kill'], 20)
        run(hdc, ['start', '-r'], 20)
    print('[INFO] hdc list targets -v (exit=%d): %s' % (code, out.replace('\n', ' | ')))
    # -v 行格式：<connectKey>\t\tUSB\t<状态>\tlocalhost\thdc
    # 注意设备名与 USB 之间有两个 tab，必须过滤空字段后再取状态，不能写死下标。
    usb_rows = []
    for line in out.splitlines():
        parts = [p.strip() for p in line.split('\t') if p.strip()]
        if len(parts) >= 3 and parts[1] == 'USB':
            usb_rows.append((parts[0], parts[2]))
    if not usb_rows:
        print('[FAIL] 没有 USB 在线设备。请确认：手机已开开发者模式 + USB 调试、数据线支持传输、屏幕已解锁。')
        return 3

    unauthorized = [key for key, state in usb_rows if state != 'Connected']
    for key, state in usb_rows:
        print('[DEVICE] %s  USB  %s' % (key, state))
    if unauthorized:
        print('[FAIL] 设备未授权（%s）。手机上会弹出「是否允许 USB 调试」，勾选「始终允许」并点「允许」。'
              % ', '.join(unauthorized))
        print('[HINT] 若弹窗不出现：① 保持屏幕解锁亮屏 ② 开发者选项里「撤销 USB 调试授权」后重插数据线'
              ' ③ USB 连接模式切到「传输文件」 ④ 本机已执行 hdc kill + 重置密钥，重新插拔会重发新公钥。')
        return 4

    print('[OK] 已授权设备 %d 台' % len(usb_rows))
    target = usb_rows[0][0]
    prefix = ['-t', target] if len(targets) > 1 else []
    probes = [
        ('系统版本', ['shell', 'param get const.product.software.version']),
        ('设备型号', ['shell', 'param get const.product.model']),
        ('品牌', ['shell', 'param get const.product.brand']),
        ('API 版本', ['shell', 'param get const.ohos.apiversion']),
        ('SDK 版本', ['shell', 'param get const.arkui.sdk.version']),
    ]
    for label, args in probes:
        text, _ = run(hdc, prefix + args)
        print('[DEVICE] %s: %s' % (label, text.replace('\n', ' | ')))

    if '--install' in sys.argv:
        idx = sys.argv.index('--install')
        if idx + 1 < len(sys.argv):
            hap = sys.argv[idx + 1]
            if not os.path.exists(hap):
                print('[FAIL] HAP 不存在: ' + hap)
                return 4
            print('[INFO] 安装 ' + hap)
            text, code = run(hdc, prefix + ['install', '-r', hap], timeout=120)
            print('[INSTALL] exit=%d %s' % (code, text.replace('\n', ' | ')))
            if code != 0:
                print('[HINT] 未签名 HAP 无法安装。需在 DevEco Studio 里用华为账号自动签名后再试。')
    return 0


if __name__ == '__main__':
    sys.exit(main())
