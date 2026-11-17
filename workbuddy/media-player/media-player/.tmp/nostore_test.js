'use strict';
// 验证：网关转发页面时强制 no-store，并剥离条件请求头（防手机复用旧缓存）
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const RUN = path.join(ROOT, '.tmp', 'gwnostore');
const GATE = 'http://127.0.0.1:5297';
const UP_PORT = 6097;
let pass = 0, fail = 0, proc = null, up = null, seenReqHeaders = {};

function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + extra : ''));
  ok ? pass++ : fail++;
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  fs.rmSync(RUN, { recursive: true, force: true });
  fs.mkdirSync(RUN, { recursive: true });
  for (const f of ['gateway.js', 'login-page.js', 'device-login-page.js', 'admin-page.js', 'store.js', 'iploc.js', 'ipdb.bin']) {
    const src = path.join(ROOT, 'auth-gateway', f);
    if (fs.existsSync(src)) fs.copyFileSync(src, path.join(RUN, f));
  }
  fs.writeFileSync(path.join(RUN, 'config.json'), JSON.stringify({
    port: 5297, host: '127.0.0.1',
    upstreams: [{ name: 'fake-player', prefix: '/', host: '127.0.0.1', port: UP_PORT, stripPrefix: false, enabled: true }],
    enabled: false, mode: 'auto', loginMode: 'device',
    secret: 'x'.repeat(64), sessionHours: 168, adminSessionHours: 12,
    allowTokenInQuery: false, corsOrigins: [], whitelistFile: 'whitelist.json', recordFile: 'records.json',
    admins: [{ username: 'admin', salt: 'aa', passwordHash: 'bb' }], wechat: {}
  }));
  fs.writeFileSync(path.join(RUN, 'whitelist.json'), JSON.stringify({ users: [{ openid: 'u1', name: 'tester' }], pending: [] }));

  // 模拟后端：HTML 带强缓存头 + ETag；记录网关转发来的请求头
  up = http.createServer((req, res) => {
    seenReqHeaders = req.headers;
    if (req.url.startsWith('/api/')) { res.writeHead(200, {'Content-Type':'application/json','Cache-Control':'public, max-age=60'}); return res.end('{"ok":1}'); }
    if (req.url.startsWith('/style.css')) {
      res.writeHead(200, { 'Content-Type': 'text/css', 'Cache-Control': 'public, max-age=31536000', ETag: '"css1"' });
      return res.end('body{color:red}');
    }
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'public, max-age=31536000', ETag: '"v1"', 'Last-Modified': 'Wed, 01 Jan 2020 00:00:00 GMT' });
    res.end('<!doctype html><html><head></head><body>PLAYER</body></html>');
  });
  await new Promise((r) => up.listen(UP_PORT, '127.0.0.1', r));

  proc = spawn(process.execPath, [path.join(RUN, 'gateway.js')], { cwd: RUN, env: { ...process.env } });
  await sleep(1200);

  // 模拟浏览器带条件头 + If-None-Match 访问（这正是手机命中旧缓存时会发的请求）
  const r = await fetch(GATE + '/player.html', {
    redirect: 'manual',
    headers: { 'If-None-Match': '"v1"', 'If-Modified-Since': 'Wed, 01 Jan 2020 00:00:00 GMT' }
  });
  const body = await r.text();
  check('转发返回 200 而非 304', r.status === 200, 'status=' + r.status);
  check('HTML 响应为 no-store', /no-store/i.test(r.headers.get('cache-control') || ''), 'cache-control=' + r.headers.get('cache-control'));
  check('HTML 响应已去除 ETag', !r.headers.get('etag'));
  check('HTML 响应已去除 Last-Modified', !r.headers.get('last-modified'));
  check('条件头 If-None-Match 未透传给后端', !seenReqHeaders['if-none-match'], 'seen=' + seenReqHeaders['if-none-match']);
  check('条件头 If-Modified-Since 未透传给后端', !seenReqHeaders['if-modified-since']);
  check('页面内容正常', body.includes('PLAYER'));

  const rc = await fetch(GATE + '/style.css', { redirect: 'manual' });
  check('CSS 响应为 no-store', /no-store/i.test(rc.headers.get('cache-control') || ''), 'cache-control=' + rc.headers.get('cache-control'));

  const rj = await fetch(GATE + '/api/ping', { redirect: 'manual' });
  check('非页面资源未被强制 no-store（仍透传后端）', /max-age/i.test(rj.headers.get('cache-control') || ''), 'cache-control=' + rj.headers.get('cache-control'));
}

main().then(() => {
  console.log('\n结果: ' + pass + ' PASS / ' + fail + ' FAIL');
  if (proc) proc.kill();
  if (up) up.close();
  process.exit(fail ? 1 : 0);
}).catch((e) => {
  console.error(e);
  if (proc) proc.kill();
  if (up) up.close();
  process.exit(1);
});
