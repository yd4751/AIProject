'use strict';
/**
 * 把服务端打包成单文件 Windows exe（Node 22 官方 SEA）。
 * 用法：npm run build:exe
 * 产物：dist/media-player.exe —— 双击即可运行。
 *   public/、config.json 与 sources.json 已内嵌；首次运行会在 exe 同目录释放可编辑的
 *   config.json / sources.json，并按配置创建下载目录。改 config.json 后重启生效。
 */
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const esbuild = require('esbuild');

const ROOT = __dirname;
const BUILD = path.join(ROOT, '.build');
const DIST = path.join(ROOT, 'dist');
const BUNDLE = path.join(BUILD, 'bundle.cjs');
const BLOB = path.join(BUILD, 'sea-prep.blob');
const CONFIG = path.join(ROOT, 'sea-config.json'); // 临时：与 assets 同目录，路径才好写
const EXE = path.join(DIST, 'media-player.exe');
const FUSE = 'NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2';

const rel = (p) => path.relative(ROOT, p).replace(/\\/g, '/');

console.log('[1/5] 清理旧的构建产物…');
fs.rmSync(BUILD, { recursive: true, force: true });
fs.mkdirSync(BUILD, { recursive: true });
fs.mkdirSync(DIST, { recursive: true });

console.log('[2/5] 打包 JS（server.js + sites.js + runtime-paths.js）…');
esbuild.buildSync({
  entryPoints: [path.join(ROOT, 'server.js')],
  bundle: true,
  platform: 'node',
  format: 'cjs',
  target: 'node22',
  outfile: BUNDLE,
  logLevel: 'warning',
  legalComments: 'none',
});

console.log('[3/5] 收集内嵌资源…');
const assets = { 'config.json': 'config.json', 'sources.json': 'sources.json' };
const pubDir = path.join(ROOT, 'public');
for (const name of fs.readdirSync(pubDir)) {
  if (!fs.statSync(path.join(pubDir, name)).isFile()) continue;
  assets['public/' + name] = 'public/' + name;
}
fs.writeFileSync(CONFIG, JSON.stringify({
  main: rel(BUNDLE),
  output: rel(BLOB),
  disableExperimentalSEAWarning: true,
  useSnapshot: false,
  useCodeCache: false,
  assets,
}, null, 2), 'utf8');
console.log('      内嵌:', Object.keys(assets).join(', '));

console.log('[4/5] 生成 SEA blob…');
try {
  execFileSync(process.execPath, ['--experimental-sea-config', CONFIG], { stdio: 'inherit', cwd: ROOT });
} finally {
  fs.rmSync(CONFIG, { force: true });
}
if (!fs.existsSync(BLOB)) throw new Error('SEA blob 未生成');

console.log('[5/5] 注入 Node 运行时，生成 exe…');
fs.copyFileSync(process.execPath, EXE);
// 复制出来的 node.exe 带 Authenticode 签名，注入后会失效；有 signtool 就顺手移除
try { execFileSync('signtool', ['remove', '/s', EXE], { stdio: 'ignore' }); } catch (err) { /* 没装也能跑 */ }
execFileSync(process.execPath, [
  path.join(ROOT, 'node_modules', 'postject', 'dist', 'cli.js'),
  EXE, 'NODE_SEA_BLOB', BLOB,
  '--sentinel-fuse', FUSE,
  '--overwrite',
], { stdio: 'inherit', cwd: ROOT });

fs.rmSync(BUILD, { recursive: true, force: true });
console.log('\n完成:', EXE, `(${(fs.statSync(EXE).size / 1048576).toFixed(1)} MB)`);
console.log('双击运行；端口、地址与各目录在 exe 同目录的 config.json 里修改，改完重启生效。');
