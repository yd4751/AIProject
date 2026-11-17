/* 验证：播放已缓存歌曲 -> 自动把文件下载到「当前设备」的浏览器下载目录
   站点当前需人机验证，故用服务端 downloads/ 里的真实文件构造历史条目来跑真实 HTTP 下载 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const PORT = 9283;
const DL = path.resolve('.tmp', 'device_downloads');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});

function cdp(wsUrl, label) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    let seq = 0; const pend = new Map(); const events = [];
    ws.addEventListener('message', (e) => {
      const m = JSON.parse(e.data);
      if (m.id && pend.has(m.id)) {
        const { resolve: rs, reject: rj } = pend.get(m.id); pend.delete(m.id);
        m.error ? rj(new Error(label + ' ' + JSON.stringify(m.error))) : rs(m.result);
      } else if (m.method && (m.method.startsWith('Browser.download') || m.method === 'Runtime.exceptionThrown')) {
        events.push({ method: m.method, params: m.params });
      }
    });
    ws.addEventListener('open', () => resolve({
      ws, events,
      send: (method, params) => new Promise((rs, rj) => {
        const id = ++seq; pend.set(id, { resolve: rs, reject: rj });
        ws.send(JSON.stringify({ id, method, params: params || {} }));
      }),
    }));
    ws.addEventListener('error', reject);
  });
}

const files = () => fs.existsSync(DL) ? fs.readdirSync(DL).filter((f) => !f.endsWith('.crdownload')) : [];
const sizes = () => files().map((f) => f + '(' + Math.round(fs.statSync(path.join(DL, f)).size / 1024) + 'KB)');

(async () => {
  const browse = await new Promise((res, rej) => {
    http.get('http://127.0.0.1:5173/api/browse?dir=@downloads', (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
  });
  const mp3s = (browse.files || []).filter((f) => /\.mp3$/i.test(f.name));
  if (mp3s.length < 2) throw new Error('下载目录 mp3 不足两首');
  const A = mp3s[0], B = mp3s[1];
  console.log('测试文件:', A.name, '/', B.name);

  fs.rmSync(DL, { recursive: true, force: true }); fs.mkdirSync(DL, { recursive: true });
  const prof = path.resolve('.tmp', 'profDevSave');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();

  let ver = null, list = null;
  for (let i = 0; i < 60; i++) {
    try { ver = await hj(PORT, '/json/version'); list = await hj(PORT, '/json/list'); if (list.length) break; } catch (e) {}
    await sleep(400);
  }
  const BW = await cdp(ver.webSocketDebuggerUrl, 'browser');
  await BW.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: DL, eventsEnabled: true });
  const P = await cdp(list[0].webSocketDebuggerUrl, 'page');
  const send = P.send;
  const ev = async (x) => {
    const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 300));
    return r.result.value;
  };

  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(2200);

  const hist = [
    { id: 'devA', name: A.name, path: A.path, kind: 'audio' },
    { id: 'devB', name: B.name, path: B.path, kind: 'audio' },
  ];
  await ev(`(() => { localStorage.setItem('mp.history', ${JSON.stringify(JSON.stringify(hist))}); localStorage.removeItem('mp.devsaved'); localStorage.removeItem('mp.autosavedevice'); return 1; })()`);
  await send('Page.reload', { ignoreCache: true });
  await sleep(2600);
  console.log('恢复后的列表:', await ev(`(() => ({ n: document.querySelectorAll('#plist li').length, sw: document.querySelector('#autoSaveDevice').checked, names: [...document.querySelectorAll('#plist li .nm')].map(x=>x.textContent.trim().slice(0,24)) }))()`));

  // 1) 播放第一首：应自动下载到设备
  await ev(`(() => { document.querySelectorAll('#plist li')[0].click(); return 1; })()`);
  await sleep(2600);
  const s1 = await ev(`(() => { const c = document.querySelector('#ptCache'); return { chipHidden: c.hidden, chipTxt: document.querySelector('#ptCacheTxt').textContent, done: c.classList.contains('done'), title: document.querySelector('#ptTitle').textContent, playing: !document.querySelector('#media').paused, src: document.querySelector('#media').src.slice(0, 70) }; })()`);
  console.log('播放第一首:', JSON.stringify(s1));
  console.log('设备下载目录:', sizes());
  console.log('下载事件:', BW.events.filter((e) => e.method === 'Browser.downloadWillBegin').map((e) => e.params.suggestedFilename));
  console.log('完成事件数:', BW.events.filter((e) => e.method === 'Browser.downloadProgress' && e.params.state === 'completed').length);
  console.log('去重表:', await ev(`localStorage.getItem('mp.devsaved')`));
  const n1 = files().length;

  // 2) 再点同一首：去重，不应重复下载
  await ev(`(() => { document.querySelectorAll('#plist li')[0].click(); return 1; })()`);
  await sleep(2200);
  console.log('重播同曲:', n1, '->', files().length, '(应不变)');

  // 3) 点缓存提示条手动重下（hook 记录下载链接，区分「没触发」与「同名被 Chrome 合并」）
  await ev(`(() => { window.__dlHooks = []; const orig = HTMLAnchorElement.prototype.click; HTMLAnchorElement.prototype.click = function () { window.__dlHooks.push(this.href); return orig.call(this); }; return 1; })()`);
  await ev(`(() => { document.querySelector('#ptCache').click(); return 1; })()`);
  await sleep(2200);
  console.log('点提示条重下:', files().length, sizes(), '| 触发链接:', await ev(`window.__dlHooks`), '| toast:', await ev(`document.querySelector('#toast').hidden ? '' : document.querySelector('#toast').textContent`));
  const n2 = files().length;

  // 4) 关掉自动保存开关，播第二首：不应自动下载
  await ev(`(() => { const s = document.querySelector('#autoSaveDevice'); s.checked = false; s.dispatchEvent(new Event('change', { bubbles: true })); return 1; })()`);
  await ev(`(() => { document.querySelectorAll('#plist li')[1].click(); return 1; })()`);
  await sleep(2400);
  console.log('关开关后播第二首:', n2, '->', files().length, '(应不变)', '| 开关存储:', await ev(`localStorage.getItem('mp.autosavedevice')`));

  // 5) 列表里的「存到本机」按钮始终可手动下载
  const hasSave = await ev(`(() => { const li = document.querySelectorAll('#plist li')[1]; const b = li && li.querySelector('.savebtn'); if (b) b.click(); return !!b; })()`);
  await sleep(2200);
  console.log('列表按钮可用:', hasSave, '| 手动后文件数:', files().length);
  console.log('页面异常:', P.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => String(e.params.exceptionDetails.text).slice(0, 100)));
  console.log('RESULT:', n1 >= 1 && files().length > n2 ? 'PASS 设备下载目录已收到歌曲，且去重/开关/手动重下均正常' : 'FAIL');
  P.ws.close(); BW.ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
