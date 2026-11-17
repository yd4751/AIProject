'use strict';
/**
 * 转发服务在线增改删 + 实时生效 + 登录后服务列表 端到端验证（完全隔离，不改真实配置）
 *   - 临时目录复制 gateway.js / login-page.js / admin-page.js，配置独立端口
 *   - 3 个 mock 后端用于验证选路、路径重写、启停、删除、手工改文件热加载
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const GWDIR = path.join(ROOT, 'auth-gateway');
const TMP = path.join(__dirname, 'routes_admin_case');
const NODE = process.execPath;
const GATE = 'http://127.0.0.1:5311';
const P_MAIN = 5312, P_B = 5313, P_C = 5314;
const ADMIN_PW = 'TestAdmin#2026';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0;
function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + String(extra).slice(0, 160) : ''));
  ok ? pass++ : fail++;
}
const cfgPath = path.join(TMP, 'config.json');

function mock(port, tag) {
  const s = http.createServer((req, res) => {
    const b = tag + ':' + req.url;
    res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': Buffer.byteLength(b) });
    res.end(b);
  });
  return new Promise((resolve) => s.listen(port, '127.0.0.1', () => resolve(s)));
}
async function waitPort(url, ms = 8000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try { const r = await fetch(url, { redirect: 'manual' }); await r.text(); return true; }
    catch (e) { await sleep(120); }
  }
  return false;
}
const J = (r) => r.json();
async function api(p, opt = {}, cookie = '') {
  return fetch(GATE + '/__auth/admin/api/' + p, Object.assign({
    redirect: 'manual', headers: Object.assign({ 'Content-Type': 'application/json' },
      cookie ? { Cookie: cookie } : {}),
  }, opt));
}
const post = (p, body, cookie) => api(p, { method: 'POST', body: JSON.stringify(body) }, cookie);
const get = (p) => fetch(GATE + p, { redirect: 'manual' });

let gw = null;
const mocks = [];
async function startGateway() {
  gw = spawn(NODE, [path.join(TMP, 'gateway.js')], { cwd: TMP, stdio: ['ignore', 'pipe', 'pipe'] });
  let log = '';
  gw.stdout.on('data', (d) => { log += d.toString(); });
  gw.stderr.on('data', (d) => { log += d.toString(); });
  const ok = await waitPort(GATE + '/__auth/status');
  if (!ok) throw new Error('网关启动失败：' + log.slice(-800));
}
async function stopGateway() {
  if (!gw || gw.exitCode !== null) return;
  gw.kill();
  await sleep(250);
  if (gw.exitCode === null) gw.kill('SIGKILL');
  gw = null;
}

(async () => {
  fs.rmSync(TMP, { recursive: true, force: true });
  fs.mkdirSync(TMP, { recursive: true });
  ['gateway.js', 'login-page.js', 'device-login-page.js', 'admin-page.js', 'store.js', 'iploc.js', 'ipdb.bin'].forEach((f) =>
    fs.copyFileSync(path.join(GWDIR, f), path.join(TMP, f)));
  fs.writeFileSync(path.join(TMP, 'whitelist.json'), JSON.stringify({ users: [], pending: [] }, null, 2));
  fs.writeFileSync(path.join(TMP, 'records.json'), '[]');
  const cfg = {
    port: 5311, host: '127.0.0.1', enabled: false, mode: 'auto',
    secret: 'fixed-test-secret-0123456789abcdef',
    whitelistFile: 'whitelist.json', recordFile: 'records.json',
    admins: [{ username: 'admin', password: ADMIN_PW }],
    wechat: {},
    upstreams: [{ id: 'r_main', name: '主站', prefix: '/', host: '127.0.0.1', port: P_MAIN,
      stripPrefix: false, hosts: [], enabled: true }],
  };
  fs.writeFileSync(cfgPath, JSON.stringify(cfg, null, 2) + '\n');

  mocks.push(await mock(P_MAIN, 'MAIN'), await mock(P_B, 'BACK_B'), await mock(P_C, 'BACK_C'));
  await startGateway();

  // 管理端登录
  const login = await fetch(GATE + '/__auth/admin/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: ADMIN_PW }),
  });
  const cookie = String(login.headers.get('set-cookie') || '').split(';')[0];
  const lj = await J(login);
  check('管理端登录成功', lj.ok === true && !!cookie, 'cookie=' + cookie.slice(0, 12));

  let r = await api('routes');
  check('未登录不能读转发服务', r.status === 401);
  r = await api('routes', {}, cookie);
  let d = await J(r);
  check('初始路由表返回 1 条且带 id', d.ok && d.routes.length === 1 && !!d.routes[0].id);
  check('管理端接口返回落地配置文件', String(d.file || '').endsWith('config.json'));

  // 在线新增
  r = await post('routes/save', { name: '演示服务', prefix: '/demo', host: '127.0.0.1',
    port: P_B, stripPrefix: false, enabled: true, hosts: [] }, cookie);
  d = await J(r);
  const demoId = d.id;
  check('在线新增转发服务成功', d.ok && !!demoId && d.routes.length === 2);
  let body = await (await get('/demo/hello')).text();
  check('新增后立即生效（无需重启）', body === 'BACK_B:/demo/hello', body);

  // 在线修改（改目标 + 去前缀 + 改名）
  r = await post('routes/save', { id: demoId, name: '演示服务V2', prefix: '/demo',
    host: '127.0.0.1', port: P_C, stripPrefix: true, enabled: true, hosts: [] }, cookie);
  d = await J(r);
  check('在线修改转发服务成功', d.ok && d.routes.some((x) => x.name === '演示服务V2'));
  body = await (await get('/demo/hello?x=1')).text();
  check('修改后立即生效且路径被重写', body === 'BACK_C:/hello?x=1', body);

  // 配置文件确实落盘
  const onDisk = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
  check('改动写回 config.json', onDisk.upstreams.length === 2
    && onDisk.upstreams.some((x) => x.name === '演示服务V2' && x.port === P_C));

  // 授权后服务列表
  const appsHtml = await (await get('/__auth/apps')).text();
  check('服务列表展示两个服务', appsHtml.includes('主站') && appsHtml.includes('演示服务V2'));
  check('服务列表卡片可点击跳转', appsHtml.includes('href="/demo/"') && appsHtml.includes('进入'));
  r = await get('/__auth/login');
  check('授权通过统一进入服务列表页', r.headers.get('location') === '/__auth/apps',
    r.headers.get('location'));
  const status = await J(await get('/__auth/status'));
  check('状态接口的 landing 指向服务列表', status.landing === '/__auth/apps'
    && status.routes.length === 2);

  // 手工改配置文件热加载
  const manual = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
  manual.upstreams.find((x) => x.id === demoId).port = P_B;
  manual.upstreams.find((x) => x.id === demoId).stripPrefix = false;
  fs.writeFileSync(cfgPath, JSON.stringify(manual, null, 2) + '\n');
  body = await (await get('/demo/hot')).text();
  check('手工改 config.json 也实时生效', body === 'BACK_B:/demo/hot', body);

  // 停用 / 启用
  r = await post('routes/toggle', { id: demoId, enabled: false }, cookie);
  d = await J(r);
  check('停用成功', d.ok && d.routes.find((x) => x.id === demoId).enabled === false);
  body = await (await get('/demo/off')).text();
  check('停用后请求落到兜底服务', body === 'MAIN:/demo/off', body);
  const appsAfterOff = await (await get('/__auth/apps')).text();
  check('停用服务不再出现在服务列表', !appsAfterOff.includes('演示服务V2'));
  await post('routes/toggle', { id: demoId, enabled: true }, cookie);
  body = await (await get('/demo/on')).text();
  check('启用后立刻恢复', body === 'BACK_B:/demo/on', body);

  // 非法输入
  const bads = [
    ['保留路径 /__auth', { name: 'X', prefix: '/__auth/x', host: '127.0.0.1', port: P_B }],
    ['非法端口', { name: 'X', prefix: '/x', host: '127.0.0.1', port: 0 }],
    ['空主机', { name: 'X', prefix: '/x', host: '', port: P_B }],
    ['空服务名', { name: '', prefix: '/x', host: '127.0.0.1', port: P_B }],
    ['重复通用前缀', { name: 'X', prefix: '/', host: '127.0.0.1', port: P_B }],
  ];
  for (const [label, b] of bads) {
    const rr = await post('routes/save', b, cookie);
    const dd = await J(rr);
    check('拒绝' + label, rr.status === 400 && !!dd.error, dd.error);
  }

  // 在线删除
  r = await post('routes/delete', { id: demoId }, cookie);
  d = await J(r);
  check('在线删除成功', d.ok && d.routes.length === 1);
  body = await (await get('/demo/gone')).text();
  check('删除后立即不再转发', body === 'MAIN:/demo/gone', body);

  // 删空所有服务不崩溃
  await post('routes/delete', { id: 'r_main' }, cookie);
  r = await get('/');
  const emptyBody = await r.text();
  check('删空服务返回明确提示而非崩溃', r.status === 503 && emptyBody.includes('NO_UPSTREAM'), emptyBody);
  const appsEmpty = await (await get('/__auth/apps')).text();
  check('无服务时列表页给出引导', appsEmpty.includes('暂无可用服务'));

  // 管理端页面含在线编辑表单
  const adminHtml = await (await get('/__auth/admin')).text();
  check('管理端含转发服务表单', ['rtName', 'rtPrefix', 'rtHost', 'rtPort', 'rtSave', 'rtCancel']
    .every((x) => adminHtml.includes('id="' + x + '"')));
  check('管理端标注实时生效', adminHtml.includes('立即生效，无需重启'));

  // 操作留痕
  const recs = JSON.parse(fs.readFileSync(path.join(TMP, 'records.json'), 'utf8'));
  check('增删改均写入授权记录', ['admin-route-save', 'admin-route-toggle', 'admin-route-delete']
    .every((a) => recs.some((x) => x.action === a)));

  // 重启后配置保持
  await stopGateway();
  await startGateway();
  r = await get('/');
  check('重启后仍保持删空状态（配置持久化）', r.status === 503);

  console.log('\n结果: ' + pass + ' 通过 / ' + fail + ' 失败');
})().catch((e) => { console.error('测试异常：', e); fail++; process.exitCode = 1; })
  .finally(async () => {
    await stopGateway();
    mocks.forEach((s) => { try { s.close(); } catch (e) { /* ignore */ } });
    try { fs.rmSync(TMP, { recursive: true, force: true }); } catch (e) { /* ignore */ }
    if (fail) process.exitCode = 1;
  });
