/* 验证「服务端缓存成功落地 -> 自动开播 -> 自动存到设备」。
   站点反爬期间，临时加一个 audioUrl 指向本机 stream 的测试站点，跑完恢复 sources.json。 */
const http = require('http'); const fs = require('fs'); const path = require('path'); const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const PORT = 9286;
const DL = path.resolve('.tmp', 'await_downloads');
const SRC = path.resolve('sources.json');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => { http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
function cdp(u) { return new Promise((resolve, reject) => { const ws = new WebSocket(u); let seq = 0; const pend = new Map(); const events = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { const p = pend.get(m.id); pend.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); } else if (m.method) events.push({ method: m.method, params: m.params }); });
  ws.addEventListener('open', () => resolve({ ws, events, send: (method, params) => new Promise((rs, rj) => { const id = ++seq; pend.set(id, { resolve: rs, reject: rj }); ws.send(JSON.stringify({ id, method, params: params || {} })); }) }));
  ws.addEventListener('error', reject); }); }

(async () => {
  const backup = fs.readFileSync(SRC);
  const browse = await new Promise((res, rej) => { http.get('http://127.0.0.1:5173/api/browse?dir=@downloads', (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
  const src = (browse.files || []).find((f) => /\.mp3$/i.test(f.name));
  if (!src) throw new Error('没有可用 mp3');
  const NEW = '自动缓存测试曲目';
  const newFile = NEW + '.mp3';
  const PROBE_ID = 'probe_' + Date.now().toString(36);
  let proc = null, P = null, BW = null;
  const wipeTasks = async () => {
    const j = await new Promise((res) => http.get('http://127.0.0.1:5173/api/site/tasks', (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { res({ tasks: [] }); } }); }).on('error', () => res({ tasks: [] })));
    await Promise.all((j.tasks || []).filter((t) => t.site === 'localtest').map((t) => new Promise((res) => http.get('http://127.0.0.1:5173/api/site/task/remove?id=' + encodeURIComponent(t.id), (r) => { r.resume(); r.on('end', res); }).on('error', res))));
  };

  try {
    await wipeTasks();
    // 临时测试站点：audioUrl 直接指向服务端已有的本地文件（resolveAudio 会识别 audio/mpeg 并返回它）
    const cfg = JSON.parse(backup.toString('utf8'));
    cfg.sites = cfg.sites.filter((s) => s.id !== 'localtest').concat([{
      id: 'localtest', name: '本地测试站点', kind: 'music', enabled: true, lyric: false,
      builtin: false, parser: 'generic',
      searchUrl: 'http://127.0.0.1:5173/', detailUrl: 'http://127.0.0.1:5173/',
      lyricUrl: '', linkPattern: '', base: '',
      audioUrl: 'http://127.0.0.1:5173/api/stream?file=' + encodeURIComponent(src.path),
    }]);
    fs.writeFileSync(SRC, JSON.stringify(cfg, null, 2), 'utf8');
    fs.rmSync(DL, { recursive: true, force: true }); fs.mkdirSync(DL, { recursive: true });
    fs.rmSync(path.resolve('downloads', newFile), { force: true });
    fs.rmSync(path.resolve('downloads', '服务端尚未缓存 - 自动缓存测试曲目.mp3'), { force: true });

    const prof = path.resolve('.tmp', 'profAwait'); fs.rmSync(prof, { recursive: true, force: true });
    proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
      '--remote-debugging-port=' + PORT, '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true }); proc.unref();
    let ver = null, list = null;
    for (let i = 0; i < 60; i++) { try { ver = await hj(PORT, '/json/version'); list = await hj(PORT, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
    BW = await cdp(ver.webSocketDebuggerUrl);
    await BW.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: DL, eventsEnabled: true });
    P = await cdp(list[0].webSocketDebuggerUrl); const send = P.send;
    const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 260)); return r.result.value; };
    await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
    await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    await send('Page.navigate', { url: 'http://127.0.0.1:5173/' }); await sleep(2200);

    const hist = [{ id: 'await1', name: '服务端尚未缓存 - 待缓存曲目', kind: 'audio', siteId: 'localtest', itemId: PROBE_ID, path: '', tags: { title: NEW, artist: '' }, parsed: { title: NEW, artist: '', episode: 0, year: '' } }];
    await ev(`(() => { localStorage.setItem('mp.history', ${JSON.stringify(JSON.stringify(hist))}); localStorage.removeItem('mp.devsaved'); localStorage.removeItem('mp.autosavedevice'); return 1; })()`);
    await send('Page.reload', { ignoreCache: true }); await sleep(2500);
    await ev(`(() => { document.querySelector('#plist li').click(); return 1; })()`);
    await sleep(1200);
    const t0 = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', paused: m.paused }; })()`);
    console.log('点击后 1.2s:', JSON.stringify(t0), '（本机文件下载极快，可能已经完成）');

    let st = null;
    for (let k = 0; k < 25; k++) {
      await sleep(1000);
      st = await ev(`(() => { const m = document.querySelector('#media'); const c = document.querySelector('#ptCache'); return { src: m.getAttribute('src') || '', playing: !m.paused, t: Number(m.currentTime.toFixed(1)), chip: c.hidden ? '' : document.querySelector('#ptCacheTxt').textContent, status: (document.querySelector('#plist li .status')||{}).textContent }; })()`);
      if (st.playing && st.t > 0.5) break;
    }
    await sleep(1800);
    console.log('缓存完成后:', JSON.stringify(st));
    const svc = fs.existsSync(path.resolve('downloads', newFile));
    const dev = fs.existsSync(DL) ? fs.readdirSync(DL).filter((f) => !f.endsWith('.crdownload')) : [];
    console.log('服务端 downloads 新增文件:', svc ? newFile + '(' + Math.round(fs.statSync(path.resolve('downloads', newFile)).size / 1024) + 'KB)' : '无');
    console.log('设备下载目录:', dev);
    console.log('下载事件:', BW.events.filter((e) => e.method === 'Browser.downloadWillBegin').map((e) => e.params.suggestedFilename));
    console.log('页面异常:', P.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => String(e.params.exceptionDetails.text).slice(0, 100)));
    const sitePlay = P.events.filter((e) => e.method === 'Network.requestWillBeSent' && /\/api\/site\/play/.test(e.params.request.url));
    console.log('站点流请求:', sitePlay.length);
    const ok = /\/api\/stream\?/.test(st.src) && st.playing && st.t > 0 && svc && dev.length > 0 && sitePlay.length === 0;
    console.log('RESULT:', ok ? 'PASS 缓存落地后自动开播并自动存到设备' : 'FAIL');
  } finally {
    fs.writeFileSync(SRC, backup);
    [newFile, '服务端尚未缓存 - 自动缓存测试曲目.mp3'].forEach((f) => fs.rmSync(path.resolve('downloads', f), { force: true }));
    await wipeTasks().catch(() => {});
    if (P) P.ws.close(); if (BW) BW.ws.close(); if (proc) proc.kill();
  }
  process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
