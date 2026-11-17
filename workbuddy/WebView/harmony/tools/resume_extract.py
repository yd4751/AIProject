#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""补齐 Command Line Tools 解压（跳过 NDK/llvm 头文件，保留 hvigorw/ohpm/SDK/toolchains）。

首次 setup_cli.py 因超时只解了 41350/93548 个文件；此脚本只解压缺失项，
跳过体积最大但与 HAP 构建无关的 native/llvm/include 头文件目录。
"""
import os
import zipfile

ZIP = r'E:\install\_downloads\commandline-tools-windows-x64-6.1.1.268.zip'
DEST = r'E:\install\harmony-commandline-tools'
SKIP = ('command-line-tools/sdk/default/openharmony/native/llvm/include/',)


def main():
    with zipfile.ZipFile(ZIP) as z:
        files = [n for n in z.namelist() if not n.endswith('/')]
        missing = [n for n in files
                   if not os.path.exists(os.path.join(DEST, *n.split('/')))
                   and not n.startswith(SKIP)]
        skipped = sum(1 for n in files if n.startswith(SKIP))
        print('zip files=%d skipped(NDK headers)=%d to_extract=%d' %
              (len(files), skipped, len(missing)), flush=True)
        ok, bad = 0, 0
        for i, n in enumerate(missing):
            try:
                z.extract(n, DEST)
                ok += 1
            except Exception as e:
                bad += 1
                if bad <= 5:
                    print('[WARN] %s -> %s' % (n, str(e)[:140]), flush=True)
            if (i + 1) % 5000 == 0:
                print('[PROGRESS] %d/%d' % (i + 1, len(missing)), flush=True)
        print('[DONE] extracted=%d failed=%d' % (ok, bad), flush=True)
        for tool in ['bin/hvigorw.bat', 'bin/ohpm.bat', 'bin/codelinter.bat',
                     'sdk/default/openharmony/toolchains/hdc.exe']:
            p = os.path.join(DEST, 'command-line-tools', *tool.split('/'))
            print('[TOOL] %-55s %s' % (tool, os.path.exists(p)), flush=True)
    return 0 if bad == 0 else 1


if __name__ == '__main__':
    raise SystemExit(main())
