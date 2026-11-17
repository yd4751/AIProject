'use strict';
/**
 * 把独立鉴权网关打包成单文件 Windows exe（Node 22 官方 SEA）。
 * 用法：npm run build:gateway
 * 产物：dist/auth-gateway.exe —— 双击即可运行，目标机器不需要装 Node。
 *
 * 内嵌：config.json（脱敏后的默认配置）、whitelist.json（空白名单模板）。
 * 首次运行会在 exe 同目录生成 gateway.db（SQLite），并把同目录已有的
 * gateway-config.json / whitelist.json / records.json 幂等迁入该库；管理员初始密码
 * 打印在控制台一次，随后以 scrypt 哈希存入 gateway.db。
 * 之后仅「启动端口 / 监听地址」保留在 gateway-config.json，其余配置、转发服务、
 * 白名单、设备、授权记录全部存 gateway.db，并在管理端「系统配置」等页面在线修改。
 * 用独立配置文件名，是为了不和同目录的 media-player.exe 争用服务端 config.json。
 */
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const esbuild = require('esbuild');

const ROOT = __dirname;                       // auth-gateway/
const PROJECT = path.dirname(ROOT);           // 项目根目录（node_modules 在这里）
const BUILD = path.join(ROOT, '.build');
const DIST = path.join(PROJECT, 'dist');
const BUNDLE = path.join(BUILD, 'gateway.cjs');
const BLOB = path.join(BUILD, 'sea-prep.blob');
const CONFIG = path.join(ROOT, 'sea-config.json');
const EXE = path.join(DIST, 'auth-gateway.exe');
const FUSE = 'NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2';

const rel = (p) => path.relative(ROOT, p).replace(/\\/g, '/');

console.log('[1/5] 清理旧的构建产物…');
fs.rmSync(BUILD, { recursive: true, force: true });
fs.mkdirSync(BUILD, { recursive: true });
fs.mkdirSync(DIST, { recursive: true });

console.log('[2/5] 打包 JS（gateway.js + login-page + device-login-page + admin-page + qrcode）…');
esbuild.buildSync({
  entryPoints: [path.join(ROOT, 'gateway.js')],
  bundle: true,
  platform: 'node',
  format: 'cjs',
  target: 'node22',
  outfile: BUNDLE,
  logLevel: 'warning',
  legalComments: 'none',
});

console.log('[3/5] 收集内嵌资源（配置脱敏、白名单用空模板；gateway.db 首次运行自动生成，不内嵌）…');
const cfg = JSON.parse(fs.readFileSync(path.join(ROOT, 'config.json'), 'utf8'));
delete cfg.secret;                                     // 每台机器首次运行生成自己的签名密钥
// 微信密钥一律不内嵌进 exe：分发包里只留空位，部署时在 exe 同目录 gateway-config.json 填写
if (cfg.wechat && typeof cfg.wechat === 'object') {
  ['miniAppSecret', 'mpAppSecret', 'openAppSecret'].forEach((k) => { delete cfg.wechat[k]; });
}
cfg.admins = (cfg.admins || []).map((a) => ({ username: a.username, password: '' }));  // 各自生成初始密码
fs.writeFileSync(path.join(BUILD, 'config.json'), JSON.stringify(cfg, null, 2) + '\n', 'utf8');
fs.writeFileSync(path.join(BUILD, 'whitelist.json'),
  JSON.stringify({ _note: '白名单：users 填微信 openid；devices 填设备机器码；未授权自动记入 pending / devicePending。',
    users: [], pending: [], devices: [], devicePending: [] }, null, 2) + '\n', 'utf8');

const assets = { 'config.json': rel(path.join(BUILD, 'config.json')),
  'whitelist.json': rel(path.join(BUILD, 'whitelist.json')) };
// IP 归属地库（约 2MB）：内嵌后 exe 单文件即可显示访问 IP 所属地区，无需外部文件
const IPDB = path.join(ROOT, 'ipdb.bin');
if (fs.existsSync(IPDB)) assets['ipdb.bin'] = rel(IPDB);
else console.log('      ! 未找到 ipdb.bin，exe 内的「IP 归属地」将显示为空（可运行 tools/build-ipdb.cjs 生成）');
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
try { execFileSync('signtool', ['remove', '/s', EXE], { stdio: 'ignore' }); } catch (err) { /* 没装也能跑 */ }
execFileSync(process.execPath, [
  path.join(PROJECT, 'node_modules', 'postject', 'dist', 'cli.js'),
  EXE, 'NODE_SEA_BLOB', BLOB,
  '--sentinel-fuse', FUSE,
  '--overwrite',
], { stdio: 'inherit', cwd: ROOT });

fs.rmSync(BUILD, { recursive: true, force: true });
console.log('\n完成:', EXE, `(${(fs.statSync(EXE).size / 1048576).toFixed(1)} MB)`);
console.log('双击运行；管理员初始密码在首次启动的控制台里打印一次，之后存 gateway.db。');
console.log('仅启动端口 / 监听地址在同目录 gateway-config.json 修改；其余全部在管理端「系统配置」「转发服务」「微信白名单」「设备白名单」页在线修改，保存即时生效。');
console.log('默认 loginMode=device（设备登录 + 机器码白名单）；在「系统配置」页可切回微信扫码，无需重启。微信 AppSecret 不会打进 exe。');
