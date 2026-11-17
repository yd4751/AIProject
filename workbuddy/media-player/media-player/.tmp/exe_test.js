'use strict';
/* 验证单文件 exe：内嵌资源、配置释放、改 config.json 后端口与路径生效、端口冲突自动顺延 */
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const ROOT = path.resolve(__dirname, '..');
const RUN = path.join(ROOT, '.tmp', 'exe_run');
const EXE = path.join(ROOT, 'dist', 'media-player.exe');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const wait = async (port, tries = 35) => {
  for (let i = 0; i < tries; i++) {
    try { const r = await fetch('http://127.0.0.1:' + port + '/'); if (r.ok) return true; } catch (e) {}
    await sleep(400);
  }
  return false;
};
const writeCfg = (obj) => fs.writeFileSync(path.join(RUN, 'config.json'), JSON.stringify(obj, null, 2), 'utf8');
const start = () => {
  const c = spawn(path.join(RUN, 'media-player.exe'), [], { cwd: RUN, stdio: ['ignore', 'pipe', 'pipe'] });
  let log = '';
  c.stdout.on('data', (d) => { log += d.toString(); });
  c.stderr.on('data', (d) => { log += d.toString(); });
  return { child: c, log: () => log };
};

(async () => {
  fs.rmSync(RUN, { recursive: true, force: true });
  fs.mkdirSync(RUN, { recursive: true });
  fs.copyFileSync(EXE, path.join(RUN, 'media-player.exe'));
  const running = [];
  try {
    // ---- A：空目录 + 指定端口，内嵌前端与首启释放 ----
    writeCfg({ port: 5273, host: '127.0.0.1' });
    const a = start(); running.push(a.child);
    const upA = await wait(5273);
    const checks = {};
    for (const [label, u] of [
      ['首页', '/'], ['app.js', '/app.js'], ['styles.css', '/styles.css'],
      ['penguin.svg', '/penguin.svg'], ['任务接口', '/api/site/tasks'], ['浏览接口', '/api/browse?dir=@downloads'],
    ]) {
      const r = await fetch('http://127.0.0.1:5273' + u);
      checks[label] = { status: r.status, bytes: (await r.text()).length };
    }
    const cfgA = JSON.parse(fs.readFileSync(path.join(RUN, 'config.json'), 'utf8'));
    const sitesA = JSON.parse(fs.readFileSync(path.join(RUN, 'sources.json'), 'utf8'));
    console.log('A 启动日志:', a.log().trim().split('\n').filter((l) => l.trim()).slice(0, 8).join(' | '));
    console.log('A 内嵌资源:', JSON.stringify(checks));
    console.log('A 释放 sources.json 站点数:', sitesA.sites.length, '| downloads/:', fs.existsSync(path.join(RUN, 'downloads')));
    console.log('A 配置补全默认项:', Object.keys(cfgA).join(','));

    // 端口冲突：第二个实例自动顺延
    const a2 = start(); running.push(a2.child);
    await sleep(2200);
    const bumped = /尝试 5274/.test(a2.log()) && /http:\/\/127\.0\.0\.1:5274/.test(a2.log());
    const r5274 = await fetch('http://127.0.0.1:5274/').then((r) => r.status).catch(() => 0);
    console.log('A 端口冲突自动改 5274:', bumped, '| 5274 状态:', r5274);
    a2.child.kill(); a.child.kill(); await sleep(600);

    // ---- B：改 config.json 换端口和自定义路径 ----
    fs.rmSync(path.join(RUN, 'data'), { recursive: true, force: true });
    writeCfg({ port: 5291, host: '127.0.0.1', appDir: 'data', publicDir: 'public', downloadDir: 'cache', sourcesFile: 'mysites.json', mediaDir: '' });
    const b = start(); running.push(b.child);
    const upB = await wait(5291);
    const homeB = await fetch('http://127.0.0.1:5291/app.js').then((r) => r.status).catch(() => 0);
    const sitesB = await fetch('http://127.0.0.1:5291/api/sites').then((r) => r.json()).catch(() => null);
    const dataDir = path.join(RUN, 'data');
    console.log('B 改配置后 5291 app.js:', homeB, '| 站点数:', sitesB && sitesB.sites && sitesB.sites.length);
    console.log('B 自定义路径:', {
      cache: fs.existsSync(path.join(dataDir, 'cache')),
      mysites: fs.existsSync(path.join(dataDir, 'mysites.json')),
      publicOnDisk: fs.existsSync(path.join(dataDir, 'public')),
    });
    console.log('B 日志含数据目录 data:', /data$/.test(b.log().trim()) || /\\data/.test(b.log()) || /\/data/.test(b.log()));
    b.child.kill(); await sleep(600);

    // ---- C：删掉配置后自动用内嵌默认值重建 ----
    fs.rmSync(path.join(RUN, 'config.json'), { force: true });
    const c = start(); running.push(c.child);
    await sleep(2200);
    const rebuilt = fs.existsSync(path.join(RUN, 'config.json'))
      ? JSON.parse(fs.readFileSync(path.join(RUN, 'config.json'), 'utf8')) : null;
    console.log('C 删配置后自动重建:', !!rebuilt, '| 默认端口:', rebuilt && rebuilt.port, '| 默认 appDir:', rebuilt && rebuilt.appDir);
    c.child.kill();

    const ok = upA && Object.values(checks).every((v) => v.status === 200 && v.bytes > 0)
      && checks['app.js'].bytes > 10000 && sitesA.sites.length > 0
      && fs.existsSync(path.join(RUN, 'downloads')) && bumped && r5274 === 200
      && upB && homeB === 200 && sitesB && sitesB.sites.length > 0
      && fs.existsSync(path.join(dataDir, 'cache')) && fs.existsSync(path.join(dataDir, 'mysites.json'))
      && !fs.existsSync(path.join(dataDir, 'public'))
      && rebuilt && rebuilt.port === 5173 && rebuilt.appDir === '.';
    console.log('RESULT:', ok ? 'PASS exe 配置化：内嵌资源 / 端口 / 路径 / 配置重建 / 端口顺延' : 'FAIL');
  } finally {
    running.forEach((c) => { try { c.kill(); } catch (e) {} });
    await sleep(300);
  }
  process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
