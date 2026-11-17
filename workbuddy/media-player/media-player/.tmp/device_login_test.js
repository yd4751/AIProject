'use strict';
/**
 * 设备登录（机器码）端到端验证，完全隔离，不碰 5173 / 6001 与真实配置。
 *  - 临时目录复制网关全部源码，配置 loginMode=device
 *  - HTTP 层：模式判定、微信入口屏蔽、机器码稳定、未授权留痕、放行/停用/删除、token 复检
 *  - 真实 Chrome：登录页自动识别设备 → 显示「无权访问 + 机器码」→ 管理员放行 → 自动进入
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const GWDIR = path.join(ROOT, 'auth-gateway');
const TMP = path.join(__dirname, 'device_case');
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const GATE = 'http://127.0.0.1:5341';
const P_BACK = 5342;
const CDP_PORT = 9466;
const ADMIN_PW = 'TestAdmin#2026';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0;
function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + String(extra).slice(0, 170) : ''));
  ok ? pass++ : fail++;
}
const cfgPath = path.join(TMP, 'config.json');
const wlPath = path.join(TMP, 'whitelist.json');
const recPath = path.join(TMP, 'records.json');
const FILES = ['gateway.js', 'login-page.js', 'admin-page.js', 'device-login-page.js', 'store.js', 'iploc.js', 'ipdb.bin'];

function mock(port) {
  const s = http.createServer((req, res) => {
    const b = 'BACK:' + req.url;
    res.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Length': Buffer.byteLength(b) });
    res.end(b);
  });
  return new Promise((resolve) => s.listen(port, '127.0.0.1', () => resolve(s)));
}
function httpJSON(urlPath) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port: CDP_PORT, path: urlPath }, (res) => {
      let d = '';
      res.on('data', (c) => (d += c));
      res.on('end', () => { try { resolve(JSON.parse(d)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}
async function waitPort(url, ms = 10000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try { const r = await fetch(url, { redirect: 'manual' }); await r.text(); return true; }
    catch (e) { await sleep(150); }
  }
  return false;
}
const req = (p, opt = {}) => fetch(GATE + p, Object.assign({ redirect: 'manual' }, opt));
const J = (r) => r.json();
function deviceLogin(id, fp, extra = {}) {
  return req('/__auth/device/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(Object.assign({ id: id, fp: fp, label: 'Test Device / Chrome' }, extra)),
  });
}
function adminApi(p, body, cookie) {
  return req('/__auth/admin/api/' + p, {
    method: body === undefined ? 'GET' : 'POST',
    headers: Object.assign({ 'Content-Type': 'application/json' }, cookie ? { Cookie: cookie } : {}),
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}
function tokenOf(r) {
  const list = r.headers.getSetCookie ? r.headers.getSetCookie() : [String(r.headers.get('set-cookie') || '')];
  const hit = list.find((c) => c.startsWith('mp_gate='));
  return hit ? decodeURIComponent(hit.split(';')[0].slice('mp_gate='.length)) : '';
}

let gw = null, back = null, chromeProc = null, cdp = null;
async function launchChrome() {
  const prof = path.join(__dirname, 'device_ui_profile');
  try { fs.rmSync(prof, { recursive: true, force: true }); } catch (e) {}
  chromeProc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${CDP_PORT}`, `--user-data-dir=${prof}`,
    '--window-size=1280,860', '--mute-audio', 'about:blank',
  ], { stdio: 'ignore', detached: true });
  chromeProc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) {
    try { list = await httpJSON('/json/list'); if (list && list.length) break; } catch (e) {}
    await sleep(300);
  }
  if (!list || !list.length) throw new Error('Chrome 未就绪');
  const page = list.find((t) => t.type === 'page');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0;
  const pending = new Map();
  const errors = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) {
      const p = pending.get(m.id); pending.delete(m.id);
      m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result);
    } else if (m.method === 'Runtime.exceptionThrown') {
      errors.push((m.params.exceptionDetails.exception || {}).description || m.params.exceptionDetails.text);
    }
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout ' + method)); } }, 30000);
  });
  const evaluate = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error((r.exceptionDetails.exception || {}).description || 'eval error');
    return r.result.value;
  };
  cdp = { ws, send, evaluate, errors, close: () => { try { ws.close(); } catch (e) {} } };
  await send('Page.enable'); await send('Runtime.enable');
}

(async () => {
  fs.rmSync(TMP, { recursive: true, force: true });
  fs.mkdirSync(TMP, { recursive: true });
  FILES.forEach((f) => fs.copyFileSync(path.join(GWDIR, f), path.join(TMP, f)));
  fs.writeFileSync(wlPath, JSON.stringify({ users: [], pending: [], devices: [], devicePending: [] }, null, 2));
  fs.writeFileSync(recPath, '[]');
  fs.writeFileSync(cfgPath, JSON.stringify({
    port: 5341, host: '127.0.0.1', enabled: true, loginMode: 'device', mode: 'auto',
    secret: 'device-test-secret-0123456789abcdef',
    whitelistFile: 'whitelist.json', recordFile: 'records.json',
    admins: [{ username: 'admin', password: ADMIN_PW }], wechat: {},
    upstreams: [{ id: 'r_main', name: '主站', prefix: '/', host: '127.0.0.1',
      port: P_BACK, stripPrefix: false, hosts: [], enabled: true }],
  }, null, 2) + '\n');

  back = await mock(P_BACK);
  gw = spawn(process.execPath, [path.join(TMP, 'gateway.js')], { cwd: TMP, stdio: ['ignore', 'pipe', 'pipe'] });
  let glog = '';
  gw.stdout.on('data', (d) => (glog += d.toString()));
  gw.stderr.on('data', (d) => (glog += d.toString()));
  if (!await waitPort(GATE + '/__auth/status')) throw new Error('网关启动失败: ' + glog.slice(-700));

  // A. 模式与微信入口
  const st = await J(await req('/__auth/status'));
  check('网关进入设备登录模式', st.mode === 'device' && st.loginMode === 'device' && st.deviceMode === true);
  check('微信登录标记为停用', st.wechatConfigured === false, st.modeLabel);
  const loginHtml = await (await req('/__auth/login')).text();
  check('登录页是设备登录页', loginHtml.includes('设备登录') && loginHtml.includes('本机机器码'));
  check('登录页无任何微信扫码入口', !loginHtml.includes('微信扫一扫') && !loginHtml.includes('小程序码'));
  for (const p of ['/__auth/qr', '/__auth/wxacode', '/__auth/poll', '/__auth/wx/callback']) {
    const r = await req(p);
    const d = await J(r);
    check('微信入口已屏蔽 ' + p, r.status === 403 && d.code === 'WECHAT_DISABLED', r.status);
  }
  const mp = await req('/__auth/mp-login', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code: 'fake' }) });
  check('小程序 code 换 token 已屏蔽', mp.status === 403 && (await J(mp)).code === 'WECHAT_DISABLED');

  // B. 未授权：机器码稳定、提示、留痕
  const r1 = await deviceLogin('dev-A', 'fp-111');
  const d1 = await J(r1);
  const mc = d1.machineCode;
  check('未授权返回 403 + 机器码', r1.status === 403 && d1.code === 'NOT_AUTHORIZED'
    && /^MC-[0-9A-F]{4}(-[0-9A-F]{4}){3}$/.test(mc || ''), mc);
  check('未授权响应带可复制提示', /设备白名单/.test(d1.hint || ''));
  const r2 = await J(await deviceLogin('dev-A', 'fp-111'));
  const r3 = await J(await deviceLogin('dev-A', 'fp-222'));
  check('同一设备机器码稳定', r2.machineCode === mc);
  check('不同指纹生成不同机器码', r3.machineCode !== mc, r3.machineCode);
  const wl1 = JSON.parse(fs.readFileSync(wlPath, 'utf8'));
  check('未授权设备进入待授权列表', wl1.devicePending.some((p) => p.code === mc));
  check('轮询不重复刷待授权记录', wl1.devicePending.filter((p) => p.code === mc).length === 1);
  const recs1 = JSON.parse(fs.readFileSync(recPath, 'utf8'));
  check('授权失败写入记录且带机器码', recs1.some((r) => r.type === 'failure' && r.device === mc
    && r.reason === 'not_whitelisted'));
  check('失败不污染微信 pending', wl1.pending.length === 0);
  check('未登录访问首页被重定向到登录页', (await req('/')).status === 302);

  // C. 管理端放行
  const lr = await req('/__auth/admin/login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: ADMIN_PW }) });
  const cookie = String(lr.headers.get('set-cookie') || '').split(';')[0];
  check('管理员可独立登录管理端', (await J(lr)).ok === true && !!cookie);
  check('未登录不能读设备白名单', (await adminApi('devices')).status === 401);
  const bad = await J(await adminApi('devices/allow', { code: 'NOT-A-CODE' }, cookie));
  check('非法机器码被拒绝', bad.error && /格式/.test(bad.error));
  const dv0 = await J(await adminApi('devices', undefined, cookie));
  check('管理端能读到待授权设备', dv0.ok && dv0.pending.some((p) => p.code === mc));
  const okAllow = await J(await adminApi('devices/allow', { code: mc, name: 'bighat 的电脑' }, cookie));
  check('管理端一键放行设备', okAllow.ok === true && okAllow.code === mc);
  const dv1 = await J(await adminApi('devices', undefined, cookie));
  check('放行后该机器码出列、设备生效', !dv1.pending.some((p) => p.code === mc)
    && dv1.devices.length === 1 && dv1.devices[0].enabled !== false,
    '剩余待授权 ' + dv1.pending.length + ' 条（含测试中故意造的其它机器码）');

  // D. 放行后自动签发会话
  const r4 = await deviceLogin('dev-A', 'fp-111', { next: '/some/page?x=1' });
  const d4 = await J(r4);
  const tok = tokenOf(r4);
  check('放行后设备登录成功', d4.ok === true && !!d4.token && !!tok, d4.next);
  check('next 只接受站内相对路径', d4.next === '/some/page?x=1');
  const evil = await J(await deviceLogin('dev-A', 'fp-111', { next: 'https://evil.example.com' }));
  check('拦截开放重定向', evil.next.startsWith('/') && !evil.next.includes('evil'));
  const home = await req('/', { headers: { Cookie: 'mp_gate=' + encodeURIComponent(tok) } });
  check('带会话访问首页被转发到后端', home.status === 200 && (await home.text()).startsWith('BACK:/'));
  const api = await req('/api/sites', { headers: { Cookie: 'mp_gate=' + encodeURIComponent(tok) } });
  check('带会话访问 API 正常', api.status === 200 && (await api.text()).startsWith('BACK:/api/sites'));
  const recs2 = JSON.parse(fs.readFileSync(recPath, 'utf8'));
  check('授权成功写入记录', recs2.some((r) => r.type === 'success' && r.device === mc));

  // E. 停用 / 启用 / 删除 / 改备注
  await adminApi('devices/revoke', { code: mc }, cookie);
  const off = await req('/', { headers: { Cookie: 'mp_gate=' + encodeURIComponent(tok) } });
  check('停用设备后旧会话立即失效', off.status === 302);
  await adminApi('devices/allow', { code: mc }, cookie);
  const dv2 = await J(await adminApi('devices', undefined, cookie));
  check('启用设备保留原备注', dv2.devices[0].name === 'bighat 的电脑', dv2.devices[0].name);
  const backOn = await req('/', { headers: { Cookie: 'mp_gate=' + encodeURIComponent(tok) } });
  check('重新启用后旧会话恢复可用', backOn.status === 200);
  await adminApi('devices/rename', { code: mc, name: '客厅电视' }, cookie);
  const dv3 = await J(await adminApi('devices', undefined, cookie));
  check('可修改设备备注', dv3.devices[0].name === '客厅电视', dv3.devices[0].name);
  await adminApi('devices/revoke', { code: mc, remove: true }, cookie);
  const dv4 = await J(await adminApi('devices', undefined, cookie));
  check('删除设备后需重新授权', dv4.devices.length === 0
    && (await J(await deviceLogin('dev-A', 'fp-111'))).code === 'NOT_AUTHORIZED');

  // F. 真实浏览器：自动识别 → 未授权提示 → 放行后自动进入
  await launchChrome();
  await cdp.send('Page.navigate', { url: GATE + '/__auth/login' });
  await sleep(2600);
  const E = cdp.evaluate;
  const uiState = await E("document.getElementById('state').textContent");
  const uiCode = await E("document.getElementById('code').textContent");
  const denyVisible = await E("getComputedStyle(document.getElementById('deny')).display !== 'none'");
  check('浏览器自动识别并显示无权访问', /无权访问/.test(uiState) && denyVisible, uiState);
  check('浏览器显示可复制的机器码', /^MC-[0-9A-F]{4}(-[0-9A-F]{4}){3}$/.test(uiCode), uiCode);
  check('页面无微信 / 口令输入框', await E("!document.body.textContent.includes('微信扫一扫') && !document.querySelector('input[type=password]')"));
  const shot = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(path.join(__dirname, 'device_denied.png'), Buffer.from(shot.data, 'base64'));
  await adminApi('devices/allow', { code: uiCode, name: '浏览器设备' }, cookie);
  let entered = false;
  for (let i = 0; i < 12; i++) {
    await sleep(1000);
    const href = await E('location.pathname + location.search').catch(() => '');
    if (href.startsWith('/__auth/apps') || href === '/' || href.startsWith('/?')) { entered = true; break; }
  }
  check('管理员放行后页面自动进入（无需刷新）', entered, await E('location.pathname').catch(() => ''));

  // 管理端 UI：登录 → 设备白名单 tab → 设备行与操作按钮
  await E("(async()=>{const r=await fetch('/__auth/admin/login',{method:'POST',"
    + "headers:{'Content-Type':'application/json'},body:JSON.stringify({username:'admin',password:"
    + JSON.stringify(ADMIN_PW) + "})});return r.ok})()");
  await cdp.send('Page.navigate', { url: GATE + '/__auth/admin' });
  await sleep(1600);
  check('管理端已登录并显示主界面', await E("!document.getElementById('app').classList.contains('hidden')"));
  await E("Array.from(document.querySelectorAll('.tabs button'))"
    + ".find(b=>b.getAttribute('data-tab')==='devices').click();true");
  await sleep(800);
  check('设备白名单标签可切换', await E("!document.getElementById('tab-devices').classList.contains('hidden')"));
  check('设备白名单页渲染出已授权机器码',
    await E("document.getElementById('dvList').textContent.includes(" + JSON.stringify(uiCode) + ")"));
  check('设备停用/改备注/删除按钮均已绑定',
    await E("document.querySelectorAll('#dvList [data-dvtoggle]').length===1"
      + " && document.querySelectorAll('#dvList [data-dvname]').length===1"
      + " && document.querySelectorAll('#dvList [data-dvdel]').length===1"));
  check('关于页显示设备登录模式说明',
    await E("document.getElementById('about').textContent.includes('设备登录模式')"));
  const shot2 = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(path.join(__dirname, 'device_admin.png'), Buffer.from(shot2.data, 'base64'));
  check('浏览器控制台零异常', cdp.errors.length === 0, cdp.errors.slice(0, 2).join(' / '));

  // G. 切回微信模式：代码保留，只需改配置
  fs.writeFileSync(cfgPath, JSON.stringify(Object.assign(
    JSON.parse(fs.readFileSync(cfgPath, 'utf8')),
    { loginMode: 'wechat', wechat: { miniAppId: 'wx_mock', miniAppSecret: 'mock_secret',
      apiBase: 'http://127.0.0.1:6097', authorizeBase: 'http://127.0.0.1:6097' } }), null, 2) + '\n');
  if (gw) { gw.kill(); await sleep(350); if (gw.exitCode === null) gw.kill('SIGKILL'); }
  gw = spawn(process.execPath, [path.join(TMP, 'gateway.js')], { cwd: TMP, stdio: ['ignore', 'pipe', 'pipe'] });
  if (!await waitPort(GATE + '/__auth/status')) throw new Error('切回微信模式后网关未启动');
  const st2 = await J(await req('/__auth/status'));
  check('改回 loginMode=wechat 即恢复微信登录', st2.mode === 'wechat-mini' && st2.wechatConfigured === true,
    st2.mode);

  console.log('\n结果: ' + pass + ' PASS / ' + fail + ' FAIL');
})().catch((e) => { console.error('测试异常: ' + e.message); fail++; })
  .finally(async () => {
    if (cdp) cdp.close();
    if (chromeProc) { try { chromeProc.kill(); } catch (e) {} }
    if (gw) { try { gw.kill(); } catch (e) {} await sleep(300); if (gw.exitCode === null) gw.kill('SIGKILL'); }
    if (back) { try { back.close(); } catch (e) {} }
    await sleep(200);
    try { fs.rmSync(TMP, { recursive: true, force: true }); } catch (e) {}
    console.log('退出码: ' + (fail ? 1 : 0));
    process.exitCode = fail ? 1 : 0;
  });
