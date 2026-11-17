'use strict';
/* 验证端口、绑定地址、public/download/sources/media 路径全部由 config.json 驱动（不依赖环境变量） */
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const CFG = path.join(ROOT, 'config.json');
const TMP = path.join(ROOT, '.tmp', 'cfgtest');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const backup = fs.readFileSync(CFG, 'utf8');
  fs.rmSync(TMP, { recursive: true, force: true });
  fs.mkdirSync(TMP, { recursive: true });
  const testCfg = {
    port: 5291,
    host: '127.0.0.1',
    appDir: '.tmp/cfgtest',
    publicDir: path.join(ROOT, 'public').replace(/\\/g, '/'),
    downloadDir: 'cache',
    sourcesFile: 'sites.json',
    mediaDir: 'media',
  };
  fs.writeFileSync(CFG, JSON.stringify(testCfg, null, 2), 'utf8');
  fs.copyFileSync(path.join(ROOT, 'sources.json'), path.join(TMP, 'sites.json'));

  let child = null, log = '';
  try {
    // 故意不传任何 PORT / HOST / MEDIA_DIR 环境变量
    child = spawn(process.execPath, ['server.js'], { cwd: ROOT, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout.on('data', (d) => { log += d.toString(); });
    child.stderr.on('data', (d) => { log += d.toString(); });

    let up = false;
    for (let i = 0; i < 30; i++) {
      try { const r = await fetch('http://127.0.0.1:5291/'); if (r.ok) { up = true; break; } } catch (e) {}
      await sleep(400);
    }
    const home = await fetch('http://127.0.0.1:5291/').then((r) => r.status).catch(() => 0);
    const sites = await fetch('http://127.0.0.1:5291/api/sites').then((r) => r.json()).catch(() => null);
    const browse = await fetch('http://127.0.0.1:5291/api/browse?dir=@downloads').then((r) => r.json()).catch(() => null);

    console.log('启动日志:', log.trim().split('\n').filter((l) => l.trim()).slice(0, 9).join(' | '));
    console.log('自定义端口 5291 首页:', home, '| 站点数:', sites && sites.sites && sites.sites.length, '| browse:', !!(browse && browse.files));
    console.log('自定义下载目录 cache 已创建:', fs.existsSync(path.join(TMP, 'cache')));
    console.log('日志含配置文件路径:', /config\.json/.test(log), '| 含 127.0.0.1:', /http:\/\/127\.0\.0\.1:5291/.test(log));

    const ok = up && home === 200 && sites && sites.sites.length > 0 && browse && browse.files
      && fs.existsSync(path.join(TMP, 'cache')) && /config\.json/.test(log)
      && /http:\/\/127\.0\.0\.1:5291/.test(log);
    console.log('RESULT:', ok ? 'PASS 端口与所有路径均由 config.json 驱动' : 'FAIL');
  } finally {
    if (child) child.kill();
    fs.writeFileSync(CFG, backup, 'utf8');
    fs.rmSync(TMP, { recursive: true, force: true });
  }
  process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
