'use strict';
/** 未配置微信凭证时的行为：不提供任何固定口令 / 绕过通道，登录页只给配置指引，管理端仍可用。 */
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const ROOT = path.resolve(__dirname, '..');
const CFG = path.join(ROOT, 'auth-gateway', 'config.json');
const WL = path.join(ROOT, '.tmp', 'none-wl.json');
const REC = path.join(ROOT, '.tmp', 'none-rec.json');
const GATE = 'http://127.0.0.1:5201';
const backup = fs.readFileSync(CFG, 'utf8');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0;
const check = (n, ok, x) => { console.log((ok ? '  PASS ' : '  FAIL ') + n + (x ? ' | ' + x : '')); ok ? pass++ : fail++; };
const req = (u, o = {}) => fetch(u, Object.assign({ redirect: 'manual' }, o));
let proc = null;
(async () => {
  try {
    fs.writeFileSync(WL, JSON.stringify({ users: [], pending: [] }) + '\n', 'utf8');
    fs.writeFileSync(REC, '[]\n', 'utf8');
    fs.writeFileSync(CFG, JSON.stringify({
      port: 5201, host: '127.0.0.1',
      upstreams: [{ name: '播放器', prefix: '/', host: '127.0.0.1', port: 6999 }],
      enabled: true, mode: 'auto', publicBaseUrl: '', whitelistFile: WL, recordFile: REC,
      secret: 'none-test-secret', admins: [{ username: 'admin', password: 'NoneTest#2026' }],
      wechat: { openAppId: '', openAppSecret: '', mpAppId: '', mpAppSecret: '',
        miniAppId: '', miniAppSecret: '' },
    }, null, 2) + '\n', 'utf8');
    proc = spawn(process.execPath, [path.join(ROOT, 'auth-gateway', 'gateway.js')],
      { cwd: ROOT, stdio: ['ignore', 'pipe', 'pipe'] });
    let log = '';
    proc.stdout.on('data', (c) => { log += c; });
    proc.stderr.on('data', (c) => { log += c; });
    let ready = false;
    for (let i = 0; i < 40; i++) {
      try { if ((await req(GATE + '/__auth/status')).status === 200) { ready = true; break; } } catch (e) {}
      await sleep(300);
    }
    check('网关在未配置凭证时也能启动', ready, ready ? '' : log.slice(0, 300));
    if (!ready) return;
    const st = await (await req(GATE + '/__auth/status')).json();
    check('状态明确标记微信未配置、mode=none', st.wechatConfigured === false && st.mode === 'none');
    const qr = await req(GATE + '/__auth/qr');
    const qd = await qr.json();
    check('二维码接口拒绝生成并给出配置指引', qr.status === 503 && qd.configured === false
      && /config.json/.test(qd.error || ''), 'status=' + qr.status);
    const login = await (await req(GATE + '/__auth/login')).text();
    check('登录页没有口令输入框（无绕过通道）', /微信扫码登录/.test(login) && !/type="password"/.test(login));
    const pw = await req(GATE + '/__auth/password', { method: 'POST',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: 'anything' }) });
    check('不存在固定口令登录接口', pw.status === 404);
    const api = await req(GATE + '/api/sites');
    check('未授权 API 仍被拦截', api.status === 401);
    const admin = await req(GATE + '/__auth/admin');
    check('管理端仍可访问（可先配置白名单）', admin.status === 200 && /网关管理端/.test(await admin.text()));
    const adminOk = await req(GATE + '/__auth/admin/login', { method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: 'NoneTest#2026' }) });
    check('管理员账号独立于微信凭证可用', adminOk.status === 200);
    check('启动日志提示配置微信凭证', /尚未配置微信登录凭证/.test(log));
  } catch (e) {
    check('执行完成', false, e.message);
  } finally {
    if (proc) { try { proc.kill(); } catch (e) {} }
    fs.writeFileSync(CFG, backup, 'utf8');
    [WL, REC].forEach((f) => { try { fs.rmSync(f, { force: true }); } catch (e) {} });
    await sleep(200);
    console.log('RESULT: ' + (fail === 0 ? 'PASS 无凭证时零绕过、管理端可用' : 'FAIL') + ' (' + pass + '/' + (pass + fail) + ')');
    process.exit(fail ? 1 : 0);
  }
})();
