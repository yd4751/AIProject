'use strict';
/**
 * 鉴权网关端到端验证（自包含，不依赖真实微信凭证 / 真实播放器服务）
 *
 * 进程内启动：
 *   - 6097 微信接口 mock（授权页 / oauth2 token / jscode2session）
 *   - 6095 主后端 mock（首页、API、Range、POST echo）
 *   - 6096 第二后端 mock（用于验证多转发与路径重写）
 * 子进程启动：
 *   - 5299 网关（临时配置，跑完恢复）
 * 再用无头 Chrome 验证登录页 → 扫码 → 自动进入 的完整交互。
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const GWDIR = path.join(ROOT, 'auth-gateway');
const CFG_FILE = path.join(GWDIR, 'config.json');
const WL_TMP = path.join(ROOT, '.tmp', 'gwtest-whitelist.json');
const REC_TMP = path.join(ROOT, '.tmp', 'gwtest-records.json');
const GATE = 'http://127.0.0.1:5299';
const BACKUP = {
  cfg: fs.existsSync(CFG_FILE) ? fs.readFileSync(CFG_FILE, 'utf8') : null,
  wl: fs.existsSync(path.join(GWDIR, 'whitelist.json')) ? fs.readFileSync(path.join(GWDIR, 'whitelist.json'), 'utf8') : null,
};
// 任何退出路径（含端口冲突导致的未捕获异常）都还原真实配置，避免测试配置残留
let restored = false;
function restoreConfig() {
  if (restored) return;
  restored = true;
  try { if (BACKUP.cfg !== null) fs.writeFileSync(CFG_FILE, BACKUP.cfg, 'utf8'); } catch (e) { /* 忽略 */ }
  try { if (BACKUP.wl !== null) fs.writeFileSync(path.join(GWDIR, 'whitelist.json'), BACKUP.wl, 'utf8'); } catch (e) { /* 忽略 */ }
}
process.on('exit', restoreConfig);
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const ADMIN_PW = 'TestAdmin#2026';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0;
function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + extra : ''));
  ok ? pass++ : fail++;
}
const req = (url, opt = {}) => fetch(url, Object.assign({ redirect: 'manual' }, opt));
function cookieOf(r) { return String(r.headers.get('set-cookie') || '').split(';')[0]; }
function readWL() { try { return JSON.parse(fs.readFileSync(WL_TMP, 'utf8')); } catch (e) { return { users: [], pending: [] }; } }
function readRec() { try { return JSON.parse(fs.readFileSync(REC_TMP, 'utf8')); } catch (e) { return []; } }
function rawHttp(port, pathname, headers = {}) {
  return new Promise((resolve, reject) => {
    const r = http.get({ host: '127.0.0.1', port, path: pathname, headers }, (x) => {
      let d = ''; x.on('data', (c) => d += c);
      x.on('end', () => resolve({ status: x.statusCode, headers: x.headers, body: d }));
    });
    r.on('error', reject);
    r.setTimeout(4000, () => { r.destroy(); reject(new Error('timeout')); });
  });
}

/* --------------------------- mock 服务 --------------------------- */
let mockOpenid = 'wx_denied_user_001';
let mockMiniOpenid = 'wx_mini_user_001';
const wxMock = http.createServer((req, res) => {
  const u = new URL(req.url, 'http://127.0.0.1:6097');
  const j = (o, code = 200) => {
    const b = JSON.stringify(o);
    res.writeHead(code, { 'Content-Type': 'application/json; charset=utf-8', 'Content-Length': Buffer.byteLength(b) });
    res.end(b);
  };
  if (u.pathname === '/__control') {
    if (u.searchParams.get('openid')) mockOpenid = u.searchParams.get('openid');
    if (u.searchParams.get('mini')) mockMiniOpenid = u.searchParams.get('mini');
    return j({ ok: true, openid: mockOpenid, mini: mockMiniOpenid });
  }
  if (u.pathname === '/connect/qrconnect' || u.pathname === '/connect/oauth2/authorize') {
    const redirect = u.searchParams.get('redirect_uri') || (GATE + '/__auth/wx/callback');
    const state = u.searchParams.get('state') || '';
    const code = mockOpenid === '__badcode__' ? 'badcode' : 'mockcode';
    const sep = redirect.includes('?') ? '&' : '?';
    res.writeHead(302, { Location: redirect + sep + 'code=' + code + '&state=' + encodeURIComponent(state) });
    return res.end();
  }
  if (u.pathname === '/sns/oauth2/access_token') {
    if (u.searchParams.get('code') === 'badcode') return j({ errcode: 40029, errmsg: 'invalid code' });
    return j({ access_token: 'mock_at', expires_in: 7200, openid: mockOpenid,
      unionid: 'union_' + mockOpenid, scope: 'snsapi_login' });
  }
  if (u.pathname === '/sns/jscode2session') {
    const code = u.searchParams.get('js_code') || '';
    if (code.startsWith('bad')) return j({ errcode: 40029, errmsg: 'invalid code' });
    return j({ openid: mockMiniOpenid, unionid: 'union_' + mockMiniOpenid, session_key: 'mock_sk' });
  }
  if (u.pathname === '/cgi-bin/token') return j({ access_token: 'mock_token', expires_in: 7200 });
  return j({ errcode: 404, errmsg: 'not found' }, 404);
});

const mainMock = http.createServer((req, res) => {
  const u = new URL(req.url, 'http://127.0.0.1:6095');
  const send = (code, body, headers = {}) => {
    const b = typeof body === 'string' ? body : JSON.stringify(body);
    res.writeHead(code, Object.assign({ 'X-Backend': 'main',
      'Content-Type': typeof body === 'string' ? 'text/html; charset=utf-8' : 'application/json; charset=utf-8',
      'Content-Length': Buffer.byteLength(b) }, headers));
    res.end(b);
  };
  if (u.pathname === '/') return send(200, '<html><body><h1>MAIN_BACKEND_OK</h1></body></html>');
  if (u.pathname === '/api/sites') return send(200, { sites: [{ id: 'gequbao', name: '歌曲宝' }] });
  if (u.pathname === '/api/stream') {
    if (req.headers.range) return send(206, 'ABCD', { 'Content-Type': 'audio/mpeg',
      'Accept-Ranges': 'bytes', 'Content-Range': 'bytes 0-3/100' });
    return send(200, 'ALLDATA', { 'Content-Type': 'audio/mpeg', 'Accept-Ranges': 'bytes' });
  }
  if (u.pathname === '/api/echo' && req.method === 'POST') {
    let d = ''; req.on('data', (c) => d += c);
    req.on('end', () => send(200, { method: req.method, body: d,
      openidHeader: req.headers['x-auth-openid'] || '', routeHeader: req.headers['x-gateway-route'] || '' }));
    return;
  }
  return send(200, { backend: 'main', path: req.url, route: req.headers['x-gateway-route'] || '' });
});

const demoMock = http.createServer((req, res) => {
  const b = JSON.stringify({ backend: 'demo', path: req.url,
    route: req.headers['x-gateway-route'] || '', openid: req.headers['x-auth-openid'] || '' });
  res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8',
    'X-Backend': 'demo', 'Content-Length': Buffer.byteLength(b) });
  res.end(b);
});

let adminCookie = '';
async function postAdmin(route, body) {
  const r = await req(GATE + route, { method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: adminCookie },
    body: JSON.stringify(body) });
  let d = {};
  try { d = await r.json(); } catch (e) {}
  return { status: r.status, data: d, cookie: cookieOf(r) };
}
async function getAdmin(route) {
  const r = await req(GATE + route, { headers: { Cookie: adminCookie } });
  let d = {};
  try { d = await r.json(); } catch (e) {}
  return { status: r.status, data: d };
}

/* --------------------------- CDP 浏览器 --------------------------- */
async function browserTests() {
  console.log('\n===== D. 无头 Chrome 交互 =====');
  if (!fs.existsSync(CHROME)) { check('Chrome 可用', false, CHROME); return; }
  const prof = path.join(ROOT, '.tmp', 'profGw' + Date.now());
  const port = 9257;
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--remote-debugging-port=' + port, '--user-data-dir=' + prof, '--window-size=1280,900', 'about:blank'],
  { stdio: 'ignore' });
  let ws = null;
  try {
    let list = null;
    for (let i = 0; i < 60; i++) {
      try {
        const r = await fetch('http://127.0.0.1:' + port + '/json/list');
        list = await r.json();
        if (list && list.length) break;
      } catch (e) { /* 未就绪 */ }
      await sleep(300);
    }
    if (!list || !list.length) { check('Chrome 调试端口就绪', false); return; }
    ws = new WebSocket(list[0].webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
    let seq = 0; const pend = new Map(); const events = [];
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      if (m.id && pend.has(m.id)) {
        const { resolve, reject } = pend.get(m.id); pend.delete(m.id);
        m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result);
      }
      if (m.method) events.push(m);
    };
    const send = (method, params) => new Promise((resolve, reject) => {
      const id = ++seq; pend.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params: params || {} }));
    });
    const ev = async (expr) => {
      const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) throw new Error(String(r.exceptionDetails.text).slice(0, 120));
      return r.result.value;
    };
    await send('Page.enable'); await send('Runtime.enable');
    // 捕获登录页自己发起的 /__auth/qr 响应，拿到它正在轮询的 ticket
    await send('Page.addScriptToEvaluateOnNewDocument', { source: [
      'window.__qrs = [];',
      'var __of = window.fetch;',
      'window.fetch = function () {',
      '  var args = arguments;',
      '  return __of.apply(this, args).then(function (r) {',
      '    try { if (String(args[0] || "").indexOf("/__auth/qr") >= 0) {',
      '      r.clone().json().then(function (d) { window.__qrs.push(d); });',
      '    } } catch (e) {}',
      '    return r;',
      '  });',
      '};',
    ].join('\n') });
    await send('Page.navigate', { url: GATE + '/__auth/login' });
    await sleep(1800);
    const dom = await ev(`(() => ({
      title: document.title,
      hasPw: !!document.querySelector('input[type=password]'),
      hasQr: !!document.querySelector('#qr svg'),
      text: document.body.innerText.slice(0, 160),
      ticket: (window.__qrs && window.__qrs[0] && window.__qrs[0].ticket) || '',
      link: (window.__qrs && window.__qrs[0] && window.__qrs[0].link) || ''
    }))()`);
    check('登录页为纯微信扫码（无口令输入框）', !dom.hasPw && dom.hasQr && /微信扫码登录/.test(dom.title + dom.text),
      'title=' + dom.title + ' hasPw=' + dom.hasPw + ' hasQr=' + dom.hasQr);
    check('登录页二维码与票据已生成', !!dom.ticket && !!dom.link, 'ticket=' + dom.ticket.slice(0, 8));

    // 1) 非白名单用户扫码：页面应显示「无权访问」并回显 openid
    if (dom.ticket) {
      await req('http://127.0.0.1:6097/__control?openid=' + encodeURIComponent('wx_browser_denied'));
      const jump = await req(dom.link);
      const cb = jump.headers.get('location');
      if (cb) await req(cb);
      await sleep(2200);
      const denied = await ev(`(() => ({
        visible: getComputedStyle(document.getElementById('deny')).display !== 'none',
        state: document.getElementById('state').textContent,
        oid: document.getElementById('oid').textContent,
        text: document.body.innerText.slice(0, 200)
      }))()`);
      check('浏览器端非白名单显示「无权访问」+ openid',
        denied.visible && /无权访问/.test(denied.state + denied.text) && /wx_browser_denied/.test(denied.oid),
        'state=' + denied.state + ' oid=' + denied.oid.slice(0, 60));
    }

    // 2) 管理员放行这个 openid 后重新扫码：页面应自动进入服务入口
    await postAdmin('/__auth/admin/api/allow', { openid: 'wx_browser_denied', name: '浏览器测试用户' });
    await send('Page.navigate', { url: GATE + '/__auth/login' });
    await sleep(1800);
    const dom2 = await ev(`(() => ({ ticket: (window.__qrs && window.__qrs[window.__qrs.length-1] || {}).ticket || '',
      link: (window.__qrs && window.__qrs[window.__qrs.length-1] || {}).link || '' }))()`);
    let finalUrl = '', finalText = '';
    if (dom2.ticket && dom2.link) {
      await req('http://127.0.0.1:6097/__control?openid=' + encodeURIComponent('wx_browser_denied'));
      const jump2 = await req(dom2.link);
      const cb2 = jump2.headers.get('location');
      if (cb2) await req(cb2);
      await sleep(3200);
      const st = await ev(`(() => ({ url: location.href, text: document.body.innerText.slice(0, 200) }))()`);
      finalUrl = st.url; finalText = st.text;
    }
    check('扫码通过后浏览器自动进入（服务入口页）',
      /__auth\/apps|127\.0\.0\.1:5299\/$/.test(finalUrl) && /影音播放器|服务入口|已通过微信身份验证/.test(finalText),
      'url=' + finalUrl + ' text=' + finalText.slice(0, 60));
    const apiOk = await ev(`(async () => { const r = await fetch('/api/sites'); return { s: r.status, b: await r.text() }; })()`);
    check('浏览器里鉴权后可访问后端 API', apiOk.s === 200 && /gequbao/.test(apiOk.b), 'status=' + apiOk.s);
    const errs = events.filter((e) => e.method === 'Runtime.exceptionThrown');
    check('浏览器控制台零异常', errs.length === 0, 'exceptions=' + errs.length);
  } catch (e) {
    check('浏览器测试执行完成', false, e.message);
  } finally {
    if (ws) { try { ws.close(); } catch (e) {} }
    try { proc.kill(); } catch (e) {}
    await sleep(300);
    try { fs.rmSync(prof, { recursive: true, force: true }); } catch (e) {}
  }
}

/* --------------------------- HTTP 测试 --------------------------- */
async function httpTests() {
  console.log('\n===== A. 拦截 / 微信登录 / 无权访问 / 失败留痕 =====');
  const home = await req(GATE + '/');
  check('未登录访问首页被重定向到登录页', home.status === 302 && /__auth\/login/.test(home.headers.get('location') || ''),
    'status=' + home.status);
  const api = await req(GATE + '/api/sites');
  check('未登录访问 API 返回 401', api.status === 401 && (await api.json()).code === 'AUTH_REQUIRED');
  const login = await req(GATE + '/__auth/login');
  const html = await login.text();
  check('登录页可访问且无任何固定口令入口', login.status === 200 && /微信扫码登录/.test(html)
    && !/type="password"/.test(html) && !/访问口令|localPassword/.test(html));
  const pwGone = await req(GATE + '/__auth/password', { method: 'POST',
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: 'x' }) });
  const cfGone = await req(GATE + '/__auth/confirm');
  check('固定口令接口已移除', pwGone.status === 404 && cfGone.status === 404,
    'password=' + pwGone.status + ' confirm=' + cfGone.status);

  const status = await (await req(GATE + '/__auth/status')).json();
  check('网关状态：微信直登 + 3 个转发服务', status.wechatConfigured === true
    && status.mode === 'wechat-open' && status.routes.length === 3 && !status.loggedIn,
  'mode=' + status.mode + ' routes=' + status.routes.length);

  const qr = await (await req(GATE + '/__auth/qr')).json();
  check('二维码指向微信开放平台授权（mock 地址）', !!qr.ticket && qr.mode === 'wechat-open'
    && qr.qrSvg.includes('<svg') && qr.link.includes('/connect/qrconnect') && qr.link.includes('state='),
  'link=' + String(qr.link).slice(0, 70));

  // 非白名单：模拟微信授权 → 回调 → 明确「无权访问」+ 回显 openid
  await req('http://127.0.0.1:6097/__control?openid=' + encodeURIComponent('wx_denied_user_001'));
  const jump = await req(qr.link);
  const cbUrl = jump.headers.get('location');
  check('微信授权页回调地址带 code + state', jump.status === 302 && /code=mockcode/.test(cbUrl || '')
    && /state=/.test(cbUrl || ''), 'location=' + String(cbUrl).slice(0, 90));
  const cb = await req(cbUrl);
  const cbHtml = await cb.text();
  check('非白名单微信用户提示「无权访问」并回显 openid',
    cb.status === 200 && /无权访问/.test(cbHtml) && /wx_denied_user_001/.test(cbHtml));
  const poll = await (await req(GATE + '/__auth/poll?ticket=' + qr.ticket)).json();
  check('轮询返回 denied + openid + 错误码', poll.status === 'denied'
    && poll.code === 'NOT_IN_WHITELIST' && poll.openid === 'wx_denied_user_001', JSON.stringify(poll).slice(0, 130));
  const rec = readRec().find((r) => r.openid === 'wx_denied_user_001' && r.type === 'failure');
  check('授权失败写入 records.json（含原因 / 来源 / IP / 票据）',
    !!rec && rec.reason === 'not_whitelisted' && rec.src === 'wechat-open' && !!rec.ip && !!rec.ticket,
    rec ? 'reason=' + rec.reason + ' src=' + rec.src : 'not found');
  check('未授权 openid 自动进入 whitelist.pending（便于手动放行）',
    readWL().pending.some((p) => p.openid === 'wx_denied_user_001'));

  // 微信接口报错也要留痕
  await req('http://127.0.0.1:6097/__control?openid=__badcode__');
  const qr2 = await (await req(GATE + '/__auth/qr')).json();
  const jump2 = await req(qr2.link);
  const badCb = await req(jump2.headers.get('location'));
  check('微信接口报错时回调给出明确失败', badCb.status === 200 && /验证未通过/.test(await badCb.text()));
  check('微信接口失败也写入授权记录（含 errcode）',
    readRec().some((r) => r.reason === 'wechat_api_error' && String(r.errcode) === '40029'));

  console.log('\n===== B. 管理端：账号 / 授权记录 / 放行 =====');
  const adminPage = await req(GATE + '/__auth/admin');
  const adminHtml = await adminPage.text();
  check('管理端页面可访问（含登录与授权记录界面）', adminPage.status === 200
    && /网关管理端/.test(adminHtml) && /授权记录/.test(adminHtml) && /管理员账号/.test(adminHtml));
  const unauth = await getAdmin('/__auth/admin/api/overview');
  check('未登录管理端 API 返回 401', unauth.status === 401 && unauth.data.code === 'ADMIN_REQUIRED');
  const badLogin = await req(GATE + '/__auth/admin/login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: 'admin', password: 'wrong' }) });
  check('错误管理员密码被拒绝并记录', badLogin.status === 401
    && readRec().some((r) => r.reason === 'bad_password'));
  const okLogin = await req(GATE + '/__auth/admin/login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: ADMIN_PW }) });
  adminCookie = cookieOf(okLogin);
  check('管理员账号登录成功并下发独立会话', okLogin.status === 200 && (await okLogin.json()).ok
    && adminCookie.startsWith('mp_admin='), 'cookie=' + adminCookie.slice(0, 22));
  check('明文密码已转 scrypt 哈希写回配置', (() => {
    const c = JSON.parse(fs.readFileSync(CFG_FILE, 'utf8'));
    const a = (c.admins || [])[0] || {};
    return !a.password && !!a.passwordHash && !!a.salt && a.passwordHash.length === 64;
  })());

  const me = await getAdmin('/__auth/admin/api/me');
  check('管理端 me 返回管理员 / 登录方式 / 文件路径', me.data.loggedIn === true
    && me.data.username === 'admin' && me.data.mode === 'wechat-open' && !!me.data.files.records);
  const ov = await getAdmin('/__auth/admin/api/overview');
  check('概览统计（待授权 / 失败 / 成功）正确', ov.data.ok === true
    && ov.data.pending >= 1 && ov.data.failures >= 1, JSON.stringify(ov.data));
  const records = await getAdmin('/__auth/admin/api/records?type=failure');
  check('授权记录可按「仅失败」筛选', records.data.ok === true
    && records.data.records.length >= 2 && records.data.records.every((r) => r.type === 'failure'));
  const routes = await getAdmin('/__auth/admin/api/routes');
  check('管理端可查看全部转发服务', routes.data.ok === true && routes.data.routes.length === 3
    && routes.data.routes.some((r) => r.prefix === '/demo' && r.stripPrefix === true));
  const allow = await postAdmin('/__auth/admin/api/allow', { openid: 'wx_denied_user_001', name: '手动放行用户' });
  check('管理端一键放行白名单', allow.data.ok === true
    && readWL().users.some((u) => u.openid === 'wx_denied_user_001')
    && !readWL().pending.some((p) => p.openid === 'wx_denied_user_001'));
  check('放行动作本身也记入授权记录',
    readRec().some((r) => r.action === 'admin-allow' && r.openid === 'wx_denied_user_001'));

  console.log('\n===== C. 白名单通过后扫码登录 + 多转发 =====');
  await req('http://127.0.0.1:6097/__control?openid=' + encodeURIComponent('wx_denied_user_001'));
  const qr3 = await (await req(GATE + '/__auth/qr')).json();
  const jump3 = await req(qr3.link);
  const okCb = await req(jump3.headers.get('location'));
  check('白名单用户微信回调显示验证通过', okCb.status === 200 && /验证通过/.test(await okCb.text()));
  const poll3 = await req(GATE + '/__auth/poll?ticket=' + qr3.ticket);
  const poll3Data = await poll3.json();
  const userCookie = cookieOf(poll3);
  check('轮询拿到会话 cookie 且返回多服务落地页', poll3Data.status === 'ok'
    && poll3Data.next === '/__auth/apps' && userCookie.startsWith('mp_gate='), 'next=' + poll3Data.next);
  check('授权成功写入记录', readRec().some((r) => r.type === 'success' && r.openid === 'wx_denied_user_001'));

  const H = { Cookie: userCookie };
  const idx = await req(GATE + '/', { headers: H });
  check('鉴权后首页透明转发到主后端', idx.status === 200 && /MAIN_BACKEND_OK/.test(await idx.text())
    && idx.headers.get('x-backend') === 'main');
  const sites = await req(GATE + '/api/sites', { headers: H });
  check('鉴权后 API 转发正常', sites.status === 200 && /gequbao/.test(await sites.text()));
  const range = await req(GATE + '/api/stream?file=x.mp3', { headers: Object.assign({ Range: 'bytes=0-3' }, H) });
  check('Range 流式请求保真（206 + Content-Range）', range.status === 206
    && range.headers.get('content-range') === 'bytes 0-3/100' && (await range.text()) === 'ABCD',
  'status=' + range.status + ' range=' + range.headers.get('content-range'));
  const post = await req(GATE + '/api/echo', { method: 'POST', headers: Object.assign({ 'Content-Type': 'application/json' }, H),
    body: JSON.stringify({ hello: 'world' }) });
  const postData = await post.json();
  check('POST 请求体保真 + 注入用户身份头', post.status === 200 && /hello/.test(postData.body)
    && postData.openidHeader === 'wx_denied_user_001' && !!postData.routeHeader, JSON.stringify(postData).slice(0, 130));

  const demo = await req(GATE + '/demo/ping?x=1', { headers: H });
  const demoData = await demo.json();
  check('第二转发服务生效且路径前缀已剥离', demo.status === 200 && demoData.backend === 'demo'
    && demoData.path === '/ping?x=1' && decodeURIComponent(demoData.route) === '示例服务（按需启用）',
  JSON.stringify(demoData));
  const hostRoute = await rawHttp(5299, '/', { host: 'demo.local', cookie: userCookie });
  check('按 Host 匹配第三个转发服务', hostRoute.status === 200 && /"backend":"demo"/.test(hostRoute.body),
    'status=' + hostRoute.status + ' body=' + hostRoute.body.slice(0, 70));
  const appsPage = await req(GATE + '/__auth/apps', { headers: H });
  check('服务入口页列出多个服务', appsPage.status === 200 && /影音播放器/.test(await appsPage.text()));

  // 小程序端：code → 无权访问 / 通过后签发 token
  const mp1 = await req(GATE + '/__auth/mp-login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'mini-code-1' }) });
  const mp1d = await mp1.json();
  check('小程序非白名单返回「无权访问」+ openid', mp1.status === 403
    && mp1d.code === 'NOT_IN_WHITELIST' && /无权访问/.test(mp1d.error) && mp1d.openid === 'wx_mini_user_001');
  await postAdmin('/__auth/admin/api/allow', { openid: 'wx_mini_user_001', name: '小程序用户' });
  const mp2 = await req(GATE + '/__auth/mp-login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'mini-code-2' }) });
  const mp2d = await mp2.json();
  check('小程序白名单用户通过并签发 token', mp2.status === 200 && mp2d.ok === true && !!mp2d.token);
  const bearer = await req(GATE + '/api/sites', { headers: { Authorization: 'Bearer ' + mp2d.token } });
  check('小程序 token（Authorization 头）可访问后端', bearer.status === 200 && /gequbao/.test(await bearer.text()));

  // 停用后已签发会话立即失效
  const revoke = await postAdmin('/__auth/admin/api/revoke', { openid: 'wx_denied_user_001' });
  const after = await req(GATE + '/api/sites', { headers: H });
  check('停用白名单用户后旧会话立即失效', revoke.data.ok === true && after.status === 401,
    'after=' + after.status);
  check('停用动作记入授权记录', readRec().some((r) => r.action === 'admin-revoke'));
  const clear = await postAdmin('/__auth/admin/api/records/clear', {});
  check('管理端可清空授权记录', clear.data.ok === true && readRec().length <= 1);
  const pwChange = await postAdmin('/__auth/admin/api/password',
    { oldPassword: ADMIN_PW, newPassword: 'NewAdmin#2026' });
  check('管理端可自助修改管理员密码', pwChange.data.ok === true, JSON.stringify(pwChange.data));
  const oldLogin = await req(GATE + '/__auth/admin/login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: ADMIN_PW }) });
  const newLogin = await req(GATE + '/__auth/admin/login', { method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: 'NewAdmin#2026' }) });
  check('改密后旧密码失效、新密码可登录', oldLogin.status === 401 && newLogin.status === 200,
    'old=' + oldLogin.status + ' new=' + newLogin.status);
  const logout = await req(GATE + '/__auth/admin/logout');
  check('管理端退出登录清除会话', logout.status === 302
    && /mp_admin=;/.test(String(logout.headers.get('set-cookie') || '')));
}

/* --------------------------- 启动 / 收尾 --------------------------- */
let gateway = null;
const servers = [];
async function main() {
  const tmpCfg = {
    port: 5299, host: '127.0.0.1',
    upstreams: [
      { name: '影音播放器', prefix: '/', host: '127.0.0.1', port: 6095 },
      { name: '示例服务（按需启用）', prefix: '/demo', host: '127.0.0.1', port: 6096, stripPrefix: true },
      { name: 'Host 专用服务', prefix: '/', host: '127.0.0.1', port: 6096, hosts: ['demo.local'] },
    ],
    enabled: true, mode: 'wechat-open', publicBaseUrl: GATE,
    whitelistFile: WL_TMP, recordFile: REC_TMP,
    sessionHours: 168, adminSessionHours: 12, secret: 'gwtest-fixed-secret',
    admins: [{ username: 'admin', password: ADMIN_PW }],
    wechat: { openAppId: 'wx_mock_open_appid', openAppSecret: 'wx_mock_open_secret',
      miniAppId: 'wx_mock_mini_appid', miniAppSecret: 'wx_mock_mini_secret',
      apiBase: 'http://127.0.0.1:6097', authorizeBase: 'http://127.0.0.1:6097' },
  };
  fs.writeFileSync(WL_TMP, JSON.stringify({ users: [], pending: [] }, null, 2) + '\n', 'utf8');
  fs.writeFileSync(REC_TMP, '[]\n', 'utf8');
  fs.writeFileSync(CFG_FILE, JSON.stringify(tmpCfg, null, 2) + '\n', 'utf8');

  await Promise.all([
    new Promise((r) => wxMock.listen(6097, '127.0.0.1', () => { servers.push(wxMock); r(); })),
    new Promise((r) => mainMock.listen(6095, '127.0.0.1', () => { servers.push(mainMock); r(); })),
    new Promise((r) => demoMock.listen(6096, '127.0.0.1', () => { servers.push(demoMock); r(); })),
  ]);
  gateway = spawn(process.execPath, [path.join(GWDIR, 'gateway.js')],
    { cwd: ROOT, stdio: ['ignore', 'pipe', 'pipe'] });
  let gwLog = '';
  gateway.stdout.on('data', (c) => { gwLog += c; });
  gateway.stderr.on('data', (c) => { gwLog += c; });
  let ready = false;
  for (let i = 0; i < 50; i++) {
    try { const r = await req(GATE + '/__auth/status'); if (r.status === 200) { ready = true; break; } } catch (e) {}
    await sleep(300);
  }
  if (!ready) {
    console.error('网关未启动，日志：\n' + gwLog.slice(0, 1200));
    check('网关启动', false);
    return;
  }
  console.log('===== 网关已就绪（5299）=====');
  await httpTests();
  await browserTests();
  console.log('\n===== 汇总 =====');
  console.log('PASS ' + pass + ' / FAIL ' + fail);
  console.log('RESULT: ' + (fail === 0 ? 'PASS 多转发 / 微信直登 / 无权访问 / 授权留痕 / 管理端 全部通过' : 'FAIL'));
}
main().catch((e) => { console.error('FATAL', e); fail++; }).finally(async () => {
  if (gateway) { try { gateway.kill(); } catch (e) {} }
  servers.forEach((s) => { try { s.close(); } catch (e) {} });
  if (BACKUP.cfg !== null) fs.writeFileSync(CFG_FILE, BACKUP.cfg, 'utf8');
  if (BACKUP.wl !== null) fs.writeFileSync(path.join(GWDIR, 'whitelist.json'), BACKUP.wl, 'utf8');
  [WL_TMP, REC_TMP].forEach((f) => { try { fs.rmSync(f, { force: true }); } catch (e) {} });
  await sleep(200);
  process.exit(fail ? 1 : 0);
});
