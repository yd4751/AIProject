// 站点坞 · 预览抓取代理（零依赖，单端口 HTTP 服务）
//
// 用途：WorkBuddy 预览面板是浏览器环境，直连自签证书的 https://bighat.icu:7000
//       会在 TLS 握手阶段被拒。本服务部署在带可信证书的平台域名下，由服务端
//       抓取目标站点并回传 HTML，小程序只请求这个可信域名。
//
// 启动：PORT=3000 node server.js
// 接口：GET /fetch?url=<目标URL>   -> 目标站点 HTML 文本
//       GET /health                -> 健康检查
//
// 安全：默认白名单只允许 bighat.icu（可用 ALLOW_HOSTS 追加），
//       拒绝内网地址、限制 2MB / 10s / 3 次重定向。

const http = require('http');
const https = require('https');
const { URL } = require('url');

const PORT = Number(process.env.PORT) || 3000;
const DEFAULT_HOSTS = ['bighat.icu'];
const ALLOW_HOSTS = DEFAULT_HOSTS.concat(
    (process.env.ALLOW_HOSTS || '').split(',').map(s => s.trim().toLowerCase()).filter(Boolean)
);
const MAX_BYTES = 2 * 1024 * 1024;
const TIMEOUT = 10000;

const CORS = {
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET, OPTIONS',
    'Access-Control-Allow-Headers': 'Accept, Content-Type',
    'Access-Control-Max-Age': '86400',
};

function send(res, status, body, extra) {
    res.writeHead(status, Object.assign({}, CORS, extra || {}));
    res.end(body);
}

function isPrivateHost(hostname) {
    const h = hostname.toLowerCase();
    if (h === 'localhost' || h === '127.0.0.1' || h === '::1' || h === '0.0.0.0') return true;
    if (h.indexOf('169.254.') === 0 || h.indexOf('10.') === 0) return true;
    if (/^192\.168\./.test(h)) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(h)) return true;
    return false;
}

function isAllowedHost(hostname) {
    const h = hostname.toLowerCase();
    return ALLOW_HOSTS.some(allowed => h === allowed || h.endsWith('.' + allowed));
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
    if (isPrivateHost(parsed.hostname) || !isAllowedHost(parsed.hostname)) {
        callback(new Error('目标地址不在白名单内：' + parsed.hostname));
        return;
    }

    const client = parsed.protocol === 'https:' ? https : http;
    const options = {
        hostname: parsed.hostname,
        port: parsed.port || undefined,
        path: parsed.pathname + parsed.search,
        method: 'GET',
        timeout: TIMEOUT,
        headers: { Accept: 'text/html,application/xhtml+xml', 'Accept-Encoding': 'identity' },
    };
    // 白名单内的调试站点当前使用自签证书；服务端抓取时跳过证书校验。
    // 正式环境换成 CA 证书后可把这一行删掉。
    if (parsed.protocol === 'https:') options.rejectUnauthorized = false;

    const req = client.request(options, response => {
        const status = response.statusCode;
        if (status >= 300 && status < 400 && response.headers.location && redirectLeft > 0) {
            response.resume();
            let next;
            try {
                next = new URL(response.headers.location, targetUrl).toString();
            } catch (error) {
                callback(new Error('重定向地址无效'));
                return;
            }
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
            callback(null, Buffer.concat(chunks).toString('utf8'));
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
        send(res, 200, JSON.stringify({ ok: true, allowHosts: ALLOW_HOSTS }), {
            'Content-Type': 'application/json; charset=utf-8',
        });
        return;
    }
    if (parsedRequest.pathname !== '/fetch') {
        send(res, 404, 'not found', { 'Content-Type': 'text/plain; charset=utf-8' });
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
    console.log('sitedock fetch proxy listening on 0.0.0.0:' + PORT);
    console.log('allow hosts: ' + ALLOW_HOSTS.join(', '));
});
