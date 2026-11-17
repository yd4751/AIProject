'use strict';
/* 运行时配置：监听端口、绑定地址、所有数据路径统一从 config.json 读取。
   - 配置文件固定在「程序目录」：开发模式是源码根目录，exe 是 exe 所在目录
   - 配置里的相对路径以 appDir 为基准解析，绝对路径直接使用
   - exe 首次运行时若磁盘上没有 config.json，使用打包时内嵌的默认配置 */

const fs = require('node:fs');
const path = require('node:path');

const DEFAULTS = {
  port: 5173,
  host: '0.0.0.0',
  appDir: '.',              // 数据根目录（其余相对路径都基于它）
  publicDir: 'public',      // 前端页面目录
  downloadDir: 'downloads', // 歌曲缓存目录
  sourcesFile: 'sources.json', // 站点配置
  mediaDir: '',             // 媒体库默认目录；留空则自动用 视频/音乐/文档/主目录
};

function isSea() {
  try { return !!require('node:sea').isSea(); } catch (err) { return false; }
}

function readJSON(file) {
  try {
    const j = JSON.parse(fs.readFileSync(file, 'utf8'));
    return (j && typeof j === 'object' && !Array.isArray(j)) ? j : null;
  } catch (err) { return null; }
}

function embeddedJSON(key) {
  if (!isSea()) return null;
  try { return JSON.parse(require('node:sea').getAsset(key, 'utf8')); } catch (err) { return null; }
}

const ROOT_DIR = isSea() ? path.dirname(process.execPath) : __dirname;
const CONFIG_FILE = path.join(ROOT_DIR, 'config.json');
const config = Object.assign({}, DEFAULTS, readJSON(CONFIG_FILE) || embeddedJSON('config.json') || {});

const APP_DIR = path.resolve(ROOT_DIR, String(config.appDir || '.'));
const resolveIn = (v, fallback) => path.resolve(APP_DIR, String(v || fallback));
const port = Number(config.port);
const PORT = Number.isInteger(port) && port > 0 && port <= 65535 ? port : DEFAULTS.port;

module.exports = {
  isSea,
  config,
  CONFIG_FILE,           // config.json（引导文件，固定位置）
  ROOT_DIR,              // 程序目录
  APP_DIR,               // 数据根目录
  PUBLIC_DIR: resolveIn(config.publicDir, DEFAULTS.publicDir),
  DOWNLOAD_DIR: resolveIn(config.downloadDir, DEFAULTS.downloadDir),
  CONFIG_PATH: resolveIn(config.sourcesFile, DEFAULTS.sourcesFile), // sources.json
  MEDIA_DIR: config.mediaDir ? resolveIn(config.mediaDir, '') : '',
  PORT,
  HOST: String(config.host || DEFAULTS.host),
};
