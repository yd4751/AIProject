// 浏览页数据链路验证：离线纯函数 + 可选线上接口契约（--online）。
// 不修改小程序代码，只模拟 wx.request 走真实 HTTPS，确认解析结果与页面数据结构一致。
const https = require('https');
const path = require('path');

const ROOT = path.join(__dirname, '..', 'miniprogram');
const problems = [];
const checked = [];

function mockWx() {
    const httpsGet = (options, callback) => new Promise((resolve, reject) => {
        const req = https.get(options.url, {
            rejectUnauthorized: false,
            timeout: options.timeout || 12000,
            headers: options.header || {},
        }, res => {
            const chunks = [];
            res.on('data', chunk => chunks.push(chunk));
            res.on('end', () => resolve({ statusCode: res.statusCode, text: Buffer.concat(chunks).toString('utf8'), headers: res.headers }));
        });
        req.on('error', reject);
        req.on('timeout', () => { req.destroy(); reject(new Error('timeout')); });
    });
    global.wx = {
        request(options) {
            httpsGet(options).then(result => {
                let data = result.text;
                const type = result.headers['content-type'] || '';
                if (type.indexOf('json') >= 0) {
                    try { data = JSON.parse(result.text); } catch (error) { /* 保持文本 */ }
                }
                options.success({ statusCode: result.statusCode, data: data, header: result.headers });
            }).catch(error => {
                options.fail({ errMsg: error.message });
            });
        },
        getStorageSync: () => '',
        setStorageSync: () => {},
        removeStorageSync: () => {},
        getBackgroundAudioManager: () => null,
        createInnerAudioContext: () => null,
        showToast: () => {},
        showLoading: () => {},
        hideLoading: () => {},
        vibrateShort: () => {},
        setClipboardData: () => {},
        setNavigationBarTitle: () => {},
    };
}

mockWx();
const htmlLite = require(path.join(ROOT, 'utils', 'html-lite.js'));
const appClient = require(path.join(ROOT, 'utils', 'app-client.js'));
const player = require(path.join(ROOT, 'utils', 'player.js'));

// ---- 离线：通用渲染纯函数 ----
const sample = [
    '<!doctype html><html><head><title>图库 &amp; 站点</title>',
    '<link rel="stylesheet" href="/styles.css"></head><body>',
    '<script>alert("x")</script>',
    '<nav><a href="/all">全部</a><a href="/movie">影视</a><a href="#/music">音乐</a></nav>',
    '<h1>今日推荐</h1>',
    '<a href="/p/1"><img data-src="/cover/1.jpg" alt="封面一"><p>第一部影片的详细介绍</p></a>',
    '<a href="./p/2">第二部影片</a>',
    '<a href="https://other.com/x">外链</a>',
    '<div class="card" style="color:#333;position:fixed" onclick="bad()">正文段落</div>',
    '</body></html>',
].join('');
const base = 'https://demo.example.com/list/index.html';
const appView = htmlLite.parseAppView(sample, base);
if (appView.tabs.length !== 3) problems.push('卡片解析：导航标签数量异常 ' + appView.tabs.length);
if (appView.cards.length !== 1) problems.push('卡片解析：图片卡片未提取 ' + appView.cards.length);
if (appView.cards[0] && appView.cards[0].image !== 'https://demo.example.com/cover/1.jpg') {
    problems.push('卡片解析：懒加载图片未绝对化');
}
if (!appView.items.some(item => item.href === 'https://demo.example.com/list/p/2')) {
    problems.push('卡片解析：相对路径链接解析失败');
}
if (appView.items.some(item => item.href.indexOf('other.com') >= 0)) problems.push('卡片解析：外链未被过滤');
if (!appView.tabs.some(tab => tab.href.indexOf('#/music') >= 0)) problems.push('卡片解析：hash 路由被误丢');
checked.push('卡片化解析（导航 / 卡片 / 列表 / 外链过滤 / hash 路由）');

const css = '.card{color:#fff;background:#111;padding:12px} h1{color:red;position:fixed} p{font-size:14px}';
const rules = htmlLite.extractCssRules(css);
if (rules.length !== 3) problems.push('CSS 规则提取失败 ' + rules.length);
if (rules[0].style.indexOf('background') < 0) problems.push('CSS 内联：background 丢失');
if (rules[1].style.indexOf('position') >= 0) problems.push('CSS 内联：position 未过滤');
const rich = htmlLite.toRichNodes(sample, base, css);
if (/<script/i.test(rich) || /\son[a-z]+=/i.test(rich)) problems.push('富文本清洗：脚本或事件属性残留');
if (rich.indexOf('https://demo.example.com/cover/1.jpg') < 0) problems.push('富文本清洗：图片未绝对化');
if (rich.indexOf('style=') < 0) problems.push('富文本清洗：内联样式未保留');
if (rich.replace(/<[^>]+>/g, '').replace(/\s+/g, '').length < 20) problems.push('富文本清洗：正文被清空');
checked.push('网页原貌 rich-text 清洗与 CSS 内联');

if (!htmlLite.isScriptDriven('<html><body><script src="/a.js"></script><div id="app"></div></body></html>')) {
    problems.push('未识别脚本驱动页面');
}
if (htmlLite.isScriptDriven('<html><body><p>' + '很长的正文内容'.repeat(200) + '</p></body></html>')) {
    problems.push('静态长文被误判为脚本驱动');
}
checked.push('脚本驱动页面识别');

const lrc = player.parseLrc('[00:12.30]第一行歌词\n[01:02.00]第二行歌词\n无效行');
if (lrc.length !== 2 || lrc[0].time < 12 || lrc[0].time > 13) problems.push('歌词解析失败');
if (player.currentLyricIndex(lrc, 70) !== 1) problems.push('歌词行定位失败');
if (player.formatTime(65) !== '01:05') problems.push('时间格式化失败 ' + player.formatTime(65));
checked.push('播放器歌词 / 时间纯函数');

const split = appClient.splitName('林俊杰 - 江南.mp3');
if (split.artist !== '林俊杰' || split.title !== '江南') problems.push('曲库文件名解析失败 ' + JSON.stringify(split));
if (!appClient.isVideo('movie.mp4') || appClient.isVideo('song.mp3')) problems.push('视频类型识别失败');
if (appClient.imageUrl('https://bighat.icu:7000', 'https://img.example.com/a.jpg').indexOf('/api/img?url=') < 0) {
    problems.push('封面代理 URL 构造失败');
}
checked.push('应用客户端 URL / 文件名 / 类型纯函数');

// ---- 线上：真实接口契约 ----
function online() {
    const site = 'https://bighat.icu:7000';
    return appClient.detectApp(site).then(sites => {
        if (!Array.isArray(sites) || sites.length === 0) {
            problems.push('应用探测失败（未拿到 /api/sites）');
            return null;
        }
        checked.push('应用探测 /api/sites（' + sites.length + ' 个站点）');
        return Promise.all([
            appClient.getLibrary(site),
            appClient.getTasks(site),
            appClient.searchAll(site, '江南', sites, 'music'),
        ]).then(results => {
            const library = results[0];
            const tasks = results[1];
            const search = results[2];
            if (!Array.isArray(library)) problems.push('曲库返回结构异常');
            if (library.length > 0) {
                const track = library[0];
                for (const key of ['id', 'title', 'artist', 'src', 'sizeText', 'video']) {
                    if (!(key in track)) problems.push('曲库条目缺字段 ' + key);
                }
                if (track.src.indexOf('/api/stream?file=') < 0) problems.push('曲库播放地址异常');
            }
            checked.push('曲库 /api/browse（' + library.length + ' 首）');
            if (tasks.length > 0 && !('percent' in tasks[0])) problems.push('任务缺进度字段');
            checked.push('下载任务 /api/site/tasks（' + tasks.length + ' 个）');
            if (search.total === 0) problems.push('搜索无结果（接口可能变更）');
            if (search.groups.length > 0 && search.groups[0].items.length > 0) {
                const item = search.groups[0].items[0];
                for (const key of ['id', 'title', 'siteId', 'kindText']) {
                    if (!(key in item)) problems.push('搜索条目缺字段 ' + key);
                }
                return appClient.getDetail(site, item.siteId, item.id).then(detail => {
                    const track = appClient.buildTrackFromItem(site, item, detail);
                    if (!track.src || !track.title) problems.push('播放条目构造失败');
                    if (typeof detail.lyric !== 'string') problems.push('详情歌词字段异常');
                    checked.push('搜索 → 详情 → 播放条目（歌词 ' + detail.lyric.length + ' 字）');
                });
            }
            return null;
        });
    }).catch(error => {
        problems.push('线上接口异常：' + error.message);
    });
}

// ---- 页面端到端：模拟用户点开调试站点，检查最终渲染状态 ----
function simulatePage() {
    const site = 'https://bighat.icu:7000';
    global.wx.navigateTo = () => {};
    global.wx.redirectTo = () => {};
    global.wx.switchTab = () => {};
    global.wx.stopPullDownRefresh = () => {};
    global.getCurrentPages = () => [];
    let definition = null;
    global.Page = object => { definition = object; };
    require(path.join(ROOT, 'pages', 'browse', 'browse.js'));
    if (!definition) {
        problems.push('浏览页未注册为 Page');
        return Promise.resolve();
    }
    const context = Object.assign({}, definition, {
        data: JSON.parse(JSON.stringify(definition.data)),
        setData(patch) {
            for (const key in patch) {
                if (Object.prototype.hasOwnProperty.call(patch, key)) this.data[key] = patch[key];
            }
        },
    });
    for (const key in definition) {
        if (typeof definition[key] === 'function') context[key] = definition[key].bind(context);
    }
    context.onLoad({ url: site });

    return new Promise(resolve => {
        let waited = 0;
        const timer = setInterval(() => {
            waited += 200;
            if (context.data.viewState !== 'ready' && waited < 12000) return;
            clearInterval(timer);
            const data = context.data;
            if (data.viewState !== 'ready') problems.push('页面未进入 ready 状态');
            if (!data.hasApp) problems.push('页面未识别为应用站点');
            if (data.mainTab !== 'app') problems.push('页面未默认切到应用视图');
            if (data.library.length === 0) problems.push('页面曲库为空');
            if (data.sites.length === 0) problems.push('页面资源站点清单为空');
            if (!data.richAvailable) problems.push('「网页原貌」内容为空');
            if (data.snapshot) problems.push('实时成功后仍在显示离线快照');
            if (['cards', 'page', 'reader'].indexOf(data.viewMode) < 0) problems.push('视图模式异常');
            checked.push('页面端到端：' + data.viewState + ' / ' + data.mainTab
                + ' / 曲库 ' + data.library.length + ' / 站点 ' + data.sites.length
                + ' / 富文本 ' + data.richHtml.length + ' 字符 / 视图 ' + data.viewMode);
            resolve();
        }, 200);
    });
}

const withOnline = process.argv.indexOf('--online') >= 0 || process.argv.indexOf('--page') >= 0;
const withPage = process.argv.indexOf('--page') >= 0;
const run = withOnline
    ? online().then(result => (withPage ? simulatePage() : result))
    : Promise.resolve();
run.then(() => {
    console.log('检查通过项: ' + checked.length);
    for (const item of checked) console.log('  ✓ ' + item);
    if (problems.length > 0) {
        console.log('\n发现 ' + problems.length + ' 个问题:');
        for (const item of problems) console.log(' - ' + item);
        process.exit(1);
    }
    const summary = withPage
        ? '线上联调 + 页面端到端全部通过'
        : (withOnline ? '线上联调全部通过' : '离线自检全部通过（加 --online 验证接口，--page 模拟页面）');
    console.log(summary);
});
