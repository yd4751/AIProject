#!/usr/bin/env node
/**
 * 把腾讯云（或 Let's Encrypt）签发的免费证书装进 nginx，替换 7000~7010 全部 server 块。
 *
 * 背景：deploy/nginx/nginx-full-7000-7010-cors.conf 里有 11 处 ssl_certificate，
 *       手改容易漏。本脚本一次替换完、自动备份、nginx -t 失败自动回滚。
 *
 * 用法（在装有 nginx 的服务器上执行）：
 *   node tools/install-cert.js --cert-dir C:/Users/Data/Downloads/bighat.icu_nginx
 *   node tools/install-cert.js --cert <bundle.crt> --key <bighat.icu.key>
 *   node tools/install-cert.js --cert-dir <目录> --dry-run
 *
 * 常用参数：
 *   --cert-dir DIR    腾讯云下载的 nginx 证书解压目录，自动识别 *_bundle.crt / *.key
 *   --cert FILE       证书链文件（fullchain / bundle），优先于 --cert-dir
 *   --key FILE        私钥文件，优先于 --cert-dir
 *   --nginx-root DIR  nginx 安装根目录，默认 C:/Users/Data/Server/nginx
 *   --conf FILE       主配置文件，默认 <nginx-root>/conf/nginx.conf
 *   --domain NAME     证书目标文件名前缀，默认 bighat.icu
 *   --dry-run         只预览将替换几处、写到哪里，不改动任何文件
 */
'use strict';

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const DEFAULT_NGINX_ROOT = 'C:/Users/Data/Server/nginx';

function usage() {
    console.log([
        '用法: node tools/install-cert.js --cert-dir <解压目录> [选项]',
        '',
        '  --cert-dir DIR    腾讯云 nginx 证书解压目录（自动找 *_bundle.crt 与 *.key）',
        '  --cert FILE       证书链文件（含中间证书），优先于 --cert-dir',
        '  --key FILE        私钥文件，优先于 --cert-dir',
        '  --nginx-root DIR  nginx 根目录，默认 ' + DEFAULT_NGINX_ROOT,
        '  --conf FILE       主配置，默认 <nginx-root>/conf/nginx.conf',
        '  --domain NAME     输出文件名前缀，默认 bighat.icu',
        '  --dry-run         只预览，不写文件',
        '  -h, --help        显示本说明',
    ].join('\n'));
}

function parseArgs(argv) {
    const o = {
        certDir: '', cert: '', key: '', nginxRoot: DEFAULT_NGINX_ROOT,
        conf: '', domain: 'bighat.icu', dryRun: false,
    };
    for (let i = 0; i < argv.length; i++) {
        const a = argv[i];
        if (a === '--dry-run') { o.dryRun = true; continue; }
        if (a === '-h' || a === '--help') { usage(); process.exit(0); }
        if (!a.startsWith('--')) throw new Error('未知参数: ' + a);
        const v = argv[++i];
        if (v === undefined) throw new Error(a + ' 缺少取值');
        const map = {
            '--cert-dir': 'certDir', '--cert': 'cert', '--key': 'key',
            '--nginx-root': 'nginxRoot', '--conf': 'conf', '--domain': 'domain',
        };
        if (!map[a]) throw new Error('未知参数: ' + a);
        o[map[a]] = v;
    }
    return o;
}

function pickFile(dir, patterns, label) {
    const names = fs.readdirSync(dir).filter(n => fs.statSync(path.join(dir, n)).isFile());
    for (const re of patterns) {
        const hit = names.find(n => re.test(n));
        if (hit) return path.join(dir, hit);
    }
    throw new Error(label + '：在 ' + dir + ' 里没找到匹配文件。目录内容: ' + names.join(', '));
}

function resolveInputs(o) {
    let cert = o.cert;
    let key = o.key;
    if (o.certDir) {
        const dir = path.resolve(o.certDir);
        if (!fs.existsSync(dir)) throw new Error('证书目录不存在: ' + dir);
        if (!cert) cert = pickFile(dir, [/_bundle\.crt$/i, /fullchain\.(pem|cer|crt)$/i, /_chain\.crt$/i, /\.cer$/i, /\.crt$/i], '证书链');
        if (!key) key = pickFile(dir, [/\.key$/i], '私钥');
    }
    if (!cert || !key) throw new Error('请提供 --cert-dir，或同时提供 --cert 与 --key');
    cert = path.resolve(cert);
    key = path.resolve(key);
    if (!fs.existsSync(cert)) throw new Error('证书文件不存在: ' + cert);
    if (!fs.existsSync(key)) throw new Error('私钥文件不存在: ' + key);
    const crtText = fs.readFileSync(cert, 'utf8');
    const keyText = fs.readFileSync(key, 'utf8');
    if (!/BEGIN CERTIFICATE/.test(crtText)) throw new Error('证书文件不是 PEM 格式: ' + cert);
    if (!/BEGIN (RSA |EC |)PRIVATE KEY/.test(keyText)) throw new Error('私钥文件不是 PEM 格式: ' + key);
    const chainCount = (crtText.match(/BEGIN CERTIFICATE/g) || []).length;
    return { cert, key, crtText, keyText, chainCount };
}

function inspect(cert) {
    const r = spawnSync('openssl', ['x509', '-in', cert, '-noout', '-subject', '-issuer', '-dates'], { encoding: 'utf8' });
    if (r.error || r.status !== 0) return null;
    const lines = r.stdout.trim().split(/\r?\n/);
    const subject = (lines.find(l => l.startsWith('subject=')) || '').trim();
    const issuer = (lines.find(l => l.startsWith('issuer=')) || '').trim();
    return { lines, selfSigned: !!subject && subject.slice(8) === issuer.slice(7) };
}

function nginxPath(p) {
    return path.resolve(p).replace(/\\/g, '/');
}

function main() {
    const o = parseArgs(process.argv.slice(2));
    const input = resolveInputs(o);
    const root = path.resolve(o.nginxRoot);
    const certDir = path.join(root, 'cert');
    const conf = o.conf ? path.resolve(o.conf) : path.join(root, 'conf', 'nginx.conf');
    const targetCert = nginxPath(path.join(certDir, o.domain + '.fullchain.cer'));
    const targetKey = nginxPath(path.join(certDir, o.domain + '.key'));

    console.log('证书来源 : ' + input.cert + '  (证书链 ' + input.chainCount + ' 张)');
    console.log('私钥来源 : ' + input.key);
    const info = inspect(input.cert);
    if (info) {
        info.lines.forEach(l => console.log('            ' + l));
        if (info.selfSigned) console.log('警告: 这张证书的 subject 与 issuer 相同，看起来仍是自签证书，小程序不会信任！');
    }
    console.log('安装位置 : ' + targetCert);
    console.log('           ' + targetKey);
    console.log('配置文件 : ' + conf);

    if (!fs.existsSync(conf)) throw new Error('找不到 nginx 配置: ' + conf + '\n可用 --conf 指定实际路径');
    const src = fs.readFileSync(conf, 'utf8');
    const crtRe = /^([ \t]*)ssl_certificate[ \t]+[^;\r\n]+;[ \t]*$/gm;
    const keyRe = /^([ \t]*)ssl_certificate_key[ \t]+[^;\r\n]+;[ \t]*$/gm;
    let crtCount = 0;
    let keyCount = 0;
    const out = src
        .replace(crtRe, (m, indent) => { crtCount++; return indent + 'ssl_certificate     ' + targetCert + ';'; })
        .replace(keyRe, (m, indent) => { keyCount++; return indent + 'ssl_certificate_key ' + targetKey + ';'; });
    console.log('将替换   : ssl_certificate ' + crtCount + ' 处，ssl_certificate_key ' + keyCount + ' 处');
    if (crtCount === 0 || keyCount === 0) throw new Error('配置里没找到证书指令，未做任何改动。请确认 --conf 指向的是完整主配置（不是 myconf 片段）');

    if (o.dryRun) {
        console.log('\n[dry-run] 未写入任何文件。');
        return;
    }

    fs.mkdirSync(certDir, { recursive: true });
    const stamp = new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14);
    const backup = conf + '.bak.' + stamp;
    fs.copyFileSync(conf, backup);
    const certExisted = fs.existsSync(targetCert);
    const keyExisted = fs.existsSync(targetKey);
    fs.copyFileSync(input.cert, targetCert);
    fs.copyFileSync(input.key, targetKey);
    fs.writeFileSync(conf, out, 'utf8');
    console.log('已备份   : ' + backup);

    const exe = path.join(root, 'nginx.exe');
    if (fs.existsSync(exe)) {
        const r = spawnSync(exe, ['-t', '-p', root], { cwd: root, encoding: 'utf8' });
        if (r.status !== 0) {
            fs.copyFileSync(backup, conf);
            if (!certExisted) fs.unlinkSync(targetCert);
            if (!keyExisted) fs.unlinkSync(targetKey);
            console.error('nginx -t 校验失败，已回滚配置。输出:\n' + (r.stderr || r.stdout || '').trim());
            process.exit(1);
        }
        console.log('nginx -t : 通过');
    } else {
        console.log('nginx -t : 跳过（本机未找到 ' + exe + '），请在服务器上手动执行校验');
    }

    console.log('\n完成。接下来在 nginx 根目录执行：');
    console.log('  nginx.exe -t -p .');
    console.log('  nginx.exe -s reload');
    console.log('\n自检（换 CA 后应为 Verify return code: 0）：');
    console.log('  openssl s_client -connect ' + o.domain + ':7000 -servername ' + o.domain + ' -showcerts');
}

try {
    main();
} catch (e) {
    console.error('错误: ' + e.message);
    process.exit(1);
}
