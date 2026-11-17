'use strict';
/** 空目录运行 dist/auth-gateway.exe：验证内嵌资源释放、初始管理员密码、页面、配置驱动端口与多转发。 */
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const ROOT = path.resolve(__dirname, '..');
const EXE = path.join(ROOT, 'dist', 'auth-gateway.exe');
const RUN = path.join(ROOT, '.tmp', 'gw_exe_run');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0;
const check = (n, ok, x) => { console.log((ok ? '  PASS ' : '  FAIL ') + n + (x ? ' | ' + x : '')); ok ? pass++ : fail++; };
const req = (u, o = {}) => fetch(u, Object.assign({ redirect: 'manual' }, o));
let proc = null;
async function start(port) {
  let log = '';
  proc = spawn(path.join(RUN, 'auth-gateway.exe'), [], { cwd: RUN, stdio: ['ignore', 'pipe', 'pipe'] });
  proc.stdout.on('data', (c) => { log += c; });
  proc.stderr.on('data', (c) => { log += c; });
  let ok = false;
  for (let i = 0; i < 60; i++) {
    try { if ((await req('http://127.0.0.1:' + port + '/__auth/status')).status === 200) { ok = true; break; } } catch (e) {}
    await sleep(300);
  }
  return { ok, log };
}
async function stop() {
  if (proc) { try { proc.kill(); } catch (e) {} proc = null; }
  await sleep(500);
}
(async () => {
  try {
    fs.rmSync(RUN, { recursive: true, force: true });
    fs.mkdirSync(RUN, { recursive: true });
    fs.copyFileSync(EXE, path.join(RUN, 'auth-gateway.exe'));
    check('exe 可单独拷贝到空目录', fs.existsSync(path.join(RUN, 'auth-gateway.exe')));

    const safeLog = (s) => String(s || '').replace(/密码:\s*\S+/g, '密码: ****').slice(0, 400);
    const first = await start(5173);
    check('exe 首次启动并监听 5173', first.ok, first.ok ? '' : safeLog(first.log));
    if (!first.ok) return;

    const cfgPath = path.join(RUN, 'gateway-config.json');
    check('首次运行释放 gateway-config.json / whitelist.json（不与服务端 config.json 冲突）',
      fs.existsSync(cfgPath) && fs.existsSync(path.join(RUN, 'whitelist.json'))
      && !fs.existsSync(path.join(RUN, 'config.json')));
    const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
    check('自动生成签名密钥与管理员哈希（无明文密码落盘）',
      !!cfg.secret && !!cfg.admins[0].passwordHash && !!cfg.admins[0].salt && !cfg.admins[0].password);
    check('内嵌配置已脱敏（未带开发机的密钥/密码）',
      !cfg.admins[0].passwordHash || !/TestAdmin|NewAdmin|NoneTest/.test(JSON.stringify(cfg.admins)));
    const m = first.log.match(/密码:\s*(\S+)/);
    check('启动窗口打印管理员初始密码', !!m);

    const status = await (await req('http://127.0.0.1:5173/__auth/status')).json();
    check('状态接口：默认设备登录 / 微信凭证未配置 / 转发服务可用',
      status.wechatConfigured === false && status.mode === 'device'
      && status.loginMode === 'device' && status.deviceMode === true
      && status.routes.length === 1, 'mode=' + status.mode + ' routes=' + status.routes.length);
    const login = await (await req('http://127.0.0.1:5173/__auth/login')).text();
    check('内嵌登录页为设备登录（机器码 / 无口令 / 无微信入口）',
      /设备登录/.test(login) && /机器码/.test(login)
      && !/微信扫码登录/.test(login) && !/type="password"/.test(login));
    const adminPage = await (await req('http://127.0.0.1:5173/__auth/admin')).text();
    check('内嵌管理端页面正常', /网关管理端/.test(adminPage) && /授权记录/.test(adminPage));
    const home = await req('http://127.0.0.1:5173/');
    check('未登录首页被拦截到登录页', home.status === 302 && /__auth\/login/.test(home.headers.get('location') || ''));
    const logo = await req('http://127.0.0.1:5173/__auth/logo');
    check('内嵌 SVG logo 正常', logo.status === 200 && /image\/svg\+xml/.test(logo.headers.get('content-type') || ''));

    const dl = await req('http://127.0.0.1:5173/__auth/device/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: 'exe-device-001', fp: 'exe-fp', label: 'exe 测试设备' }) });
    const dld = await dl.json();
    check('exe 内未授权设备返回「无权访问 + 机器码」',
      dl.status === 403 && dld.ok === false && /无权访问/.test(dld.error || '') && !!dld.machineCode,
      dld.machineCode || dld.error);
    const qrOff = await req('http://127.0.0.1:5173/__auth/qr');
    const qrOffData = await qrOff.json();
    check('exe 内微信扫码 / 小程序码入口被彻底屏蔽',
      qrOff.status === 403 && qrOffData.code === 'WECHAT_DISABLED', 'status=' + qrOff.status);

    if (m) {
      const r = await req('http://127.0.0.1:5173/__auth/admin/login', { method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: 'admin', password: m[1] }) });
      const d = await r.json();
      const ck = String(r.headers.get('set-cookie') || '').split(';')[0];
      check('用初始密码可登录 exe 的管理端', r.status === 200 && d.ok === true && ck.startsWith('mp_admin='));
      const me = await req('http://127.0.0.1:5173/__auth/admin/api/me', { headers: { Cookie: ck } });
      const meData = await me.json();
      check('管理端 API 在 exe 中可用', me.status === 200 && meData.loggedIn === true);
      check('管理员登录写入 records.json', fs.existsSync(path.join(RUN, 'records.json'))
        && JSON.parse(fs.readFileSync(path.join(RUN, 'records.json'), 'utf8')).length >= 1);
    }
    await stop();

    // 改 config.json：端口 + 第二个转发服务，重启验证配置驱动
    cfg.port = 5299;
    cfg.upstreams.push({ name: '第二服务', prefix: '/demo', host: '127.0.0.1', port: 6998,
      stripPrefix: true, enabled: true });
    fs.writeFileSync(cfgPath, JSON.stringify(cfg, null, 2) + '\n', 'utf8');
    const second = await start(5299);
    check('改 gateway-config.json 端口后重启生效', second.ok, second.ok ? '' : safeLog(second.log));
    if (second.ok) {
      const st2 = await (await req('http://127.0.0.1:5299/__auth/status')).json();
      check('新增转发服务被加载', st2.routes.length === 2 && st2.routes.some((r) => r.prefix === '/demo'),
        'routes=' + st2.routes.length);
      const routes = await req('http://127.0.0.1:5299/__auth/admin/api/routes');
      check('管理端 API 未登录仍返回 401', routes.status === 401);
    }
    await stop();
  } catch (e) {
    check('执行完成', false, e.message);
  } finally {
    await stop();
    try { fs.rmSync(RUN, { recursive: true, force: true }); } catch (e) {}
    console.log('RESULT: ' + (fail === 0 ? 'PASS 网关 exe 空目录运行、配置驱动、管理端可用' : 'FAIL') + ' (' + pass + '/' + (pass + fail) + ')');
    process.exit(fail ? 1 : 0);
  }
})();
