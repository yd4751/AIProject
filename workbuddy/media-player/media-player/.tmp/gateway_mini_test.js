'use strict';
/**
 * 个人主体小程序码登录链路专项验证（自包含，不依赖真实微信凭证 / 真实播放器服务）
 * 用 mock 微信 API + 临时网关配置，验证 mode=auto 优先选 mini、小程序码生成、
 * scene 票据回传、白名单校验、网页轮询自动登录、转发放行。
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const RUN = path.join(ROOT, '.tmp', 'gwmini');
const GATE = 'http://127.0.0.1:5293';
const WX = 'http://127.0.0.1:6093';
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4//8/AwAI/AL+X6Z7WQAAAABJRU5ErkJggg==', 'base64');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0, proc = null, wxSrv = null, upSrv = null;
const wxCalls = [];

function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + extra : ''));
  ok ? pass++ : fail++;
}
const req = (url, opt = {}) => fetch(url, Object.assign({ redirect: 'manual' }, opt));
const cookieOf = (r) => String(r.headers.get('set-cookie') || '').split(';')[0];

function readRec() {
  try { return JSON.parse(fs.readFileSync(path.join(RUN, 'records.json'), 'utf8')); } catch (e) { return []; }
}
function readWL() {
  try { return JSON.parse(fs.readFileSync(path.join(RUN, 'whitelist.json'), 'utf8')); } catch (e) { return { users: [], pending: [] }; }
}

async function main() {
  fs.rmSync(RUN, { recursive: true, force: true });
  fs.mkdirSync(RUN, { recursive: true });
  for (const f of ['gateway.js', 'login-page.js', 'device-login-page.js', 'admin-page.js', 'store.js', 'iploc.js', 'ipdb.bin']) {
    fs.copyFileSync(path.join(ROOT, 'auth-gateway', f), path.join(RUN, f));
  }

  // 只有 mini 凭证、mode=auto、publicBaseUrl 留空 —— 正是个人开发者的典型配置
  fs.writeFileSync(path.join(RUN, 'config.json'), JSON.stringify({
    port: 5293, host: '127.0.0.1',
    upstreams: [{ name: 'mock', prefix: '/', host: '127.0.0.1', port: 6094, stripPrefix: false, enabled: true }],
    enabled: true, mode: 'auto', publicBaseUrl: '', lanBaseUrl: '',
    whitelistFile: 'whitelist.json', recordFile: 'records.json',
    sessionHours: 1, adminSessionHours: 1, secret: 'mini-test-secret',
    allowTokenInQuery: true, corsOrigins: [], landing: '', admins: [],
    wechat: {
      openAppId: '', openAppSecret: '', mpAppId: '', mpAppSecret: '',
      miniAppId: 'wx_mini_mock', miniAppSecret: 'mini_secret_mock',
      miniPage: 'pages/gate/index', miniEnv: 'trial', apiBase: WX, authorizeBase: WX,
    },
  }, null, 2));
  fs.writeFileSync(path.join(RUN, 'whitelist.json'), JSON.stringify({
    users: [{ openid: 'wx_allowed_001', name: '测试用户', enabled: true }], pending: [],
  }, null, 2));

  wxSrv = http.createServer((rq, rs) => {
    const u = new URL(rq.url, WX);
    let body = '';
    rq.on('data', (c) => body += c);
    rq.on('end', () => {
      wxCalls.push({ path: u.pathname, body });
      if (u.pathname === '/cgi-bin/token') {
        const b = JSON.stringify({ access_token: 'mock_token', expires_in: 7200 });
        rs.writeHead(200, { 'Content-Type': 'application/json' }); return rs.end(b);
      }
      if (u.pathname === '/sns/jscode2session') {
        const code = u.searchParams.get('js_code');
        const b = code === 'allow' ? JSON.stringify({ openid: 'wx_allowed_001', session_key: 'sk' })
          : code === 'deny' ? JSON.stringify({ openid: 'wx_denied_001', session_key: 'sk' })
            : JSON.stringify({ errcode: 40029, errmsg: 'invalid code' });
        rs.writeHead(200, { 'Content-Type': 'application/json' }); return rs.end(b);
      }
      if (u.pathname === '/wxa/getwxacodeunlimit') {
        rs.writeHead(200, { 'Content-Type': 'image/png', 'Content-Length': PNG.length }); return rs.end(PNG);
      }
      rs.writeHead(404); rs.end();
    });
  });
  upSrv = http.createServer((rq, rs) => { rs.writeHead(200, { 'Content-Type': 'text/plain' }); rs.end('MOCK_UPSTREAM:' + rq.url); });
  await Promise.all([
    new Promise((r) => wxSrv.listen(6093, '127.0.0.1', r)),
    new Promise((r) => upSrv.listen(6094, '127.0.0.1', r)),
  ]);

  proc = spawn(process.execPath, [path.join(RUN, 'gateway.js')], { cwd: RUN, stdio: ['ignore', 'pipe', 'pipe'] });
  let log = '';
  proc.stdout.on('data', (c) => log += c.toString());
  proc.stderr.on('data', (c) => log += c.toString());

  let ready = false;
  for (let i = 0; i < 40; i++) {
    try { const r = await req(GATE + '/__auth/status'); if (r.ok) { ready = true; break; } } catch (e) { /* 等 */ }
    await sleep(250);
  }
  check('仅 mini 凭证 + mode=auto 的网关可启动', ready, ready ? '' : log.slice(0, 300));
  if (!ready) throw new Error('gateway not ready');

  const st = await (await req(GATE + '/__auth/status')).json();
  check('auto 自动选择小程序码登录（个人可用路径优先）', st.mode === 'wechat-mini' && st.wechatConfigured === true,
    'mode=' + st.mode);
  check('不配置 publicBaseUrl 也能正常使用微信登录', st.mode === 'wechat-mini' && !/publicBaseUrl/.test(JSON.stringify(st)));

  const qr = await (await req(GATE + '/__auth/qr')).json();
  check('二维码接口返回小程序码地址（非开放平台授权链接）',
    !!qr.ticket && /\/__auth\/wxacode\?ticket=/.test(qr.imageUrl || '') && !qr.link,
    'imageUrl=' + (qr.imageUrl || ''));
  check('扫码文案提示小程序码', /小程序码/.test(qr.scanText || ''));

  const code = await req(GATE + qr.imageUrl);
  const ctype = code.headers.get('content-type') || '';
  const cbuf = Buffer.from(await code.arrayBuffer());
  const sceneCall = wxCalls.find((c) => c.path === '/wxa/getwxacodeunlimit');
  let sceneBody = {};
  try { sceneBody = JSON.parse(sceneCall ? sceneCall.body : '{}'); } catch (e) { /* 忽略 */ }
  check('小程序码接口返回 PNG 图片', code.status === 200 && /image\/png/.test(ctype) && cbuf.length > 0,
    'status=' + code.status + ' bytes=' + cbuf.length);
  check('小程序码把登录票据放进 scene，并带上体验版配置',
    sceneBody.scene === qr.ticket && sceneBody.page === 'pages/gate/index'
    && sceneBody.env_version === 'trial' && sceneBody.check_path === false,
    'scene=' + sceneBody.scene);

  const denied = await req(GATE + '/__auth/mp-login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code: 'deny', ticket: qr.ticket }),
  });
  const dd = await denied.json();
  check('非白名单微信用户明确「无权访问」并回显 openid',
    denied.status === 403 && dd.code === 'NOT_IN_WHITELIST' && dd.openid === 'wx_denied_001');
  check('授权失败留痕，且写入待授权 pending 便于手动放行',
    readRec().some((r) => r.type === 'failure' && r.openid === 'wx_denied_001' && r.reason === 'not_whitelisted')
    && readWL().pending.some((p) => p.openid === 'wx_denied_001'));

  const allowed = await req(GATE + '/__auth/mp-login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code: 'allow', ticket: qr.ticket }),
  });
  const ad = await allowed.json();
  check('白名单用户小程序内校验通过并签发 token', allowed.status === 200 && ad.ok === true && !!ad.token);

  const poll = await req(GATE + '/__auth/poll?ticket=' + encodeURIComponent(qr.ticket));
  const pd = await poll.json();
  check('网页轮询到扫码成功并种下会话 cookie',
    pd.status === 'ok' && !!pd.token && !!cookieOf(poll));

  const home = await req(GATE + '/', { headers: { Cookie: cookieOf(poll) } });
  check('登录后请求被透明转发到后端', home.status === 200 && /MOCK_UPSTREAM/.test(await home.text()));
  const anon = await req(GATE + '/', { redirect: 'manual' });
  check('未登录仍被拦截到登录页', anon.status === 302 && /__auth\/login/.test(anon.headers.get('location') || ''));

  const loginHtml = await (await req(GATE + '/__auth/login')).text();
  check('登录页无口令绕过入口', !/type="password"/.test(loginHtml));
  check('网关运行期零异常', !/Error|Unhandled|ECONNREFUSED/.test(log), 'log=' + log.length + 'B');
}

main()
  .then(() => console.log('RESULT: ' + (fail === 0 ? 'PASS ' : 'FAIL ') + pass + '/' + (pass + fail)))
  .catch((e) => { console.log('FATAL ' + e.message); fail++; console.log('RESULT: FAIL'); })
  .finally(() => {
    if (proc) { try { proc.kill(); } catch (e) { /* 忽略 */ } }
    if (wxSrv) { try { wxSrv.close(); } catch (e) { /* 忽略 */ } }
    if (upSrv) { try { upSrv.close(); } catch (e) { /* 忽略 */ } }
    setTimeout(() => {
      try { fs.rmSync(RUN, { recursive: true, force: true }); } catch (e) { /* 忽略 */ }
      process.exit(fail === 0 ? 0 : 1);
    }, 250);
  });
