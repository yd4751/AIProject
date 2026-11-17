#!/usr/bin/env node
/**
 * 生成「https 端口段 -> http 端口段」的 nginx 配置。
 * 默认: https 7000~7010 -> http 6000~6010（同序号偏移 -1000）。
 *
 * 两种布局:
 *   split      (默认)  ports-*.conf + sitedock-common.conf，含 http 级 map，
 *                      适合 Linux，两个文件分别放 conf.d/ 与 snippets/。
 *   standalone          单文件，不含 map、不含 include，只有 server 块与内联指令。
 *                      适合配置被 include 到 http{} 内的任意目录，也适配 Windows。
 *   full                完整主配置（worker_processes / events / http / 11 个 server），
 *                      可直接用 nginx -c 启动，不依赖 nginx.conf 的 include 位置。
 *
 * 用法:
 *   node tools/gen-nginx-conf.js
 *   node tools/gen-nginx-conf.js --https-start 7100 --https-end 7105 --http-start 6100
 *   node tools/gen-nginx-conf.js --cert-preset selfsigned
 *   node tools/gen-nginx-conf.js --cert-preset selfsigned --layout standalone --platform windows
 *   node tools/gen-nginx-conf.js --cert-preset selfsigned --layout full --platform windows
 *   node tools/gen-nginx-conf.js --layout standalone --platform windows --cert-dir C:/nginx/cert
 *   node tools/gen-nginx-conf.js --cert-preset selfsigned --layout full --platform windows --domain bighat.icu --cors true
 *   node tools/gen-nginx-conf.js --cert /path/fullchain.pem --key /path/privkey.pem
 *
 * 关键约束: 无论哪种布局，配置文件都必须被 include 在 nginx.conf 的 http{} 块内。
 *           include 写在 server{} 内或 http{} 外都会报 "xxx directive is not allowed here"。
 */
'use strict';

const fs = require('fs');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');
const OUT_DIR = path.join(ROOT, 'deploy', 'nginx');

function parseArgs(argv) {
    const options = {
        httpsStart: 7000,
        httpsEnd: 7010,
        httpStart: 6000,
        domain: 'site.example.com',
        backend: '127.0.0.1',
        cert: '',
        key: '',
        certPreset: 'letsencrypt',
        platform: 'linux',
        layout: 'split',
        certDir: '',
        includePath: '',
        cors: false,
        out: OUT_DIR,
    };
    for (let i = 0; i < argv.length; i += 2) {
        const name = argv[i];
        const value = argv[i + 1];
        if (!name || !name.startsWith('--') || value === undefined) {
            throw new Error('参数格式错误，应为 --name value，收到: ' + name);
        }
        const map = {
            '--https-start': 'httpsStart',
            '--https-end': 'httpsEnd',
            '--http-start': 'httpStart',
            '--domain': 'domain',
            '--backend': 'backend',
            '--cert': 'cert',
            '--key': 'key',
            '--cert-preset': 'certPreset',
            '--platform': 'platform',
            '--layout': 'layout',
            '--cert-dir': 'certDir',
            '--include-path': 'includePath',
            '--cors': 'cors',
            '--out': 'out',
        };
        const key = map[name];
        if (!key) throw new Error('未知参数: ' + name);
        if (key === 'cors') {
            options.cors = value === 'true' || value === '1' || value === 'yes';
        } else {
            options[key] = ['httpsStart', 'httpsEnd', 'httpStart'].includes(key) ? Number(value) : value;
        }
    }
    return options;
}

/**
 * Windows Git Bash / MSYS 会把 /etc/... 这类 POSIX 绝对路径自动转换成
 * Git 安装目录下的 Windows 路径，直接写进 nginx 配置会失效。
 * 仅 Linux 平台需要还原；Windows 平台本来就该用盘符路径。
 */
function normalizeServerPath(value, label) {
    if (!value) return value;
    const unixStyle = value.replace(/\\/g, '/');
    if (!/^[A-Za-z]:/.test(unixStyle)) return value;
    const marker = /\/(?:etc|opt|usr|var|home|srv|root)\//.exec(unixStyle);
    if (marker) {
        const restored = unixStyle.slice(marker.index);
        console.warn('[提示] ' + label + ' 被 Git Bash 做了路径转换，已还原为 ' + restored);
        return restored;
    }
    console.warn('[警告] ' + label + ' 是 Windows 路径，Linux nginx 需要服务器上的 POSIX 路径。');
    console.warn('        Git Bash 中可用 MSYS_NO_PATHCONV=1 前缀避免转换。');
    return value;
}

function validateOptions(o) {
    const problems = [];
    for (const key of ['httpsStart', 'httpsEnd', 'httpStart']) {
        if (!Number.isInteger(o[key]) || o[key] < 1 || o[key] > 65535) {
            problems.push(key + ' 必须是 1~65535 的整数');
        }
    }
    if (o.httpsEnd < o.httpsStart) problems.push('httpsEnd 不能小于 httpsStart');
    const count = o.httpsEnd - o.httpsStart + 1;
    const httpEnd = o.httpStart + count - 1;
    if (httpEnd > 65535) problems.push('http 端口段超出 65535');
    if (o.httpsStart <= httpEnd && o.httpStart <= o.httpsEnd) {
        problems.push('https 端口段与 http 端口段重叠，会造成代理自环');
    }
    if (!/^[a-z0-9.-]+$/i.test(o.domain)) problems.push('domain 不是合法域名');
    if (!o.backend) problems.push('backend 不能为空');
    if (!!o.cert !== !!o.key) problems.push('--cert 与 --key 必须同时提供');
    if (['letsencrypt', 'selfsigned'].indexOf(o.certPreset) < 0) {
        problems.push('--cert-preset 只能是 letsencrypt 或 selfsigned');
    }
    if (['linux', 'windows'].indexOf(o.platform) < 0) problems.push('--platform 只能是 linux 或 windows');
    if (['split', 'standalone', 'full'].indexOf(o.layout) < 0) {
        problems.push('--layout 只能是 split、standalone 或 full');
    }
    if (problems.length) throw new Error(problems.join('; '));
    return count;
}

function certDirOf(o) {
    if (o.certDir) return o.certDir.replace(/\\/g, '/').replace(/\/+$/, '');
    return o.platform === 'windows' ? 'C:/Users/Data/Server/nginx/cert' : '/etc/nginx/cert';
}

function resolveCertPaths(o, preset) {
    if (preset === 'custom') return [o.cert, o.key];
    if (o.platform === 'windows') {
        const dir = certDirOf(o);
        if (preset === 'selfsigned') return [dir + '/selfsigned.crt', dir + '/selfsigned.key'];
        return [dir + '/' + o.domain + '_bundle.crt', dir + '/' + o.domain + '.key'];
    }
    if (preset === 'selfsigned') {
        return ['/etc/nginx/cert/selfsigned.crt', '/etc/nginx/cert/selfsigned.key'];
    }
    return [
        '/etc/letsencrypt/live/' + o.domain + '/fullchain.pem',
        '/etc/letsencrypt/live/' + o.domain + '/privkey.pem',
    ];
}

function certNotes(o, preset) {
    if (preset === 'custom') {
        return ['# 使用自定义证书路径。确认证书链完整、私钥权限收紧、域名与 server_name 一致。'];
    }
    if (preset === 'selfsigned') {
        return [
            '#',
            '# >>> 当前为自签证书（本地联调模式）<<<',
            '# 证书由 tools/gen-selfsigned-cert.js 生成，源文件在 deploy/nginx/cert/。',
            '# 把 selfsigned.crt / selfsigned.key 放到下面 ssl_certificate 指向的目录。',
            '# 自签证书不受系统信任：浏览器会告警，微信开发者工具需勾选',
            '# 「不校验合法域名、TLS 版本及 HTTPS 证书」；真机小程序 / iOS 不接受自签。',
            '# 正式上线必须换成 CA 签发证书：改用 --cert-preset letsencrypt（占位待填）',
            '# 或 --cert /path/fullchain.pem --key /path/privkey.pem。',
        ];
    }
    return [
        '# TODO: 以下是占位路径，文件尚不存在，直接部署 nginx -t 会失败。',
        '#       请先申请 CA 可信证书（云厂商免费 DV 或 certbot），证书链要完整',
        '#       （用 fullchain.pem / bundle.crt），证书域名需与 server_name 一致。',
        '#       只想本地联调可改用 --cert-preset selfsigned。',
    ];
}

function tlsDirectives(cert, key) {
    return [
        'ssl_certificate     ' + cert + ';',
        'ssl_certificate_key ' + key + ';',
        'ssl_protocols       TLSv1.2 TLSv1.3;',
        'ssl_prefer_server_ciphers off;',
    ];
}

function corsDirectives() {
    return [
        'add_header Access-Control-Allow-Origin   * always;',
        "add_header Access-Control-Allow-Methods  'GET, HEAD, POST, OPTIONS' always;",
        'add_header Access-Control-Allow-Headers  * always;',
        'add_header Access-Control-Max-Age       86400 always;',
    ];
}

/**
 * location 内的 OPTIONS 预检短路。
 * 浏览器跨域在带自定义头时先发 OPTIONS；若透传给后端，Vite 等通常返回 405，
 * 预检失败后真正的 GET 根本不会发出。这里直接 204 + 完整 CORS 头。
 * nginx 的 add_header 继承规则是「本级有 add_header 就不继承父级」，
 * 所以 if 块里必须把四个头写全，不能直接复用 server 级配置。
 */
function corsLocationDirectives() {
    return [
        '        # 浏览器跨域预检：直接 204，不转发给后端（后端通常返回 405）。',
        '        if ($request_method = OPTIONS) {',
        '            add_header Access-Control-Allow-Origin   * always;',
        "            add_header Access-Control-Allow-Methods  'GET, HEAD, POST, OPTIONS' always;",
        '            add_header Access-Control-Allow-Headers  * always;',
        '            add_header Access-Control-Max-Age       86400 always;',
        '            return 204;',
        '        }',
        '',
    ];
}

/**
 * 公共反代指令。standalone 模式没有 http 级 map，用固定 "upgrade" 值；
 * split 模式引用主配置顶部 map 定义的 $sitedock_conn_upgrade。
 */
function proxyDirectives(standalone) {
    const lines = [
        'error_page 497 https://$host:$server_port$request_uri;',
        '',
        'proxy_http_version 1.1;',
        'proxy_set_header Host              $http_host;',
        'proxy_set_header X-Real-IP         $remote_addr;',
        'proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;',
        'proxy_set_header X-Forwarded-Proto $scheme;',
        'proxy_set_header X-Forwarded-Port  $server_port;',
    ];
    if (standalone) {
        lines.push(
            '# 无 map 的 WebSocket 写法：普通请求也会带上 Connection: upgrade，',
            '# 绝大多数后端会忽略；若后端对此敏感，注释掉下面两行（仅影响 HMR/WebSocket）。',
            'proxy_set_header Upgrade           $http_upgrade;',
            'proxy_set_header Connection        "upgrade";'
        );
    } else {
        lines.push(
            'proxy_set_header Upgrade           $http_upgrade;',
            'proxy_set_header Connection        $sitedock_conn_upgrade;'
        );
    }
    lines.push(
        '',
        'proxy_connect_timeout 10s;',
        'proxy_send_timeout    60s;',
        'proxy_read_timeout    60s;'
    );
    return lines;
}

function indent(lines) {
    return lines.map(line => (line ? '    ' + line : ''));
}

/** split 布局：公共片段，被每个 server include。 */
function buildCommon(o, preset) {
    const certAndKey = resolveCertPaths(o, preset);
    return [
        '# ============================================================',
        '# 站点坞 · 公共 TLS / 反向代理片段（split 布局）',
        '# 由 tools/gen-nginx-conf.js 生成；修改端口范围请改脚本后重跑。',
        '#',
        '# 部署: 放到 nginx 的 snippets/ 目录（或 --include-path 指定的位置）',
        '# ============================================================',
        '',
        '# 证书只校验域名、不校验端口，一份证书覆盖全部 ' + o.httpsStart + '~' + o.httpsEnd + ' 端口。',
        ...certNotes(o, preset),
        ...tlsDirectives(certAndKey[0], certAndKey[1]),
        '# 调试期不要加 HSTS（浏览器会长期缓存强制 https，改配置时很难回退）。',
        '',
        ...proxyDirectives(false),
        ...(o.cors ? [
            '# 调试期跨域：WorkBuddy 预览面板等浏览器环境读取页面内容需要这些响应头。',
            '# 正式上线且确认无需浏览器跨域访问后，可去掉本段重新生成。',
            ...corsDirectives(),
        ] : []),
        '# 后端若是 SSE / 流式输出，取消下行注释:',
        '# proxy_buffering off;',
        '',
    ].join('\n');
}

function deployHeader(o, count, extra) {
    return [
        '# ============================================================',
        '# 站点坞 · https ' + o.httpsStart + '~' + o.httpsEnd + ' -> http ' + o.httpStart + '~' + (o.httpStart + count - 1),
        '# 由 tools/gen-nginx-conf.js 生成，共 ' + count + ' 组映射（偏移 ' + (o.httpStart - o.httpsStart) + '）。',
        '# 平台: ' + o.platform + ' / 布局: ' + (o.layout === 'standalone' ? 'standalone（单文件，无 map、无 include）' : 'split（含 http 级 map）'),
        ...extra,
        '# ============================================================',
        '',
    ];
}

/** split 布局：主配置含 http 级 map + 若干 server（引用公共片段）。 */
function buildMain(o, count) {
    const includePath = o.includePath || (o.platform === 'windows'
        ? 'myconf/sitedock-common.conf'
        : 'snippets/sitedock-common.conf');
    const lines = deployHeader(o, count, [
        '#',
        '# 【重要】map 是 http{} 级指令，本文件必须被 include 在 nginx.conf 的 http{} 块内。',
        '#        若 include 写在 server{} 内或 http{} 外，会报 "map directive is not allowed here"。',
        '#        不适合这个上下文时，改用 --layout standalone（不含 map）。',
        '#',
        '# 部署:',
        '#   1) 本文件放到 conf.d/（Linux）或 myconf/（Windows）',
        '#   2) sitedock-common.conf 放到 ' + includePath,
        '#   3) 证书文件按片段顶部说明放到服务器对应路径',
        '#   4) 把下面所有 server_name 改成你的域名（需与证书 SAN 匹配）',
        '#   5) nginx -t && nginx -s reload',
        '#   6) 防火墙放行 TCP ' + o.httpsStart + '~' + o.httpsEnd + '，但不要放行 ' + o.httpStart + '~' + (o.httpStart + count - 1),
    ]);
    lines.push(
        'map $http_upgrade $sitedock_conn_upgrade {',
        '    default upgrade;',
        "    ''      close;",
        '}',
        ''
    );
    for (let i = 0; i < count; i++) {
        const httpsPort = o.httpsStart + i;
        const httpPort = o.httpStart + i;
        const upstream = 'http://' + o.backend + ':' + httpPort;
        lines.push(
            '# ------- ' + httpsPort + ' -> ' + httpPort + ' -------',
            'server {',
            '    listen      ' + httpsPort + ' ssl;',
            '    server_name ' + o.domain + ';',
            '',
            '    include ' + includePath + ';',
            '',
            '    location / {',
            ...(o.cors ? corsLocationDirectives() : []),
            '        proxy_pass ' + upstream + ';',
            '        proxy_redirect ' + upstream + '/ https://$host:' + httpsPort + '/;',
            '    }',
            '}',
            ''
        );
    }
    return lines.join('\n');
}

/** standalone 布局：全部指令内联进 server，文件里只有 server{} 块。 */
function buildStandalone(o, count, preset) {
    const certAndKey = resolveCertPaths(o, preset);
    const lines = deployHeader(o, count, [
        '#',
        '# 【重要】本文件只有 server{} 块，必须被 include 在 nginx.conf 的 http{} 块内:',
        '#',
        '#   http {',
        '#       ...',
        '#       include       mime.types;',
        '#       include       myconf/*.conf;   <-- 放在这里，不能放进 server{} 内',
        '#       ...',
        '#   }',
        '#',
        '# 【切勿】用 nginx -c 直接指向本文件，或把本文件当主配置启动：',
        '#   脱离 http{} 后 server{} 必定报 "server directive is not allowed here"。',
        '#   若反复报这个错，改用同目录 nginx-full-*.conf 完整主配置（或 fix-nginx.cmd）。',
        '#',
        '# 若原来把 include 写在某个 server{} 里，请先删掉那行，再放到 http{} 内。',
        '# 本文件不含 map，也不会引用 snippets/ 等外部片段，避免路径与上下文问题。',
        '#',
        '# 部署:',
        '#   1) 本文件单独放到 myconf/（该目录里不要同时放 split 版或 sitedock-common.conf，',
        '#      否则通配符 include 会加载两份 server 造成冲突）',
        '#   2) 证书放到 ' + certDirOf(o) + '/ 目录',
        '#   3) 把下面所有 server_name 改成你的域名（需与证书 SAN 匹配）',
        '#   4) nginx -t，通过后 nginx -s reload',
        '#   5) 防火墙放行 TCP ' + o.httpsStart + '~' + o.httpsEnd + '，但不要放行 ' + o.httpStart + '~' + (o.httpStart + count - 1),
        '#',
        '# 证书只校验域名、不校验端口，一份证书覆盖全部 ' + o.httpsStart + '~' + o.httpsEnd + ' 端口。',
        ...certNotes(o, preset),
    ]);
    for (let i = 0; i < count; i++) {
        const httpsPort = o.httpsStart + i;
        const httpPort = o.httpStart + i;
        const upstream = 'http://' + o.backend + ':' + httpPort;
        lines.push(
            '# ------- ' + httpsPort + ' -> ' + httpPort + ' -------',
            'server {',
            '    listen      ' + httpsPort + ' ssl;',
            '    server_name ' + o.domain + ';',
            '',
            ...indent(tlsDirectives(certAndKey[0], certAndKey[1])),
            '    # 调试期不要加 HSTS；nginx 1.25.1+ 如需 HTTP/2 可在此加一行 http2 on;',
            '',
            ...indent(proxyDirectives(true)),
            ...(o.cors ? [
                '    # 调试期跨域：浏览器环境读取页面内容需要这些响应头。',
                ...indent(corsDirectives()),
            ] : []),
            '',
            '    location / {',
            ...(o.cors ? corsLocationDirectives() : []),
            '        proxy_pass ' + upstream + ';',
            '        proxy_redirect ' + upstream + '/ https://$host:' + httpsPort + '/;',
            '    }',
            '}',
            ''
        );
    }
    return lines.join('\n');
}

/** 计算产物文件名，供生成逻辑与配置内注释共用，避免注释里的示例名和实际文件对不上。 */
function outputFileName(o) {
    let name;
    if (o.layout === 'full') {
        name = 'nginx-full-' + o.httpsStart + '-' + o.httpsEnd + '.conf';
    } else {
        const suffix = o.layout === 'standalone' ? (o.platform === 'windows' ? '-windows' : '-standalone') : '';
        name = 'ports-' + o.httpsStart + '-' + o.httpsEnd + suffix + '.conf';
    }
    return o.cors ? name.replace(/\.conf$/, '-cors.conf') : name;
}

/**
 * full 布局：完整 nginx 主配置，自带 events{} 与 http{}，
 * 可直接用 nginx -c <file> -p <prefix> 校验/启动，不依赖 nginx.conf 的 include 位置。
 */
function buildFull(o, count, preset) {
    const standalone = buildStandalone(o, count, preset);
    const at = standalone.indexOf('# ------- ');
    if (at < 0) throw new Error('无法从 standalone 布局提取 server 块');
    const blocks = standalone.slice(at).split('\n');
    const certAndKey = resolveCertPaths(o, preset);
    const root = o.platform === 'windows' ? 'C:/Users/Data/Server/nginx' : '/etc/nginx';
    const fullName = outputFileName(o);
    return [
        '# ============================================================',
        '# 站点坞 · 完整 nginx 主配置（full 布局，可直接 nginx -c 启动）',
        '# 由 tools/gen-nginx-conf.js 生成，共 ' + count + ' 组映射。',
        '#',
        '# 【这是完整主配置，不是片段】不要再被别的 nginx.conf include，',
        '# 也不要和 myconf/*.conf 同时加载，否则 server 会重复。',
        '#',
        '# 把本文件放到 nginx 根目录（不要放 myconf，避免被通配符 include）:',
        '#   ' + root + '/' + fullName,
        '#',
        '# 方案 A（推荐，零判断）：备份后用本文件替换 conf/nginx.conf，再正常启动。',
        '#   同目录的 fix-nginx.cmd 可一键完成「备份 + 替换 + 校验 + 失败回滚」。',
        '#   替换后主配置不含 include myconf，myconf 里的旧片段不会再被加载。',
        '#',
        '# 方案 B（独立实例，不改原配置）:',
        '#   nginx.exe -t -c ' + root + '/' + fullName + ' -p ' + root,
        '#   nginx.exe    -c ' + root + '/' + fullName + ' -p ' + root,
        '#   启动独立实例前请先停掉原实例（nginx.exe -s stop），否则端口冲突。',
        '#',
        '# 【切勿】对 myconf/ 里的 ports-*.conf 片段直接执行 nginx -c：',
        '#   片段只有 server{}，没有 http{} 包裹，必定报',
        '#   "server directive is not allowed here"。',
        '#',
        '# Windows 版 nginx 不支持 user 指令，这里刻意不写；',
        '# 也不 include mime.types（Windows 下相对路径有歧义，纯反代不需要它）。',
        '# pid / error_log 单独命名，便于与原实例共存。',
        '# 证书: ' + certAndKey[0],
        '# 私钥: ' + certAndKey[1],
        ...certNotes(o, preset),
        '# ============================================================',
        '',
        'worker_processes  1;',
        'pid               logs/sitedock.pid;',
        'error_log          logs/sitedock-error.log notice;',
        '',
        'events {',
        '    worker_connections  1024;',
        '}',
        '',
        'http {',
        '    default_type  application/octet-stream;',
        '    sendfile      on;',
        '    keepalive_timeout  65;',
        '',
        ...indent(blocks),
        '}',
        '',
    ].join('\n');
}

/** 静态自检：括号、端口映射、指令完整性、上下文相关约束、编码。 */
function selfCheck(config, common, o, count, preset, layout) {
    const problems = [];
    const codeLines = config.split('\n').filter(line => !/^\s*#/.test(line)).join('\n');
    const opens = (codeLines.match(/{/g) || []).length;
    const closes = (codeLines.match(/}/g) || []).length;
    if (opens !== closes) problems.push('主配置花括号不配对: ' + opens + ' vs ' + closes);

    const servers = (config.match(/^[ \t]*server \{/gm) || []).length;
    if (servers !== count) problems.push('server 数量不符: ' + servers + '，应为 ' + count);
    const listens = (config.match(/^[ \t]*listen\s+\d+ ssl;/gm) || []).length;
    if (listens !== count) problems.push('listen 数量不符: ' + listens);
    const passes = (config.match(/proxy_pass http:\/\//g) || []).length;
    if (passes !== count) problems.push('proxy_pass 数量不符: ' + passes);
    for (let i = 0; i < count; i++) {
        const httpsPort = o.httpsStart + i;
        const httpPort = o.httpStart + i;
        if (config.indexOf('proxy_pass http://' + o.backend + ':' + httpPort + ';') < 0) {
            problems.push('缺少 ' + httpsPort + ' -> ' + httpPort + ' 映射');
        }
        if (config.indexOf('listen      ' + httpsPort + ' ssl;') < 0) problems.push('缺少 listen ' + httpsPort);
    }

    if (layout === 'split') {
        if (!/map \$http_upgrade \$sitedock_conn_upgrade/.test(config)) problems.push('缺少 sitedock_conn_upgrade 的 map 定义');
        if (common.indexOf('$sitedock_conn_upgrade') < 0) problems.push('公共片段未引用 sitedock_conn_upgrade');
        if (config.indexOf('include ') < 0) problems.push('split 布局缺少 include');
        if (common.indexOf('ssl_protocols       TLSv1.2 TLSv1.3;') < 0) problems.push('公共片段 TLS 版本不符合要求');
    } else if (layout === 'standalone') {
        // 不含任何 http 级指令、不含 include，且必须提示 include 位置
        if (/^[ \t]*map\s/m.test(config)) problems.push('standalone 不应包含 map（会触发 not allowed here）');
        if (/^[ \t]*include\s/m.test(config)) problems.push('standalone 不应包含 include');
        if (config.indexOf('http{}') < 0) problems.push('缺少 http{} include 位置说明');
        if (config.indexOf('server directive is not allowed here') < 0) problems.push('缺少 -c 误用提示');
        if (config.indexOf('ssl_protocols       TLSv1.2 TLSv1.3;') < 0) problems.push('TLS 版本不符合小程序要求');
        if (preset === 'selfsigned' && config.indexOf('不接受自签') < 0) problems.push('缺少自签证书风险提示');
    } else {
        // full：自带 events/http，除 mime.types 外不能引入其他 include
        if (config.indexOf('worker_processes  1;') < 0) problems.push('full 布局缺少 worker_processes');
        if (config.indexOf('events {') < 0) problems.push('full 布局缺少 events 块');
        if (config.indexOf('pid               logs/sitedock.pid;') < 0) problems.push('full 布局缺少独立 pid 文件');
        if (config.indexOf('server directive is not allowed here') < 0) problems.push('缺少 -c 误用提示');
        if (!/^[ \t]*http \{/m.test(config)) problems.push('full 布局缺少 http 块');
        if (/^[ \t]*map\s/m.test(config)) problems.push('full 布局不应包含 map');
        const extraIncludes = config.split('\n').filter(line => {
            const trimmed = line.trim();
            return trimmed.startsWith('include ') && trimmed.indexOf('mime.types') < 0;
        });
        if (extraIncludes.length) problems.push('full 布局不应包含额外 include: ' + extraIncludes.length + ' 处');
        if (config.indexOf('ssl_protocols       TLSv1.2 TLSv1.3;') < 0) problems.push('TLS 版本不符合小程序要求');
        if (preset === 'selfsigned' && config.indexOf('不接受自签') < 0) problems.push('缺少自签证书风险提示');
    }

    const corsText = layout === 'split' ? config + '\n' + common : config;
    if (o.cors) {
        if (corsText.indexOf('Access-Control-Allow-Origin') < 0) problems.push('已开启 CORS 但配置里缺少跨域响应头');
        if (corsText.indexOf('Access-Control-Allow-Methods') < 0) problems.push('缺少 Allow-Methods 响应头');
        if (corsText.indexOf('return 204;') < 0) problems.push('已开启 CORS 但缺少 OPTIONS 预检短路');
        const optionBlocks = (corsText.match(/if \(\$request_method = OPTIONS\) \{/g) || []).length;
        if (optionBlocks !== count) problems.push('OPTIONS 预检块数量不符: ' + optionBlocks + '，应为 ' + count);
    }
    if (!o.cors && /^[ \t]*add_header\s+Access-Control-Allow-Origin/m.test(corsText)) {
        problems.push('未开启 CORS 却包含跨域响应头');
    }

    const certAndKey = resolveCertPaths(o, preset);
    const target = layout === 'split' ? common : config;
    if (target.indexOf(certAndKey[0]) < 0) problems.push('证书路径未写入配置');
    if (target.indexOf(certAndKey[1]) < 0) problems.push('私钥路径未写入配置');
    if (o.platform === 'linux' && /ssl_certificate(?:_key)?\s+[A-Za-z]:[\\/]/.test(target)) {
        problems.push('Linux 平台证书路径不应是 Windows 盘符路径');
    }
    if (config.charCodeAt(0) === 0xfeff) problems.push('主配置带 BOM，nginx 可能解析失败');
    if (common && common.charCodeAt(0) === 0xfeff) problems.push('公共片段带 BOM');

    // 每个非注释指令行必须以 ; 或 { 结束（或单独一个 }），可抓出漏分号
    for (const [name, text] of [['主配置', config], ['公共片段', common]]) {
        if (!text) continue;
        text.split('\n').forEach((raw, index) => {
            const line = raw.trim();
            if (!line || line.startsWith('#') || line === '}') return;
            if (!/[;{]$/.test(line)) problems.push(name + ' 第 ' + (index + 1) + ' 行指令未结束: ' + line);
        });
    }
    return problems;
}

function main() {
    const o = parseArgs(process.argv.slice(2));
    if (o.platform === 'linux') {
        o.cert = normalizeServerPath(o.cert, '--cert');
        o.key = normalizeServerPath(o.key, '--key');
    }
    const count = validateOptions(o);
    const preset = (o.cert && o.key) ? 'custom' : o.certPreset;
    if (preset === 'selfsigned') {
        const local = path.join(ROOT, 'deploy', 'nginx', 'cert');
        if (!fs.existsSync(path.join(local, 'selfsigned.crt')) || !fs.existsSync(path.join(local, 'selfsigned.key'))) {
            console.warn('[提示] 本地未找到自签证书，部署前请先执行: node tools/gen-selfsigned-cert.js');
        }
    }
    let config;
    let common = '';
    if (o.layout === 'split') {
        config = buildMain(o, count);
        common = buildCommon(o, preset);
    } else if (o.layout === 'standalone') {
        config = buildStandalone(o, count, preset);
    } else {
        config = buildFull(o, count, preset);
    }
    const problems = selfCheck(config, common, o, count, preset, o.layout);
    if (problems.length) {
        console.error('自检失败:');
        for (const p of problems) console.error(' - ' + p);
        process.exit(1);
    }
    fs.mkdirSync(o.out, { recursive: true });
    const mainPath = path.join(o.out, outputFileName(o));
    fs.writeFileSync(mainPath, config, 'utf8');
    console.log('已生成:');
    console.log('  ' + path.relative(ROOT, mainPath) + '  (' + count + ' 组映射)');
    if (o.layout === 'split') {
        const commonPath = path.join(o.out, 'sitedock-common.conf');
        fs.writeFileSync(commonPath, common, 'utf8');
        console.log('  ' + path.relative(ROOT, commonPath));
    }
    console.log('映射: https ' + o.httpsStart + '~' + o.httpsEnd + ' -> http ' + o.httpStart + '~' + (o.httpStart + count - 1));
    console.log('平台: ' + o.platform + ' / 布局: ' + o.layout + ' / 证书模式: ' + preset + ' / 跨域: ' + (o.cors ? '开' : '关'));
    if (o.layout === 'standalone') console.log('注意: 本文件不含 map/include，但仍必须被 include 在 nginx.conf 的 http{} 块内');
    if (o.layout === 'full') console.log('注意: 这是完整主配置，用 nginx -c 指定，不要再被其他 nginx.conf include');
    console.log('静态自检通过（括号 / 端口配对 / 上下文约束 / TLS / 证书路径 / 指令完整性 / 编码）');
}

main();
