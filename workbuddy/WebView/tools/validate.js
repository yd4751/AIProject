// 小程序工程静态自检（不依赖微信开发者工具）
const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..', 'miniprogram');
const problems = [];
const checked = [];

function walk(dir, output) {
    for (const name of fs.readdirSync(dir)) {
        const full = path.join(dir, name);
        const stat = fs.statSync(full);
        if (stat.isDirectory()) walk(full, output);
        else output.push(full);
    }
    return output;
}

const files = walk(ROOT, []);
const rel = full => path.relative(ROOT, full).replace(/\\/g, '/');

// 1. 严格 JSON
for (const file of files.filter(f => f.endsWith('.json'))) {
    try {
        JSON.parse(fs.readFileSync(file, 'utf8'));
        checked.push('JSON ' + rel(file));
    } catch (error) {
        problems.push('JSON 解析失败 ' + rel(file) + ': ' + error.message);
    }
}

const app = JSON.parse(fs.readFileSync(path.join(ROOT, 'app.json'), 'utf8'));
const config = JSON.parse(fs.readFileSync(path.join(ROOT, 'project.config.json'), 'utf8'));

// 2. 页面文件齐备
for (const page of app.pages || []) {
    for (const ext of ['.js', '.wxml']) {
        const target = path.join(ROOT, page + ext);
        if (!fs.existsSync(target)) problems.push('缺少页面文件 ' + page + ext);
    }
}
if (!app.lazyCodeLoading) problems.push('app.json 缺少 lazyCodeLoading');
if (config.miniprogramRoot !== './' && config.miniprogramRoot !== '.') {
    problems.push('miniprogramRoot 与工程结构不符: ' + config.miniprogramRoot);
}
checked.push('页面 ' + (app.pages || []).length + ' 个');

// 3. tabBar 配置
for (const item of (app.tabBar && app.tabBar.list) || []) {
    if (!(app.pages || []).includes(item.pagePath)) problems.push('tabBar 页面未注册 ' + item.pagePath);
    for (const key of ['iconPath', 'selectedIconPath']) {
        if (!item[key]) continue;
        const icon = path.join(ROOT, item[key]);
        if (!fs.existsSync(icon)) problems.push('图标缺失 ' + item[key]);
        else if (fs.statSync(icon).size > 40 * 1024) problems.push('图标超过 40KB ' + item[key]);
    }
}
checked.push('tabBar ' + ((app.tabBar && app.tabBar.list) || []).length + ' 项');

// 4. WXML 禁用语法
const bannedWxml = [
    [/\?\./, '可选链 ?.'],
    [/\?\?/, '空值合并 ??'],
    [/=>/, '箭头函数'],
    [/\bdocument\./, 'document'],
    [/\bwindow\./, 'window'],
    [/\blocalStorage\b/, 'localStorage'],
    [/\bfetch\(/, 'fetch'],
    [/<(div|span|section|article|a\s|p>|ul|li>|h1)\b/i, 'HTML 标签'],
];
for (const file of files.filter(f => f.endsWith('.wxml'))) {
    const text = fs.readFileSync(file, 'utf8');
    for (const [pattern, label] of bannedWxml) {
        if (pattern.test(text)) problems.push('WXML 含 ' + label + ': ' + rel(file));
    }
    checked.push('WXML ' + rel(file));
}

// 5. WXSS @import / WXML import / JS require 本地引用存在
function checkRefs(file, pattern, kind) {
    const text = fs.readFileSync(file, 'utf8');
    let match = pattern.exec(text);
    while (match !== null) {
        let target = path.resolve(path.dirname(file), match[1]);
        if (kind === 'require' && !fs.existsSync(target) && fs.existsSync(target + '.js')) {
            target = target + '.js';
        }
        if (!fs.existsSync(target)) problems.push(kind + ' 引用不存在 ' + rel(file) + ' -> ' + match[1]);
        match = pattern.exec(text);
    }
}
for (const file of files.filter(f => f.endsWith('.js'))) {
    checkRefs(file, /require\(['"](\.[^'"]+)['"]\)/g, 'require');
}
for (const file of files.filter(f => f.endsWith('.wxss'))) {
    checkRefs(file, /@import\s+['"]([^'"]+)['"]/g, '@import');
}
for (const file of files.filter(f => f.endsWith('.wxml'))) {
    checkRefs(file, /<(?:include|import)\s+src=['"](\.[^'"]+)['"]/g, 'include/import');
}

// 6. 禁止浏览器 API 出现在 JS（注释除外）
for (const file of files.filter(f => f.endsWith('.js'))) {
    const text = fs.readFileSync(file, 'utf8').replace(/\/\/[^\n]*/g, '');
    for (const word of ['document.querySelector', 'window.location', 'localStorage', 'fetch(']) {
        if (text.includes(word)) problems.push('JS 含浏览器 API ' + word + ': ' + rel(file));
    }
}

// 7. HTML 解析纯函数冒烟测试
global.wx = { request() {} };
const { parseHtml, resolveUrl } = require(path.join(ROOT, 'utils', 'html-lite.js'));
const sample = [
    '<html><head><title>测试 &amp; 站点</title>',
    '<meta name="description" content="一个自建站点"></head><body>',
    '<script>var x = 1;</script><nav><a href="/admin">后台 &gt;</a></nav>',
    '<h1>欢迎光临</h1><p>这是第一段正文内容，用于验证解析是否工作。</p>',
    '<p>第二段 &lt;正文&gt; 内容。</p><img data-src="/cover.png" />',
    '<ul><li>列表项一内容很长</li></ul>',
    '<a href="./about">关于我们</a><a href="https://other.com/x">外链</a>',
    '</body></html>',
].join('');
const parsed = parseHtml(sample, 'https://blog.example.com/posts/hello');
if (parsed.title !== '测试 & 站点') problems.push('解析 title 失败: ' + parsed.title);
if (parsed.description !== '一个自建站点') problems.push('解析 description 失败');
if (!parsed.blocks.some(b => b.type === 'heading' && b.text === '欢迎光临')) problems.push('解析标题块失败');
if (!parsed.blocks.some(b => b.type === 'text' && b.text.includes('第二段 <正文> 内容'))) problems.push('实体解码/文本块失败');
if (!parsed.blocks.some(b => b.type === 'image' && b.src === 'https://blog.example.com/cover.png')) problems.push('图片提取或绝对化失败');
if (parsed.links.length === 0 || parsed.links[0].href !== 'https://blog.example.com/admin') problems.push('同站链接解析失败: ' + JSON.stringify(parsed.links));
if (parsed.links.some(l => l.href.indexOf('other.com') >= 0)) problems.push('外链未被过滤');
if (resolveUrl('https://blog.example.com/posts/hello', './about') !== 'https://blog.example.com/posts/about') problems.push('相对 URL 解析失败');
checked.push('HTML 解析冒烟测试');

// 8. 本地数据层与检测缓存冒烟测试（mock wx 存储）
const storage = {};
global.wx = {
    getStorageSync: key => (Object.prototype.hasOwnProperty.call(storage, key) ? storage[key] : ''),
    setStorageSync: (key, value) => { storage[key] = value; },
    removeStorageSync: key => { delete storage[key]; },
    request: options => { options.success({ statusCode: 200, data: '<html></html>' }); },
};
const { createLocalSiteStore } = require(path.join(ROOT, 'utils', 'site-store.js'));
const checker = require(path.join(ROOT, 'utils', 'site-checker.js'));
const siteStore = createLocalSiteStore({ storageKey: 'sitedock-sites-v1' });

siteStore.create({ name: '我的博客', url: 'blog.example.com', group: '博客' }).then(created => {
    if (created.error || !created.data) {
        problems.push('站点创建失败');
        return null;
    }
    if (created.data.url !== 'https://blog.example.com') problems.push('URL 补全失败: ' + created.data.url);
    return siteStore.list().then(listed => {
        if (listed.data.length !== 1) problems.push('站点列表数量异常');
        return created.data.id;
    });
}).then(id => {
    if (!id) return siteStore.update(id, { pinned: true }).then(() => null);
    return siteStore.update(id, { pinned: true }).then(updated => {
        if (updated.error || updated.data.pinned !== true) problems.push('置顶更新失败');
        return siteStore.remove(id).then(() => id);
    });
}).then(() => siteStore.list()).then(final => {
    if (final.data.length !== 0) problems.push('站点删除失败');
    checker.saveSettings({ timeout: 10000 });
    if (checker.getSettings().timeout !== 10000) problems.push('设置读写失败');
    checker.recordStatus('x', { status: 'online', latency: 42, httpStatus: 200 });
    if (checker.loadStatusMap().x.latency !== 42) problems.push('状态缓存读写失败');
    checker.clearStatusCache();
    if (Object.keys(checker.loadStatusMap()).length !== 0) problems.push('状态缓存清除失败');
    checked.push('数据层与检测缓存冒烟测试');

    // 9. 访问诊断与抓取代理
    const rp = require(path.join(ROOT, 'utils', 'request-proxy.js'));
    const badUrl = 'http://43.139.101.12:5173';
    const diagnosis = rp.diagnoseUrl(badUrl);
    if (rp.isDirectlyReachable(badUrl)) problems.push('受限 URL 误判为可直连');
    if (!diagnosis.some(line => line.indexOf('http 明文') >= 0)) problems.push('未诊断出 http 明文');
    if (!diagnosis.some(line => line.indexOf('IP 地址') >= 0)) problems.push('未诊断出 IP 直连');
    if (!diagnosis.some(line => line.indexOf('5173') >= 0)) problems.push('未诊断出非标端口');
    if (!rp.isDirectlyReachable('https://blog.example.com')) problems.push('合法 https 域名误判为受限');
    if (!rp.isDirectlyReachable('https://blog.example.com:8443')) problems.push('带端口的 https 域名误判为受限');
    rp.saveProxyConfig({ template: 'https://proxy.example.com/fetch', token: 't1' });
    const proxied = rp.buildRequestUrl(badUrl);
    if (proxied.indexOf('url=' + encodeURIComponent(badUrl)) < 0 || proxied.indexOf('token=t1') < 0) {
        problems.push('代理 URL 构造失败: ' + proxied);
    }
    if (!rp.isDirectlyReachable(badUrl)) problems.push('配置代理后仍判定受限');
    rp.saveProxyConfig({ template: 'https://p.example.com/f?target={url}', token: '' });
    if (rp.buildRequestUrl(badUrl).indexOf('target=' + encodeURIComponent(badUrl)) < 0) problems.push('占位符模板失败');
    rp.clearProxyConfig();
    if (rp.isUsingProxy()) problems.push('代理清除失败');
    checked.push('访问诊断与抓取代理冒烟测试');

    // 10. 调试站点、备案诊断与首次启动初始化
    const ds = require(path.join(ROOT, 'utils', 'debug-site.js'));
    const boot = require(path.join(ROOT, 'utils', 'bootstrap.js'));
    const debugUrl = ds.getDebugSite().url;
    if (debugUrl !== 'https://bighat.icu:7000') problems.push('调试站点地址不符');
    if (!ds.isDebugSite(debugUrl)) problems.push('调试站点识别失败');
    if (ds.isDebugSite('https://other.example.com')) problems.push('调试站点误判');
    if (ds.getFilingChecklist().length < 4) problems.push('上线待办清单不完整');
    const debugDiagnosis = rp.diagnoseUrl(debugUrl);
    if (!debugDiagnosis.some(line => line.indexOf('备案') >= 0)) problems.push('未诊断出备案状态');
    if (!debugDiagnosis.some(line => line.indexOf('自签') >= 0)) problems.push('未诊断出自签证书');
    const bootStore = createLocalSiteStore({ storageKey: 'sitedock-boot-test' });
    return boot.ensureBuiltinSite(bootStore, global.wx);
}).then(bootResult => {
    if (!bootResult.created) problems.push('首次启动未写入内置站点');
    const boot = require(path.join(ROOT, 'utils', 'bootstrap.js'));
    const bootStore = createLocalSiteStore({ storageKey: 'sitedock-boot-test' });
    return boot.ensureBuiltinSite(bootStore, global.wx);
}).then(secondBoot => {
    if (secondBoot.created) problems.push('内置站点被重复写入');
    checked.push('调试站点与首次启动冒烟测试');

    // 11. 离线快照（预览面板兜底内容）与浏览页缓存优先逻辑
    const snapshotPath = path.join(ROOT, 'data', 'demo-snapshot.js');
    if (!fs.existsSync(snapshotPath)) {
        problems.push('缺少离线快照数据（tools/make-demo-snapshot.js 生成）');
    } else {
        const snap = require(snapshotPath);
        if (snap.url !== 'https://bighat.icu:7000') problems.push('快照地址与调试站点不一致');
        if (!Array.isArray(snap.blocks) || snap.blocks.length === 0) problems.push('快照没有内容块');
        if (!snap.blocks.every(b => b && b.type && b.key)) problems.push('快照内容块缺少 type/key');
        if (!Array.isArray(snap.links)) problems.push('快照 links 不是数组');
        const browseJs = fs.readFileSync(path.join(ROOT, 'pages', 'browse', 'browse.js'), 'utf8');
        const browseWxml = fs.readFileSync(path.join(ROOT, 'pages', 'browse', 'browse.wxml'), 'utf8');
        if (browseJs.indexOf('demo-snapshot.js') < 0) problems.push('浏览页未引用离线快照');
        if (browseJs.indexOf('keepSnapshot') < 0) problems.push('浏览页缺少实时失败保留快照逻辑');
        if (browseWxml.indexOf('snapshot-banner') < 0) problems.push('浏览页缺少快照提示横幅');
        if (!snap.app || !Array.isArray(snap.app.library) || !Array.isArray(snap.app.sites)) {
            problems.push('离线快照缺少结构化的 APP 数据');
        }
        checked.push('离线快照数据与浏览页缓存优先');
    }

    // 12. 通用浏览三层视图、应用加速模式与播放控件
    for (const file of ['utils/app-client.js', 'utils/player.js', 'pages/browse/browse.wxss']) {
        if (!fs.existsSync(path.join(ROOT, file))) problems.push('缺少文件 ' + file);
    }
    if (!fs.existsSync(path.join(__dirname, 'verify-browse.js'))) problems.push('缺少浏览页联调脚本');
    const lite = require(path.join(ROOT, 'utils', 'html-lite.js'));
    for (const fn of ['parseAppView', 'toRichNodes', 'isScriptDriven', 'fetchStyles']) {
        if (typeof lite[fn] !== 'function') problems.push('html-lite 缺少导出 ' + fn);
    }
    const client = require(path.join(ROOT, 'utils', 'app-client.js'));
    for (const fn of ['detectApp', 'getLibrary', 'searchAll', 'buildTrackFromItem']) {
        if (typeof client[fn] !== 'function') problems.push('app-client 缺少导出 ' + fn);
    }
    const browseSource = fs.readFileSync(path.join(ROOT, 'pages', 'browse', 'browse.js'), 'utf8');
    const browseMarkup = fs.readFileSync(path.join(ROOT, 'pages', 'browse', 'browse.wxml'), 'utf8');
    for (const keyword of ['app-client', 'utils/player', 'toRichNodes', 'keepSnapshot', 'hasApp', 'viewMode']) {
        if (browseSource.indexOf(keyword) < 0) problems.push('浏览页缺少关键逻辑 ' + keyword);
    }
    for (const keyword of ['rich-text', 'view-switch', 'mode-switch', 'mini-player', 'sheet-mask', 'snapshot-banner']) {
        if (browseMarkup.indexOf(keyword) < 0) problems.push('浏览页缺少界面区块 ' + keyword);
    }
    const handlers = [];
    const handlerPattern = /(?:bind|catch)[a-z]+=["']([A-Za-z_$][\w$]*)["']/g;
    let handler = handlerPattern.exec(browseMarkup);
    while (handler !== null) {
        if (handlers.indexOf(handler[1]) < 0) handlers.push(handler[1]);
        handler = handlerPattern.exec(browseMarkup);
    }
    for (const name of handlers) {
        if (browseSource.indexOf(name + '(') < 0) problems.push('WXML 事件无对应方法 ' + name);
    }
    checked.push('通用三层浏览 / 应用加速 / 播放控件（' + handlers.length + ' 个事件已核对）');

    console.log('检查通过项: ' + checked.length);
    if (problems.length > 0) {
        console.log('\n发现 ' + problems.length + ' 个问题:');
        for (const item of problems) console.log(' - ' + item);
        process.exit(1);
    }
    console.log('静态自检全部通过');
});
