'use strict';
/** 新版 dist/auth-gateway.exe 专项：验证「在线增改删转发服务 + 实时生效 + 落盘到 gateway-config.json」。
 *  使用隔离端口，不碰用户正在运行的 5173 / 6001 服务。 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const EXE = path.join(ROOT, 'dist', 'auth-gateway.exe');
const RUN = path.join(__dirname, 'gw_exe_routes_run');
const GATE = 'http://127.0.0.1:5391';
const P_MAIN = 5392, P_NEW = 5393;
const PW = 'TestAdmin#2026';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0, proc = null;
const mocks = [];
const check = (n, ok, x) => { console.log((ok ? '  PASS ' : '  FAIL ') + n + (x ? ' | ' + String(x).slice(0, 140) : '')); ok ? pass++ : fail++; };
const req = (u, o = {}) => fetch(u, Object.assign({ redirect: 'manual' }, o));

function mock(port, tag) {
  const s = http.createServer((q, r) => {
    const b = tag + ':' + q.url;
    r.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Length': Buffer.byteLength(b) });
    r.end(b);
  });
  return new Promise((resolve) => s.listen(port, '127.0.0.1', () => resolve(s)));
}

(async () => {
  fs.rmSync(RUN, { recursive: true, force: true });
  fs.mkdirSync(RUN, { recursive: true });
  fs.copyFileSync(EXE, path.join(RUN, 'auth-gateway.exe'));
  fs.writeFileSync(path.join(RUN, 'whitelist.json'), JSON.stringify({ users: [], pending: [] }));
  const cfg = {
    port: 5391, host: '127.0.0.1', enabled: false, mode: 'auto',
    whitelistFile: 'whitelist.json', recordFile: 'records.json',
    secret: 'exe-routes-test-secret-0123456789',
    admins: [{ username: 'admin', password: PW }],
    wechat: {},
    upstreams: [{ id: 'r_main', name: '主站', prefix: '/', host: '127.0.0.1',
      port: P_MAIN, stripPrefix: false, hosts: [], enabled: true }],
  };
  const cfgFile = path.join(RUN, 'gateway-config.json');
  fs.writeFileSync(cfgFile, JSON.stringify(cfg, null, 2) + '\n');

  mocks.push(await mock(P_MAIN, 'MAIN'), await mock(P_NEW, 'NEW'));
  let log = '';
  proc = spawn(path.join(RUN, 'auth-gateway.exe'), [], { cwd: RUN, stdio: ['ignore', 'pipe', 'pipe'] });
  proc.stdout.on('data', (c) => { log += c; });
  proc.stderr.on('data', (c) => { log += c; });
  let ready = false;
  for (let i = 0; i < 60; i++) {
    try { if ((await req(GATE + '/__auth/status')).status === 200) { ready = true; break; } } catch (e) {}
    await sleep(300);
  }
  check('新版 exe 在隔离端口启动', ready, ready ? '' : log.slice(-400));
  if (!ready) return;

  const login = await req(GATE + '/__auth/admin/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: PW }),
  });
  const cookie = String(login.headers.get('set-cookie') || '').split(';')[0];
  check('exe 管理端登录成功', (await login.json()).ok === true && !!cookie);
  const post = (p, b) => req(GATE + '/__auth/admin/api/' + p, {
    method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify(b),
  });

  let r = await post('routes/save', { name: '新增服务', prefix: '/demo', host: '127.0.0.1',
    port: P_NEW, enabled: true, stripPrefix: false, hosts: [] });
  let d = await r.json();
  const id = d.id;
  check('exe 内在线新增转发服务', d.ok === true && !!id && d.routes.length === 2);
  let body = await (await req(GATE + '/demo/x')).text();
  check('exe 内新增后立即生效', body === 'NEW:/demo/x', body);

  r = await post('routes/toggle', { id: id, enabled: false });
  check('exe 内停用成功', (await r.json()).ok === true);
  body = await (await req(GATE + '/demo/y')).text();
  check('停用后走兜底服务', body === 'MAIN:/demo/y', body);
  await post('routes/toggle', { id: id, enabled: true });

  r = await post('routes/delete', { id: id });
  check('exe 内删除成功', (await r.json()).routes.length === 1);
  body = await (await req(GATE + '/demo/z')).text();
  check('删除后立即不再转发', body === 'MAIN:/demo/z', body);

  const disk = JSON.parse(fs.readFileSync(cfgFile, 'utf8'));
  check('改动落盘到 exe 同目录 gateway-config.json', disk.upstreams.length === 1
    && disk.upstreams[0].id === 'r_main');
  check('明文管理员密码已转哈希', !!disk.admins[0].passwordHash && !disk.admins[0].password);
  const apps = await (await req(GATE + '/__auth/apps')).text();
  check('exe 内授权后服务列表可点击', apps.includes('主站') && apps.includes('href="/"'));
  const adminHtml = await (await req(GATE + '/__auth/admin')).text();
  check('exe 内管理端含在线编辑表单', adminHtml.includes('id="rtSave"'));
  // 404 修复：exe 内嵌页面必须是修复后的脚本，且双斜杠路径能正常返回
  check('exe 内嵌页面无非法正则', adminHtml.includes('replace(/^\\/+/') && !adminHtml.includes('replace(/^/+/'));
  const rr1 = await req(GATE + '/__auth/admin/api//records?type=all&q=', { headers: { Cookie: cookie } });
  const rr2 = await req(GATE + '/__auth/admin/api/records?type=all&q=', { headers: { Cookie: cookie } });
  check('exe 内 records 双斜杠路径不再 404', rr1.status === 200 && rr2.status === 200,
    rr1.status + '/' + rr2.status);
  check('exe 运行期零异常', !/Error|Unhandled|EADDRINUSE/i.test(log), log.slice(-200));

  console.log('\n结果: ' + pass + ' 通过 / ' + fail + ' 失败');
})().catch((e) => { console.error('测试异常:', e); fail++; })
  .finally(async () => {
    if (proc) { try { proc.kill(); } catch (e) {} await sleep(400); if (proc.exitCode === null) proc.kill('SIGKILL'); }
    mocks.forEach((s) => { try { s.close(); } catch (e) {} });
    try { fs.rmSync(RUN, { recursive: true, force: true }); } catch (e) {}
    if (fail) process.exitCode = 1;
  });
