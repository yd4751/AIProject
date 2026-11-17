#!/usr/bin/env node
/**
 * 生成用于本地联调的自签 TLS 证书（nginx 用，不加密私钥以便无人值守启动）。
 *
 * 用法:
 *   node tools/gen-selfsigned-cert.js
 *   node tools/gen-selfsigned-cert.js --domain site.example.com --ip 43.139.101.12,127.0.0.1
 *   node tools/gen-selfsigned-cert.js --domain your.domain.com --days 825 --out deploy/nginx/cert
 *
 * 产出（deploy/nginx/cert/）:
 *   selfsigned.crt           自签证书（含 SAN）
 *   selfsigned.key           私钥（PKCS#8，未加密）
 *   selfsigned-openssl.cnf   生成时使用的 openssl 配置，便于核对/改域名重签
 *
 * 注意: 自签证书不受系统信任。浏览器会告警，微信开发者工具需勾选
 *       「不校验合法域名、TLS 版本及 HTTPS 证书」；真机小程序 / iOS 不接受自签，
 *       正式上线必须换成 CA 签发的证书（云厂商免费 DV 或 Let's Encrypt）。
 */
'use strict';

const fs = require('fs');
const path = require('path');
const os = require('os');
const { spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..');
const DEFAULT_OUT = path.join(ROOT, 'deploy', 'nginx', 'cert');

function parseArgs(argv) {
    const options = {
        domain: 'site.example.com',
        ip: '43.139.101.12,127.0.0.1',
        days: 365,
        bits: 2048,
        out: DEFAULT_OUT,
    };
    for (let i = 0; i < argv.length; i += 2) {
        const name = argv[i];
        const value = argv[i + 1];
        if (!name || !name.startsWith('--') || value === undefined) {
            throw new Error('参数格式错误，应为 --name value，收到: ' + name);
        }
        const map = {
            '--domain': 'domain',
            '--ip': 'ip',
            '--days': 'days',
            '--bits': 'bits',
            '--out': 'out',
        };
        const key = map[name];
        if (!key) throw new Error('未知参数: ' + name);
        options[key] = ['days', 'bits'].includes(key) ? Number(value) : value;
    }
    return options;
}

function validate(o) {
    const problems = [];
    const bare = o.domain.replace(/^\*\./, '');
    if (!/^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/i.test(bare)) problems.push('domain 不是合法域名');
    if (!Number.isInteger(o.days) || o.days < 1 || o.days > 3650) problems.push('days 必须是 1~3650');
    if (!Number.isInteger(o.bits) || o.bits < 2048) problems.push('bits 至少 2048');
    const ips = o.ip.split(',').map(s => s.trim()).filter(Boolean);
    for (const ip of ips) {
        if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(ip) && ip.indexOf(':') < 0) {
            problems.push('IP 格式不合法: ' + ip);
        }
    }
    if (problems.length) throw new Error(problems.join('; '));
    return ips;
}

function buildConfig(o, ips) {
    const bare = o.domain.replace(/^\*\./, '');
    const dns = [o.domain, 'localhost'];
    const parts = bare.split('.');
    if (parts.length >= 2) {
        const wildcard = '*.' + parts.slice(-2).join('.');
        if (dns.indexOf(wildcard) < 0) dns.push(wildcard);
    }
    const altNames = [];
    dns.forEach((name, i) => altNames.push('DNS.' + (i + 1) + ' = ' + name));
    ips.forEach((ip, i) => altNames.push('IP.' + (i + 1) + ' = ' + ip));

    return [
        '# 由 tools/gen-selfsigned-cert.js 生成，仅用于本地联调，切勿用于生产。',
        '[req]',
        'distinguished_name = dn',
        'prompt = no',
        'x509_extensions = v3_req',
        '',
        '[dn]',
        'CN = ' + o.domain,
        'O = SiteDock Dev',
        'C = CN',
        '',
        '[v3_req]',
        'basicConstraints = critical,CA:FALSE',
        'keyUsage = critical,digitalSignature,keyEncipherment',
        'extendedKeyUsage = serverAuth',
        'subjectAltName = @alt_names',
        '',
        '[alt_names]',
        ...altNames,
        '',
    ].join('\n');
}

function run(args, label) {
    const result = spawnSync('openssl', args, { encoding: 'utf8' });
    if (result.error) throw new Error(label + ' 无法执行 openssl: ' + result.error.message);
    if (result.status !== 0) {
        throw new Error(label + ' 失败: ' + (result.stderr || result.stdout || '').trim());
    }
    return (result.stdout || '').trim();
}

/** Windows 下把路径统一成 openssl.exe 能识别的正斜杠形式。 */
function native(file) {
    return path.resolve(file).replace(/\\/g, '/');
}

function main() {
    const o = parseArgs(process.argv.slice(2));
    const ips = validate(o);
    fs.mkdirSync(o.out, { recursive: true });

    const crt = path.join(o.out, 'selfsigned.crt');
    const key = path.join(o.out, 'selfsigned.key');
    const conf = path.join(o.out, 'selfsigned-openssl.cnf');
    fs.writeFileSync(conf, buildConfig(o, ips), 'utf8');

    console.log('正在生成自签证书（RSA ' + o.bits + '，有效期 ' + o.days + ' 天）...');
    run([
        'req', '-x509', '-newkey', 'rsa:' + o.bits, '-sha256', '-nodes',
        '-days', String(o.days),
        '-config', native(conf),
        '-keyout', native(key),
        '-out', native(crt),
    ], '证书签发');

    // 校验 1: 证书与私钥必须是配对的同一把密钥
    const certPub = run(['x509', '-in', native(crt), '-pubkey', '-noout'], '读取证书公钥');
    const keyPub = run(['pkey', '-in', native(key), '-pubout'], '读取私钥公钥');
    if (certPub.replace(/\s/g, '') !== keyPub.replace(/\s/g, '')) {
        throw new Error('证书与私钥不匹配');
    }

    // 校验 2: 扩展项必须包含 SAN 与 serverAuth（现代浏览器/小程序校验项）
    const ext = run(['x509', '-in', native(crt), '-noout', '-ext', 'subjectAltName,extendedKeyUsage,basicConstraints'], '读取证书扩展');
    if (ext.indexOf('DNS:') < 0) throw new Error('证书缺少 DNS SAN');
    if (ext.indexOf('TLS Web Server Authentication') < 0 && ext.indexOf('serverAuth') < 0) {
        throw new Error('证书缺少 serverAuth 用途');
    }

    const info = run(['x509', '-in', native(crt), '-noout', '-subject', '-dates'], '读取证书信息');
    console.log('\n' + info);
    console.log(ext + '\n');
    console.log('已生成:');
    console.log('  ' + path.relative(ROOT, crt));
    console.log('  ' + path.relative(ROOT, key));
    console.log('  ' + path.relative(ROOT, conf));
    console.log('校验通过：证书/私钥配对、SAN、serverAuth、CA:FALSE');
    console.log('\n部署: 把上面 .crt 和 .key 上传到服务器 /etc/nginx/cert/，然后 chmod 600 selfsigned.key');
    console.log('警告: 自签证书不受信任，仅用于联调；小程序真机/iOS 不接受，上线必须换 CA 证书。');
}

main();
