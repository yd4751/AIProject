const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});
(async () => {
  const prof = path.resolve('.tmp', 'profShot');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--remote-debugging-port=9232', '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'],
    { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 50; i++) { try { list = await hj(9232, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error('eval err'); return r.result.value; };
  await send('Page.enable'); await send('Runtime.enable');
  const U = 'http://127.0.0.1:5173';
  await send('Page.navigate', { url: U }); await sleep(1600);
  await ev(`(() => { localStorage.setItem('mp.skin','midnight'); localStorage.setItem('mp.history', JSON.stringify([
    { id:'r1', name:'七里香 - 周杰伦.mp3', path:'', kind:'audio', remote:true, siteId:'gequbao', itemId:'4194', tags:{title:'七里香',artist:'周杰伦'}, parsed:{title:'七里香',artist:'周杰伦'}, lyric:'', lyricSrc:'歌曲宝 · 歌词', timestamp: Date.now() },
    { id:'s1', name:'周杰伦 - 晴天.mp3', path:'E:/Project/workbuddy/media-player/media-player/downloads/周杰伦 - 晴天.mp3', kind:'audio', remote:false, siteId:'', itemId:'', tags:null, parsed:{title:'晴天',artist:'周杰伦'}, lyric:'', lyricSrc:'', timestamp: Date.now()-1000 },
    { id:'s2', name:'周杰伦 - 夜曲.mp3', path:'E:/Project/workbuddy/media-player/media-player/downloads/周杰伦 - 夜曲.mp3', kind:'audio', remote:false, siteId:'', itemId:'', tags:null, parsed:{title:'夜曲',artist:'周杰伦'}, lyric:'', lyricSrc:'', timestamp: Date.now()-2000 }
  ])); return 1 })()`);
  await send('Page.navigate', { url: U }); await sleep(2000);
  await ev(`(() => { document.querySelector('.navbtn[data-panel="list"]').click(); return 1 })()`);
  await sleep(600);
  const shot = await send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(path.resolve('.tmp', 'H1_list.png'), Buffer.from(shot.data, 'base64'));
  console.log('saved');
  ws.close(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
