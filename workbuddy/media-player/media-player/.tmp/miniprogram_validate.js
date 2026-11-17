'use strict';
/** 小程序工程静态校验：JSON / 页面文件 / 原生组件 / 语法 / 敏感信息 */
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const DIR = path.resolve(__dirname, '..', 'auth-gateway', 'miniprogram');
let pass = 0, fail = 0;
function check(name, ok, extra) {
  console.log((ok ? '  PASS ' : '  FAIL ') + name + (extra ? ' | ' + extra : ''));
  ok ? pass++ : fail++;
}
const read = (f) => fs.readFileSync(path.join(DIR, f), 'utf8');
function json(f) {
  try { return JSON.parse(read(f)); } catch (e) { check('JSON 可解析: ' + f, false, e.message); return null; }
}
function walk(d) {
  return fs.readdirSync(path.join(DIR, d), { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? walk(path.join(d, e.name)) : [path.join(d, e.name).replace(/\\/g, '/')]);
}
const files = walk('.').sort();

['app.json', 'project.config.json', 'sitemap.json', 'pages/gate/index.json'].forEach((f) => {
  check('JSON 严格可解析: ' + f, !!json(f));
});
const app = json('app.json') || {};
const project = json('project.config.json') || {};
const sitemap = json('sitemap.json') || {};
check('app.json.pages 非空', Array.isArray(app.pages) && app.pages.length > 0);
check('app.json 含 lazyCodeLoading', app.lazyCodeLoading === 'requiredComponents');
check('sitemapLocation 指向真实文件', !!app.sitemapLocation && fs.existsSync(path.join(DIR, app.sitemapLocation)));
check('sitemap 规则合法', Array.isArray(sitemap.rules) && sitemap.rules.length > 0);
check('project.config.json miniprogramRoot 指向工程根',
  !project.miniprogramRoot || ['./', '.'].includes(project.miniprogramRoot));
check('appid 已填写且非游客 appid', !!project.appid && project.appid !== 'touristappid', project.appid);
check('compileType 为 miniprogram', project.compileType === 'miniprogram');

(app.pages || []).forEach((p) => {
  check('页面文件齐全: ' + p, ['.wxml', '.js', '.wxss'].every((ext) =>
    fs.existsSync(path.join(DIR, p + ext))));
  const wxml = read(p + '.wxml');
  check('WXML 无 HTML 标签/浏览器对象: ' + p,
    !/<(div|span|p|a|img|h[1-6]|script|iframe)\b|\bdocument\.|\bwindow\./i.test(wxml));
  // 粗检标签配对（忽略自闭合标签）
  const stack = [];
  let closed = true;
  wxml.replace(/<!--[\s\S]*?-->/g, '').replace(/<(\/)?([a-zA-Z][\w-]*)([^>]*?)(\/)?>/g,
    (m, closing, tag, attrs, self) => {
      if (closing) { if (stack.pop() !== tag) closed = false; }
      else if (!self) stack.push(tag);
      return m;
    });
  check('WXML 标签闭合: ' + p, closed && stack.length === 0, 'stack=' + stack.join(','));
});

files.filter((f) => f.endsWith('.js')).forEach((f) => {
  const src = read(f);
  check('JS 无浏览器 API: ' + f, !/\bdocument\.|\bwindow\.|localStorage|\bfetch\(/.test(src));
  let syntax = true, err = '';
  try { execFileSync(process.execPath, ['--check', path.join(DIR, f)], { stdio: 'pipe' }); }
  catch (e) { syntax = false; err = String(e.stderr || e.message).slice(0, 160); }
  check('JS 语法通过: ' + f, syntax, err);
  const reqs = [...src.matchAll(/require\(['"](\.[^'"]+)['"]\)/g)].map((m) => m[1]);
  reqs.forEach((r) => {
    const target = path.resolve(path.dirname(path.join(DIR, f)), r);
    check('require 路径存在: ' + f + ' → ' + r,
      fs.existsSync(target) || fs.existsSync(target + '.js'));
  });
});

const all = files.map((f) => read(f)).join('\n');
check('工程内不含小程序 AppSecret', !/3cee5ac93b037db37c7cfdb7c89c1d5a/.test(all));
check('基础工程文件齐全', ['app.js', 'app.json', 'app.wxss', 'project.config.json', 'sitemap.json', 'config.js']
  .every((f) => fs.existsSync(path.join(DIR, f))));
check('个人主体不引入 web-view 组件', !/<web-view\b/.test(all));
console.log('\n文件清单: ' + files.join(', '));
console.log('结果: ' + pass + ' 通过 / ' + fail + ' 失败');
if (fail) process.exitCode = 1;
