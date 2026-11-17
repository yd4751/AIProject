'use strict';
/** 验证重新打包的网关 exe 里已带上「个人主体小程序码」新指引（空目录、隔离端口） */
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const RUN = path.join(ROOT, '.tmp', 'gw_hint_run');
const EXE = path.join(ROOT, 'dist', 'auth-gateway.exe');
const GATE = 'http://127.0.0.1:5297';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let proc = null, pass = 0, fail = 0, log = '';
function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + extra : ''));
  ok ? pass++ : fail++;
}

async function main() {
  fs.rmSync(RUN, { recursive: true, force: true });
  fs.mkdirSync(RUN, { recursive: true });
  // exe 按「自身所在目录」找配置，必须先拷进空目录
  const localExe = path.join(RUN, 'auth-gateway.exe');
  fs.copyFileSync(EXE, localExe);
  fs.writeFileSync(path.join(RUN, 'gateway-config.json'), JSON.stringify({
    port: 5297, host: '127.0.0.1',
    upstreams: [{ name: 'backend', prefix: '/', host: '127.0.0.1', port: 6001, enabled: true }],
    enabled: true, mode: 'auto', publicBaseUrl: '', lanBaseUrl: '',
    whitelistFile: 'whitelist.json', recordFile: 'records.json',
    sessionHours: 1, adminSessionHours: 1, secret: 'hint-test-secret',
    allowTokenInQuery: true, corsOrigins: [], landing: '', admins: [],
    wechat: { openAppId: '', openAppSecret: '', mpAppId: '', mpAppSecret: '',
      miniAppId: '', miniAppSecret: '', miniPage: 'pages/gate/index', miniEnv: 'release',
      apiBase: 'https://api.weixin.qq.com', authorizeBase: 'https://open.weixin.qq.com' },
  }, null, 2));

  proc = spawn(localExe, [], { cwd: RUN, stdio: ['ignore', 'pipe', 'pipe'] });
  proc.stdout.on('data', (c) => log += c.toString());
  proc.stderr.on('data', (c) => log += c.toString());

  let ready = false;
  for (let i = 0; i < 40; i++) {
    try { const r = await fetch(GATE + '/__auth/status'); if (r.ok) { ready = true; break; } } catch (e) { /* 等 */ }
    await sleep(250);
  }
  check('exe 在空目录按 gateway-config.json 启动', ready, ready ? '' : log.slice(0, 260));
  if (!ready) throw new Error('exe not ready');

  const st = await (await fetch(GATE + '/__auth/status')).json();
  check('未配凭证时 mode=none（无绕过）', st.mode === 'none' && st.wechatConfigured === false);

  const qr = await fetch(GATE + '/__auth/qr');
  const qd = await qr.json();
  check('二维码接口指向个人可用的小程序凭证', qr.status === 503 && /miniAppId/.test(qd.error || ''));
  check('未配置提示明确「个人开发者 / 无需公章」',
    /个人开发者/.test(qd.hint || '') && /无需公章/.test(qd.hint || ''), 'hint=' + (qd.hint || ''));

  const login = await (await fetch(GATE + '/__auth/login')).text();
  check('登录页无口令输入框', !/type="password"/.test(login));
  const admin = await (await fetch(GATE + '/__auth/admin')).text();
  check('管理端关于页带个人主体接入指引', /个人开发者接入/.test(admin) && /小程序/.test(admin));

  check('exe 运行期零异常', !/Unhandled|node:internal|EADDRINUSE/.test(log), 'log=' + log.length + 'B');
}

main()
  .then(() => console.log('RESULT: ' + (fail === 0 ? 'PASS ' : 'FAIL ') + pass + '/' + (pass + fail)))
  .catch((e) => { console.log('FATAL ' + e.message); fail++; console.log('RESULT: FAIL'); })
  .finally(() => {
    if (proc) { try { proc.kill(); } catch (e) { /* 忽略 */ } }
    setTimeout(() => {
      try { fs.rmSync(RUN, { recursive: true, force: true }); } catch (e) { /* 忽略 */ }
      process.exit(fail === 0 ? 0 : 1);
    }, 250);
  });
