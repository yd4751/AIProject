#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""站点坞 HarmonyOS 工程静态自检。

本机尚未安装 DevEco Studio / hvigor，无法真编译；此脚本做工程自洽性兜底校验：
配置文件可解析、资源引用有定义、相对 import 能落到 .ets 文件、
页面与 Ability 入口存在、ArkTS 大括号配平、@Extend 扩展已定义。
"""
import glob
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
fails = []
passes = []


def check(cond, msg):
    (passes if cond else fails).append(msg)


def read(path):
    with open(path, 'r', encoding='utf-8') as f:
        return f.read()


def load_json5(path):
    # 本工程配置文件只使用整行 // 注释，去掉后即为标准 JSON
    lines = [l for l in read(path).splitlines() if not l.strip().startswith('//')]
    return json.loads('\n'.join(lines))


def collect_resources():
    strings, colors, media, profiles = set(), set(), set(), set()
    bases = [
        os.path.join(ROOT, 'AppScope', 'resources', 'base'),
        os.path.join(ROOT, 'entry', 'src', 'main', 'resources', 'base'),
    ]
    for base in bases:
        for sub, bucket, key in [
            ('element/string.json', strings, 'string'),
            ('element/color.json', colors, 'color'),
        ]:
            p = os.path.join(base, *sub.split('/'))
            if os.path.exists(p):
                for item in json.loads(read(p)).get(key, []):
                    bucket.add(item['name'])
        for sub, bucket in [('media', media), ('profile', profiles)]:
            d = os.path.join(base, sub)
            if os.path.isdir(d):
                for f in os.listdir(d):
                    bucket.add(os.path.splitext(f)[0])
    return strings, colors, media, profiles


# ---------------------------------------------------------------- 1. 必需文件
required = [
    'build-profile.json5', 'AppScope/app.json5', 'hvigorfile.ts', 'oh-package.json5',
    'hvigor/hvigor-config.json5', 'entry/build-profile.json5', 'entry/hvigorfile.ts',
    'entry/oh-package.json5', 'entry/src/main/module.json5',
    'entry/src/main/resources/base/profile/main_pages.json',
    'entry/src/main/ets/entryability/EntryAbility.ets',
    'entry/src/main/ets/pages/Index.ets',
    'entry/src/main/ets/model/Site.ets',
    'entry/src/main/ets/model/SiteStore.ets',
    'entry/src/main/ets/model/SiteChecker.ets',
    'entry/src/main/ets/common/Constants.ets',
    'AppScope/resources/base/media/app_icon.png',
    'entry/src/main/resources/base/media/app_icon.png',
]
for rel in required:
    check(os.path.exists(os.path.join(ROOT, *rel.split('/'))), '文件存在: ' + rel)

# ---------------------------------------------------------------- 2. 配置解析
configs = [
    'build-profile.json5', 'AppScope/app.json5', 'oh-package.json5',
    'hvigor/hvigor-config.json5', 'entry/build-profile.json5', 'entry/oh-package.json5',
    'entry/src/main/module.json5',
    'AppScope/resources/base/element/string.json',
    'entry/src/main/resources/base/element/string.json',
    'entry/src/main/resources/base/element/color.json',
    'entry/src/main/resources/base/profile/main_pages.json',
]
parsed = {}
for rel in configs:
    try:
        parsed[rel] = load_json5(os.path.join(ROOT, *rel.split('/')))
        check(True, '解析通过: ' + rel)
    except Exception as e:
        check(False, '解析失败: %s -> %s' % (rel, e))

root_bp = parsed.get('build-profile.json5', {})
product = root_bp.get('app', {}).get('products', [{}])[0]
for field in ('compatibleSdkVersion', 'targetSdkVersion', 'compileSdkVersion'):
    val = str(product.get(field, ''))
    check(bool(re.match(r'^\d+\.\d+\.\d+\(\d+\)$', val)),
          'SDK 字段 %s 格式合法（x.y.z(api)），实得 %s' % (field, val))
check(len({product.get(f) for f in ('compatibleSdkVersion', 'targetSdkVersion', 'compileSdkVersion')}) == 1,
      '三个 SDK 版本字段一致')
check(product.get('runtimeOS') == 'HarmonyOS', "runtimeOS = 'HarmonyOS'")
check(root_bp.get('app', {}).get('signingConfigs') == [],
      'signingConfigs 为空（未签名，需 IDE 自动签名后才可安装）')

mod = parsed.get('entry/src/main/module.json5', {}).get('module', {})
perms = [p.get('name') for p in mod.get('requestPermissions', [])]
check('ohos.permission.INTERNET' in perms, '声明 INTERNET 权限')
check(mod.get('type') == 'entry', 'module.type = entry')
check('phone' in mod.get('deviceTypes', []), 'deviceTypes 含 phone')

# ---------------------------------------------------------------- 3. 资源引用
strings, colors, media, profiles = collect_resources()
all_text = ''
for path in glob.glob(os.path.join(ROOT, '**', '*.*'), recursive=True):
    if os.path.splitext(path)[1].lower() in ('.ets', '.ts', '.json', '.json5'):
        all_text += read(path) + '\n'

buckets = {'string': strings, 'color': colors, 'media': media, 'profile': profiles}
refs = re.findall(r'\$(string|color|media|profile):([\w]+)', all_text)
for kind, name in sorted(set(refs)):
    check(name in buckets[kind], '资源引用 $%s:%s 有定义' % (kind, name))

rrefs = re.findall(r"\$r\('app\.(\w+)\.(\w+)'\)", all_text)
for kind, name in sorted(set(rrefs)):
    check(kind in buckets and name in buckets.get(kind, set()),
          "资源引用 $r('app.%s.%s') 有定义" % (kind, name))

# ---------------------------------------------------------------- 4. import / 入口
ets_files = glob.glob(os.path.join(ROOT, '**', '*.ets'), recursive=True)
bad_imports = 0
for f in ets_files:
    for rel in re.findall(r"from\s+'([\.][^']+)'", read(f)):
        target = os.path.normpath(os.path.join(os.path.dirname(f), rel + '.ets'))
        if not os.path.exists(target):
            bad_imports += 1
            check(False, 'import 找不到: %s -> %s' % (os.path.basename(f), rel))
check(bad_imports == 0, '所有相对 import 均落到存在的 .ets 文件')

pages = parsed.get('entry/src/main/resources/base/profile/main_pages.json', {}).get('src', [])
for page in pages:
    p = os.path.join(ROOT, 'entry', 'src', 'main', 'ets', *page.split('/')) + '.ets'
    check(os.path.exists(p), 'main_pages 页面存在: ' + page)

for ab in mod.get('abilities', []):
    src = ab.get('srcEntry', '')
    p = os.path.join(ROOT, 'entry', 'src', 'main', *src.split('/'))
    check(os.path.exists(p), 'Ability srcEntry 存在: ' + src)

# ---------------------------------------------------------------- 5. ArkTS 结构与关键实现
index_text = read(os.path.join(ROOT, 'entry/src/main/ets/pages/Index.ets'))
check('@Extend(TextInput)' in index_text and '.fieldInput()' in index_text,
      'Index.ets 定义了 @Extend(TextInput) 扩展且被 fieldInput() 使用')
check(index_text.count('{') == index_text.count('}'),
      'Index.ets 大括号配平 (%d/%d)' % (index_text.count('{'), index_text.count('}')))
for token in ['Web({ src: this.currentUrl', '.javaScriptAccess(true)', '.domStorageAccess(true)',
              '.mixedMode(MixedMode.All)', "onBackPress(): boolean", 'accessBackward()',
              '.expandSafeArea(', 'setWebDebuggingAccess']:
    check(token in index_text, 'Index.ets 含关键实现: ' + token)

store_text = read(os.path.join(ROOT, 'entry/src/main/ets/model/SiteStore.ets'))
for token in ['preferences.getPreferences', 'MAX_SITES', 'normalizeUrl', 'seedDefaults']:
    check(token in store_text, 'SiteStore.ets 含: ' + token)

check('http.createHttp' in read(os.path.join(ROOT, 'entry/src/main/ets/model/SiteChecker.ets')),
      'SiteChecker.ets 使用真实 http 请求而非演示值')
check('setWindowLayoutFullScreen(true)' in read(os.path.join(ROOT, 'entry/src/main/ets/entryability/EntryAbility.ets')),
      'EntryAbility 开启沉浸式布局')

# ---------------------------------------------------------------- 结果
for p in passes:
    print('[PASS] ' + p)
for f in fails:
    print('[FAIL] ' + f)
print('[RESULT] %d PASS / %d FAIL' % (len(passes), len(fails)))
sys.exit(1 if fails else 0)
