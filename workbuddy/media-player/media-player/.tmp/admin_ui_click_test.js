'use strict';
/**
 * 管理端「转发服务」真实浏览器点击验证（完全隔离）
 *  - 独立端口起网关 + mock 后端，独立 Chrome profile / CDP 端口
 *  - 真实点击：登录 → 转发服务标签 → 编辑 → 改值 → 保存 → 停用 → 新增
 *  - 收集页面 JS 异常，验证 DOM 与 config.json 同步
 */
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const GWDIR = path.join(ROOT, 'auth-gateway');
const TMP = path.join(__dirname, 'admin_ui_case');
const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const GATE = 'http://127.0.0.1:5321';
const P_BACK = 5322;
const CDP_PORT = 9455;
const ADMIN_PW = 'TestAdmin#2026';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let pass = 0, fail = 0;
function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + String(extra).slice(0, 200) : ''));
  ok ? pass++ : fail++;
}
const cfgPath = path.join(TMP, 'config.json');

function mock(port) {
  const s = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('BACK:' + req.url);
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

let gw = null, back = null, cdp = null, chromeProc = null;
async function launchChrome() {
  const prof = path.join(__dirname, 'admin_ui_profile');
  fs.rmSync(prof, { recursive: true, force: true });
  chromeProc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${CDP_PORT}`, `--user-data-dir=${prof}`,
    '--window-size=1400,900', '--mute-audio', 'about:blank',
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
  const notFound = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) {
      const p = pending.get(m.id); pending.delete(m.id);
      m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result);
    } else if (m.method === 'Runtime.exceptionThrown') {
      errors.push((m.params.exceptionDetails.exception || {}).description || m.params.exceptionDetails.text || 'exception');
    } else if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error'
      && !/401 \(Unauthorized\)|404 \(Not Found\)|favicon/.test(m.params.entry.text)) {
      errors.push(m.params.entry.text);
    } else if (m.method === 'Network.responseReceived' && m.params.response.status === 404) {
      notFound.push(m.params.response.url);
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
  cdp = { ws, send, evaluate, errors, notFound, close: () => { try { ws.close(); } catch (e) {} } };
  await send('Page.enable'); await send('Runtime.enable'); await send('Log.enable'); await send('Network.enable');
}

(async () => {
  fs.rmSync(TMP, { recursive: true, force: true });
  fs.mkdirSync(TMP, { recursive: true });
  ['gateway.js', 'login-page.js', 'device-login-page.js', 'admin-page.js'].forEach((f) =>
    fs.copyFileSync(path.join(GWDIR, f), path.join(TMP, f)));
  fs.writeFileSync(path.join(TMP, 'whitelist.json'), JSON.stringify({ users: [], pending: [] }));
  fs.writeFileSync(path.join(TMP, 'records.json'), '[]');
  fs.writeFileSync(cfgPath, JSON.stringify({
    port: 5321, host: '127.0.0.1', enabled: false, mode: 'auto',
    secret: 'fixed-test-secret-admin-ui-0123456789',
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
  if (!await waitPort(GATE + '/__auth/status')) throw new Error('网关启动失败: ' + glog.slice(-600));

  await launchChrome();
  await cdp.send('Page.navigate', { url: GATE + '/__auth/admin' });
  await sleep(1800);

  const E = cdp.evaluate;
  check('页面脚本已执行（登录框或主界面可见）', await E(
    "!document.getElementById('login').classList.contains('hidden')"
    + " || !document.getElementById('app').classList.contains('hidden')"));
  check('管理端页面加载出登录框', await E("!document.getElementById('login').classList.contains('hidden')"));

  await E(`document.getElementById('u').value='admin';document.getElementById('p').value=${JSON.stringify(ADMIN_PW)};document.getElementById('loginBtn').click();true`);
  await sleep(1200);
  check('管理员登录成功并进入主界面', await E("!document.getElementById('app').classList.contains('hidden')"),
    await E("document.getElementById('who').textContent"));

  // 本次报障点：/api//records 双斜杠 404，修复后新旧两种路径都要 200
  const rs = await E("(async()=>{const a=await fetch('/__auth/admin/api//records?type=all&q=');"
    + "const b=await fetch('/__auth/admin/api/records?type=all&q=');return a.status+'/'+b.status})()");
  check('授权记录接口双斜杠路径不再 404', rs === '200/200', rs);
  check('授权记录表格正常渲染', await E("!!document.getElementById('records') && !!document.getElementById('rtype')"));

  // 切到「转发服务」标签
  await E(`Array.from(document.querySelectorAll('.tabs button')).find(b=>b.getAttribute('data-tab')==='routes').click();true`);
  await sleep(900);
  check('转发服务标签可切换', await E("!document.getElementById('tab-routes').classList.contains('hidden')"));
  check('页面存在新增/保存按钮', await E("!!document.getElementById('rtSave') && !!document.getElementById('rtCancel')"));
  check('列表渲染出编辑按钮', await E("document.querySelectorAll('#routes [data-redit]').length === 1"),
    await E("document.querySelectorAll('#routes tr').length + ' 行'"));

  // 点「编辑」
  await E(`document.querySelector('#routes [data-redit]').click();true`);
  await sleep(250);
  const form = await E(`JSON.stringify({name:rtName.value,prefix:rtPrefix.value,port:rtPort.value,
    saveText:rtSave.textContent,cancelHidden:rtCancel.classList.contains('hidden')})`);
  const f = JSON.parse(form);
  check('点编辑后表单回填原有配置', f.name === '主站' && f.prefix === '/' && String(f.port) === String(P_BACK), form);
  check('按钮切换为「保存修改」且出现取消编辑', f.saveText === '保存修改' && f.cancelHidden === false, form);

  // 改值并保存
  await E(`document.getElementById('rtName').value='主站(已改)';document.getElementById('rtPort').value='5322';document.getElementById('rtSave').click();true`);
  await sleep(1000);
  const msg = await E("document.getElementById('rtMsg').textContent");
  check('保存修改返回成功提示', /已保存修改/.test(msg), msg);
  const onDisk = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
  check('修改写回 config.json', onDisk.upstreams.some((x) => x.name === '主站(已改)'));
  check('列表显示新服务名', await E("document.getElementById('routes').textContent.includes('主站(已改)')"));

  // 停用 / 启用
  await E(`document.querySelector('#routes [data-rtoggle]').click();true`);
  await sleep(800);
  check('停用按钮生效并写盘', JSON.parse(fs.readFileSync(cfgPath, 'utf8')).upstreams[0].enabled === false);
  await E(`document.querySelector('#routes [data-rtoggle]').click();true`);
  await sleep(800);
  check('启用按钮生效并写盘', JSON.parse(fs.readFileSync(cfgPath, 'utf8')).upstreams[0].enabled !== false);

  // 新增服务
  await E(`rtName.value='演示';rtPrefix.value='/demo';rtHost.value='127.0.0.1';rtPort.value='5322';rtSave.click();true`);
  await sleep(900);
  check('在线新增服务成功', JSON.parse(fs.readFileSync(cfgPath, 'utf8')).upstreams.length === 2,
    await E("document.getElementById('rtMsg').textContent"));

  check('页面无 JS 异常', cdp.errors.length === 0, cdp.errors.slice(0, 2).join(' / '));

  // 管理端页面热读：改文件后不重启网关，刷新即可生效
  const pageFile = path.join(TMP, 'admin-page.js');
  const src = fs.readFileSync(pageFile, 'utf8');
  fs.writeFileSync(pageFile, src.replace('<title>网关管理端</title>', '<title>网关管理端 HOT</title>'));
  await cdp.send('Page.navigate', { url: GATE + '/__auth/admin' });
  await sleep(900);
  check('管理端页面改后无需重启网关', await E("document.title.includes('HOT')"), await E('document.title'));

  const api404 = cdp.notFound.filter((u) => u.includes('/__auth/admin/api/'));
  check('管理端 API 全程无 404', api404.length === 0, api404.slice(0, 3).join(' / '));

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
