// 站点坞 · 抓取代理（零依赖单文件）
//
// 作用：小程序只能请求 https 合法域名，无法直连 http / IP / 非标端口站点。
//       把本服务部署在带 https 域名的服务器上，由它服务端抓取目标站点并回传 HTML。
//
// 启动：
//   PROXY_TOKEN=your-secret PORT=8787 node tools/proxy-server.js
//   再用 Nginx 反代 443 -> 127.0.0.1:8787，并配置可信证书。
//
// 小程序「设置 → 抓取代理」填：
//   https://your-domain.com/fetch
//   令牌填 your-secret（小程序会自动附加 url 与 token 参数）
//
// 接口：GET /fetch?url=<目标URL>&token=<令牌>  → 目标站点的 HTML 文本
//      GET /health                            → 健康检查
//
// 可选环境变量：
//   ALLOW_HOSTS  逗号分隔的域名/IP 白名单，留空表示不限制（建议生产环境配置）

const http = require('http');
const https = require('https');
const { URL } = require('url');

const PORT = Number(process.env.PORT) || 8787;
const TOKEN = process.env.PROXY_TOKEN || '';
const ALLOW_HOSTS = (process.env.ALLOW_HOSTS || '')
    .split(',')
    .map(item => item.trim().toLowerCase())
    .filter(Boolean);
const MAX_BYTES = 2 * 1024 * 1024;
const TIMEOUT = 10000;

const CORS = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type, Accept',
    'Access-Control-Max-Age': '86400',
};

function send(res, status, body, extra) {
    const headers = Object.assign({}, CORS, extra || {});
    res.writeHead(status, headers);
    res.end(body);
}

function isForbiddenHost(hostname) {
    const host = hostname.toLowerCase();
    if (host === 'localhost' || host === '127.0.0.1' || host === '::1' || host === '0.0.0.0') return true;
    if (host.indexOf('169.254.') === 0 || host.indexOf('10.') === 0) return true;
    if (/^192\.168\./.test(host)) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(host)) return true;
    return false;
}

function fetchTarget(targetUrl, redirectLeft, callback) {
    let parsed;
    try {
        parsed = new URL(targetUrl);
    } catch (error) {
        callback(new Error('目标 URL 格式不正确'));
        return;
    }
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        callback(new Error('只支持 http / https 目标'));
        return;
    }
    if (isForbiddenHost(parsed.hostname)) {
        callback(new Error('目标地址属于内网/本机，已被拒绝'));
        return;
    }
    if (ALLOW_HOSTS.length > 0 && !ALLOW_HOSTS.includes(parsed.hostname.toLowerCase())) {
        callback(new Error('目标地址不在 ALLOW_HOSTS 白名单内'));
        return;
    }

    const client = parsed.protocol === 'https:' ? https : http;
    const req = client.request({
        hostname: parsed.hostname,
        port: parsed.port || undefined,
        path: parsed.pathname + parsed.search,
        method: 'GET',
        timeout: TIMEOUT,
        headers: { Accept: 'text/html,application/xhtml+xml', 'Accept-Encoding': 'identity' },
    }, response => {
        const status = response.statusCode;
        if (status >= 300 && status < 400 && response.headers.location && redirectLeft > 0) {
            response.resume();
            const next = new URL(response.headers.location, targetUrl).toString();
            fetchTarget(next, redirectLeft - 1, callback);
            return;
        }
        const chunks = [];
        let size = 0;
        response.on('data', chunk => {
            size += chunk.length;
            if (size > MAX_BYTES) {
                response.destroy();
                callback(new Error('目标页面超过 2MB 限制'));
                return;
            }
            chunks.push(chunk);
        });
        response.on('end', () => {
            if (status >= 400) {
                callback(new Error('目标站点返回 ' + status));
                return;
            }
            callback(null, Buffer.concat(chunks).toString('utf8'), status);
        });
    });
    req.on('timeout', () => { req.destroy(); callback(new Error('目标站点响应超时')); });
    req.on('error', error => callback(new Error('无法连接目标站点：' + (error.code || error.message))));
    req.end();
}

const server = http.createServer((req, res) => {
    let parsedRequest;
    try {
        parsedRequest = new URL(req.url, 'http://localhost');
    } catch (error) {
        send(res, 400, 'bad request', { 'Content-Type': 'text/plain; charset=utf-8' });
        return;
    }

    if (req.method === 'OPTIONS') {
        send(res, 204, '');
        return;
    }
    if (parsedRequest.pathname === '/health') {
        send(res, 200, JSON.stringify({ ok: true, proxy: 'sitedock', tokenRequired: TOKEN !== '' }), {
            'Content-Type': 'application/json; charset=utf-8',
        });
        return;
    }
    if (parsedRequest.pathname !== '/fetch') {
        send(res, 404, 'not found', { 'Content-Type': 'text/plain; charset=utf-8' });
        return;
    }
    if (TOKEN && parsedRequest.searchParams.get('token') !== TOKEN) {
        send(res, 401, 'invalid token', { 'Content-Type': 'text/plain; charset=utf-8' });
        return;
    }
    const targetUrl = parsedRequest.searchParams.get('url');
    if (!targetUrl) {
        send(res, 400, 'missing url', { 'Content-Type': 'text/plain; charset=utf-8' });
        return;
    }

    fetchTarget(targetUrl, 3, (error, html) => {
        if (error) {
            send(res, 502, error.message, { 'Content-Type': 'text/plain; charset=utf-8' });
            return;
        }
        send(res, 200, html, { 'Content-Type': 'text/html; charset=utf-8' });
    });
});

server.listen(PORT, '0.0.0.0', () => {
    console.log('sitedock proxy listening on 0.0.0.0:' + PORT);
    console.log('token required: ' + (TOKEN !== '' ? 'yes' : 'no'));
    console.log('allowed hosts: ' + (ALLOW_HOSTS.length > 0 ? ALLOW_HOSTS.join(', ') : 'any (not recommended in production)'));
});
