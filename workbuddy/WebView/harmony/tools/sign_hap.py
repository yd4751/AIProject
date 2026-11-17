#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""站点坞 HarmonyOS HAP 本地签名（不依赖华为账号 / DevEco 自动签名）。

使用 Command Line Tools 自带的 OpenHarmony.p12 + hap-sign-tool.jar：
  1. 从密钥库导出应用签名证书（PEM）
  2. 按本工程 bundleName 生成 Provision Profile（有效期 1 年，可写入真机 UDID）
  3. sign-profile 签出 .p7b；sign-app 把未签名 HAP 签成可安装 HAP
  4. verify-app 校验签名块

用法：
  python harmony/tools/sign_hap.py                     # 用未签名 HAP，UDID 留空
  python harmony/tools/sign_hap.py --udid <UDID>      # 指定真机 UDID（debug profile 需要）
  python harmony/tools/sign_hap.py --fetch-udid       # 授权后自动从 hdc 读取 UDID

注意：OpenHarmony 自签证书能否被 HarmonyOS 商业真机接受，必须真机实测；
      若被拒，仍需 DevEco Studio 26 登录华为账号做自动签名。
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time
import uuid

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CLI = r'E:\install\harmony-commandline-tools\command-line-tools'
LIB = os.path.join(CLI, 'sdk', 'default', 'openharmony', 'toolchains', 'lib')
JAVA = r'E:\install\jdk-17\bin\java.exe'
KEYTOOL = r'E:\install\jdk-17\bin\keytool.exe'
HDC = os.path.join(CLI, 'sdk', 'default', 'openharmony', 'toolchains', 'hdc.exe')

KEYSTORE = os.path.join(LIB, 'OpenHarmony.p12')
SIGN_TOOL = os.path.join(LIB, 'hap-sign-tool.jar')
PROFILE_CERT = os.path.join(LIB, 'OpenHarmonyProfileDebug.pem')
PROFILE_TPL = os.path.join(LIB, 'UnsgnedDebugProfileTemplate.json')
KEY_PWD = '123456'          # 工具包自带密钥库的公开默认口令
STORE_PWD = '123456'
APP_ALIAS = 'openharmony application release'
PROFILE_ALIAS = 'openharmony application profile debug'
SIGN_ALG = 'SHA256withECDSA'

OUT_DIR = os.path.join(ROOT, 'build')
UNSIGNED = os.path.join(ROOT, 'entry', 'build', 'default', 'outputs', 'default',
                        'entry-default-unsigned.hap')
SIGNED = os.path.join(OUT_DIR, 'entry-default-signed.hap')


def run(cmd, label):
    p = subprocess.run(cmd, capture_output=True, timeout=180)
    out = (p.stdout or b'').decode('utf-8', 'replace') + (p.stderr or b'').decode('utf-8', 'replace')
    print('[%s] exit=%d' % (label, p.returncode))
    if p.returncode != 0 or 'ERROR' in out.upper():
        print(out.strip()[-1500:])
    return p.returncode, out


def bundle_name():
    p = os.path.join(ROOT, 'AppScope', 'app.json5')
    text = open(p, encoding='utf-8').read()
    text = '\n'.join(l for l in text.splitlines() if not l.strip().startswith('//'))
    return json.loads(text)['app']['bundleName']


def fetch_udid():
    if not os.path.exists(HDC):
        return ''
    _, out = run([HDC, 'shell', 'bm', 'get', '--udid'], 'hdc udid')
    for line in out.splitlines():
        s = line.strip()
        if s and not s.startswith('[') and 'Fail' not in s and 'error' not in s.lower():
            return s
    return ''


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--udid', default='', help='真机 UDID，写入 debug profile 的 device-ids')
    ap.add_argument('--fetch-udid', action='store_true', help='从已授权 hdc 设备自动读取 UDID')
    args = ap.parse_args()

    for path, label in [(JAVA, 'JDK17'), (KEYSTORE, 'OpenHarmony.p12'),
                        (SIGN_TOOL, 'hap-sign-tool.jar'), (UNSIGNED, '未签名 HAP')]:
        if not os.path.exists(path):
            print('[FAIL] 缺少 %s: %s' % (label, path))
            return 2
    os.makedirs(OUT_DIR, exist_ok=True)

    udid = args.udid or (fetch_udid() if args.fetch_udid else '')
    if not udid:
        print('[WARN] 未提供真机 UDID：debug profile 的 device-ids 留空，真机可能拒绝安装。')
    else:
        print('[INFO] UDID = ' + udid)

    # 1) 生成应用签名证书链。
    #    p12 里现成的 "openharmony application release" 是自签证书（issuer == subject），
    #    与「由 Application CA 签发」的链不匹配，直接拼链会报 11013002 Signature does not match。
    #    用 generate-app-cert 以同一把应用私钥重新签发一张 CA 签名的证书，并让工具输出 certChain。
    ca_cert = os.path.join(OUT_DIR, 'app-ca.cer')
    root_cert = os.path.join(OUT_DIR, 'app-root-ca.cer')
    for alias, path in [('openharmony application ca', ca_cert),
                        ('openharmony application root ca', root_cert)]:
        code, _ = run([KEYTOOL, '-exportcert', '-alias', alias, '-keystore', KEYSTORE,
                       '-storetype', 'PKCS12', '-storepass', STORE_PWD, '-rfc', '-file', path],
                      'exportcert/' + alias)
        if code != 0 or not os.path.exists(path):
            print('[FAIL] CA 证书 %s 导出失败' % alias)
            return 3

    app_cert = os.path.join(OUT_DIR, 'app-release.cer')
    code, out = run([JAVA, '-jar', SIGN_TOOL, 'generate-app-cert',
                     '-keyAlias', APP_ALIAS, '-keyPwd', KEY_PWD,
                     '-issuer', 'C=CN,O=OpenHarmony,OU=OpenHarmony Team,CN=OpenHarmony Application CA',
                     '-issuerKeyAlias', 'openharmony application ca', '-issuerKeyPwd', KEY_PWD,
                     '-subject', 'C=CN,O=OpenHarmony,OU=OpenHarmony Team,CN=SiteDock Debug',
                     '-validity', '365', '-signAlg', SIGN_ALG,
                     '-rootCaCertFile', root_cert, '-subCaCertFile', ca_cert,
                     '-keystoreFile', KEYSTORE, '-keystorePwd', STORE_PWD,
                     '-outForm', 'certChain', '-outFile', app_cert], 'generate-app-cert')
    if code != 0 or not os.path.exists(app_cert):
        print('[FAIL] 应用证书链生成失败')
        return 3
    chain_text = open(app_cert, encoding='utf-8').read()
    leaves = re.findall(r'-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----',
                        chain_text, re.S)
    if not leaves:
        print('[FAIL] 证书链中没有可解析的 PEM 证书')
        return 3
    cert_pem = leaves[0]     # profile 里放 leaf 证书
    print('[INFO] 应用证书链级数 = %d' % len(leaves))

    # 2) 生成 Provision Profile
    tpl = json.load(open(PROFILE_TPL, encoding='utf-8'))
    now = int(time.time())
    tpl['uuid'] = str(uuid.uuid4())
    tpl['validity'] = {'not-before': now - 86400, 'not-after': now + 365 * 86400}
    info = tpl['bundle-info']
    info['bundle-name'] = bundle_name()
    info['development-certificate'] = cert_pem + '\n'
    if udid:
        tpl['debug-info']['device-ids'] = [udid]
    else:
        tpl['debug-info']['device-ids'] = []
    profile_json = os.path.join(OUT_DIR, 'profile-debug.json')
    with open(profile_json, 'w', encoding='utf-8') as f:
        json.dump(tpl, f, ensure_ascii=False, indent=2)
    print('[INFO] bundleName = ' + info['bundle-name'])

    # 3) 签 profile
    profile_p7b = os.path.join(OUT_DIR, 'profile-debug.p7b')
    code, _ = run([JAVA, '-jar', SIGN_TOOL, 'sign-profile',
                   '-mode', 'localSign', '-keyAlias', PROFILE_ALIAS, '-keyPwd', KEY_PWD,
                   '-profileCertFile', PROFILE_CERT, '-inFile', profile_json,
                   '-signAlg', SIGN_ALG, '-keystoreFile', KEYSTORE, '-keystorePwd', STORE_PWD,
                   '-outFile', profile_p7b], 'sign-profile')
    if code != 0 or not os.path.exists(profile_p7b):
        print('[FAIL] profile 签名失败')
        return 4

    # 4) 签 HAP
    code, _ = run([JAVA, '-jar', SIGN_TOOL, 'sign-app',
                   '-mode', 'localSign', '-keyAlias', APP_ALIAS, '-keyPwd', KEY_PWD,
                   '-appCertFile', app_cert, '-profileFile', profile_p7b,
                   '-inFile', UNSIGNED, '-signAlg', SIGN_ALG,
                   '-keystoreFile', KEYSTORE, '-keystorePwd', STORE_PWD,
                   '-outFile', SIGNED, '-signCode', '1'], 'sign-app')
    if code != 0 or not os.path.exists(SIGNED):
        print('[FAIL] HAP 签名失败')
        return 5

    # 5) 校验
    code, out = run([JAVA, '-jar', SIGN_TOOL, 'verify-app',
                     '-inFile', SIGNED,
                     '-outCertChain', os.path.join(OUT_DIR, 'verify-certchain.cer'),
                     '-outProfile', os.path.join(OUT_DIR, 'verify-profile.p7b')],
                    'verify-app')
    print('[OK] 已签名 HAP = %s (%d bytes)' % (SIGNED, os.path.getsize(SIGNED)))
    print('[INFO] verify-app exit=%d' % code)
    print('[NEXT] hdc install -r "%s"' % SIGNED)
    return 0


if __name__ == '__main__':
    sys.exit(main())
