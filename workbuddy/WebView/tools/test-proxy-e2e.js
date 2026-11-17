// 抓取代理端到端冒烟测试：启动本地代理 -> 抓取目标站点 -> 校验返回 HTML
const { spawn } = require('child_process');
const http = require('http');
const path = require('path');

const PORT = 18787;
const TOKEN = 'test-token';
const TARGET = 'http://43.139.101.12:5173';

const child = spawn(process.execPath, [path.join(__dirname, 'proxy-server.js')], {
    env: Object.assign({}, process.env, { PORT: String(PORT), PROXY_TOKEN: TOKEN }),
    stdio: ['ignore', 'pipe', 'pipe'],
});
let childLog = '';
child.stdout.on('data', chunk => { childLog += chunk.toString(); });
child.stderr.on('data', chunk => { childLog += chunk.toString(); });

function get(requestPath) {
    return new Promise((resolve, reject) => {
        const req = http.request({ hostname: '127.0.0.1', port: PORT, path: requestPath, method: 'GET', timeout: 15000 }, res => {
            const chunks = [];
            res.on('data', chunk => chunks.push(chunk));
            res.on('end', () => resolve({ status: res.statusCode, body: Buffer.concat(chunks).toString('utf8'), cors: res.headers['access-control-allow-origin'] || '' }));
        });
        req.on('timeout', () => { req.destroy(); reject(new Error('代理请求超时')); });
        req.on('error', reject);
        req.end();
    });
}

function waitHealth(attempt) {
    return get('/health').catch(error => {
        if (attempt <= 0) throw error;
        return new Promise(resolve => setTimeout(resolve, 150)).then(() => waitHealth(attempt - 1));
    });
}

(async () => {
    try {
        const health = await waitHealth(20);
        console.log('health:', health.body.trim());

        const denied = await get('/fetch?url=' + encodeURIComponent(TARGET));
        console.log('无令牌:', denied.status, denied.body.trim());

        const ok = await get('/fetch?url=' + encodeURIComponent(TARGET) + '&token=' + TOKEN);
        const isHtml = /<html|<!doctype/i.test(ok.body);
        console.log('抓取结果: status=' + ok.status + ' bytes=' + Buffer.byteLength(ok.body) + ' isHtml=' + isHtml + ' cors=' + ok.cors);

        if (denied.status !== 401) throw new Error('令牌校验未生效');
        if (ok.status !== 200 || !isHtml) throw new Error('未能抓取到目标 HTML: ' + ok.body.slice(0, 120));
        if (ok.cors !== '*') throw new Error('缺少 CORS 头');
        console.log('代理端到端测试通过');
    } catch (error) {
        console.log('代理端到端测试失败: ' + error.message);
        console.log(childLog.trim());
        process.exitCode = 1;
    } finally {
        child.kill();
    }
})();
