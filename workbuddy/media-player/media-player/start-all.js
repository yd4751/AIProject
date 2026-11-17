#!/usr/bin/env node
/**
 * 一键启动「后端服务 + 鉴权网关」。
 * 端口与路径分别读取 config.json 和 auth-gateway/config.json，本脚本不写死任何配置。
 * 子进程日志统一加前缀输出；任一服务意外退出会停止其余服务，方便排查。
 */
const { spawn } = require('child_process');
const path = require('path');

const ROOT = __dirname;
const SERVICES = [
  { name: '后端', file: 'server.js', delay: 0 },
  { name: '网关', file: 'auth-gateway/gateway.js', delay: 900 },
];

const children = [];
let stopping = false;

function forward(stream, name, isErr) {
  let buf = '';
  const out = (line) => (isErr ? process.stderr : process.stdout).write(`[${name}] ${line}\n`);
  stream.on('data', (chunk) => {
    buf += chunk.toString();
    const lines = buf.split(/\r?\n/);
    buf = lines.pop();
    for (const line of lines) out(line);
  });
  stream.on('end', () => { if (buf) out(buf); });
}

function start(svc) {
  if (stopping) return;
  const child = spawn(process.execPath, [path.join(ROOT, svc.file)], {
    cwd: ROOT,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  children.push(child);
  forward(child.stdout, svc.name, false);
  forward(child.stderr, svc.name, true);
  child.on('error', (err) => {
    if (!stopping) console.error(`[启动器] ${svc.name} 无法启动：${err.message}`);
    shutdown(1);
  });
  child.on('exit', (code, signal) => {
    if (stopping) return;
    console.error(`[启动器] ${svc.name} 意外退出 (code=${code}, signal=${signal})，正在停止其它服务`);
    shutdown(1);
  });
}

function shutdown(code) {
  if (stopping) return;
  stopping = true;
  for (const c of children) { try { c.kill(); } catch (err) { /* 忽略 */ } }
  process.exit(code);
}

console.log('  正在启动影音播放器服务（Ctrl + C 停止全部）');
console.log(`  工作目录: ${ROOT}`);
for (const svc of SERVICES) {
  if (svc.delay) setTimeout(() => start(svc), svc.delay);
  else start(svc);
}
process.on('SIGINT', () => shutdown(0));
process.on('SIGTERM', () => shutdown(0));
