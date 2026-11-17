// 站点连通性探测：区分 HTTP 明文 / HTTPS / 端口 / CORS 响应头
const http = require('http');
const https = require('https');

const TARGETS = [
    { label: 'HTTP  43.139.101.12:5173', protocol: 'http:', hostname: '43.139.101.12', port: 5173 },
    { label: 'HTTPS 43.139.101.12:5173', protocol: 'https:', hostname: '43.139.101.12', port: 5173 },
];

function probe(target) {
    return new Promise(resolve => {
        const client = target.protocol === 'https:' ? https : http;
        const req = client.request({
            hostname: target.hostname,
            port: target.port,
            method: 'GET',
            path: '/',
            timeout: 8000,
            rejectUnauthorized: false,
            headers: { Origin: 'https://preview.workbuddy.cn', Accept: 'text/html' },
        }, res => {
            let bytes = 0;
            res.on('data', chunk => { bytes += chunk.length; });
            res.on('end', () => {
                resolve({
                    label: target.label,
                    status: res.statusCode,
                    server: res.headers.server || '-',
                    cors: res.headers['access-control-allow-origin'] || '无',
                    location: res.headers.location || '-',
                    bytes: bytes,
                });
            });
        });
        req.on('timeout', () => { req.destroy(); resolve({ label: target.label, error: '超时' }); });
        req.on('error', error => resolve({ label: target.label, error: error.code || error.message }));
        req.end();
    });
}

Promise.all(TARGETS.map(probe)).then(results => {
    for (const item of results) console.log(JSON.stringify(item));
});
