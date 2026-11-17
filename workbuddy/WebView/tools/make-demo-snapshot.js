// 生成调试站点的「离线快照」，供预览面板或断网时秒开结构化的 APP 界面。
// 同时读取站点自带的 JSON 接口，把真实曲库 / 站点 / 任务写进快照，
// 不再生成一整页文字（那条路对脚本驱动的站点毫无用处）。
//
//   node tools/make-demo-snapshot.js
//
// 产物：miniprogram/data/demo-snapshot.js（CommonJS 模块，勿手工编辑）

const fs = require('fs');
const path = require('path');
const https = require('https');

const ROOT = path.resolve(__dirname, '..');
const TARGET = 'https://bighat.icu:7000';
const OUT = path.join(ROOT, 'miniprogram', 'data', 'demo-snapshot.js');

global.wx = {
    getStorageSync: () => '',
    setStorageSync: () => {},
    removeStorageSync: () => {},
    request() {},
};
const { parseHtml } = require(path.join(ROOT, 'miniprogram', 'utils', 'html-lite.js'));

function get(target) {
    return new Promise((resolve, reject) => {
        const req = https.get(target, {
            rejectUnauthorized: false,
            timeout: 12000,
            headers: { Accept: 'text/html,application/json,*/*' },
        }, res => {
            const chunks = [];
            res.on('data', chunk => chunks.push(chunk));
            res.on('end', () => resolve({ status: res.statusCode, text: Buffer.concat(chunks).toString('utf8') }));
        });
        req.on('timeout', () => { req.destroy(); reject(new Error('抓取超时')); });
        req.on('error', error => reject(new Error(error.code || error.message)));
    });
}

function json(target) {
    return get(target).then(result => {
        if (result.status !== 200) return null;
        try { return JSON.parse(result.text); } catch (error) { return null; }
    }).catch(() => null);
}

function splitName(name) {
    const raw = String(name || '').replace(/\.[^.]+$/, '').trim();
    const parts = raw.split(/\s+[-–—]\s+/);
    return parts.length >= 2
        ? { artist: parts[0].trim(), title: parts.slice(1).join(' - ').trim() }
        : { artist: '本地文件', title: raw || '未命名' };
}

function sizeText(bytes) {
    const value = Number(bytes) || 0;
    if (value >= 1024 * 1024) return (value / 1024 / 1024).toFixed(1) + ' MB';
    if (value >= 1024) return (value / 1024).toFixed(0) + ' KB';
    return value + ' B';
}

(async () => {
    const [page, libraryData, sitesData, tasksData] = await Promise.all([
        get(TARGET),
        json(TARGET + '/api/browse?dir=@downloads'),
        json(TARGET + '/api/sites'),
        json(TARGET + '/api/site/tasks'),
    ]);
    if (page.status !== 200) throw new Error('站点返回 ' + page.status);

    const parsed = parseHtml(page.text, TARGET);
    const blocks = parsed.blocks
        .filter(block => block.type !== 'image')
        .slice(0, 4)
        .map((block, index) => ({
            type: block.type,
            key: 'b' + index,
            text: String(block.text || '').slice(0, 120),
            level: block.level || 2,
        }));
    if (blocks.length === 0) {
        blocks.push({ type: 'heading', key: 'b0', text: parsed.title || '离线快照', level: 2 });
        blocks.push({ type: 'text', key: 'b1', text: '联网后会自动切换到站点实时数据。', level: 2 });
    }

    const files = (libraryData && libraryData.files) || [];
    const library = files.map(file => {
        const parts = splitName(file.name);
        const ext = String(file.name || '').split('.').pop().toLowerCase();
        return {
            id: 'local|' + (file.path || file.name),
            title: parts.title,
            artist: parts.artist,
            album: '本地曲库',
            cover: '',
            sizeText: sizeText(file.size),
            ext: ext,
            video: ['mp4', 'mkv', 'mov', 'webm', 'avi'].indexOf(ext) >= 0,
            kind: 'local',
        };
    });
    const sites = ((sitesData && sitesData.sites) || []).map(site => ({
        id: site.id || '',
        name: site.name || site.id || '未命名站点',
        kind: site.kind === 'movie' ? 'movie' : 'music',
        kindText: site.kind === 'movie' ? '影视' : '音乐',
        enabled: site.enabled !== false,
    }));
    const tasks = ((tasksData && tasksData.tasks) || []).map(task => ({
        id: task.id || '',
        title: task.title || task.name || '下载任务',
        artist: task.artist || '',
        siteName: task.siteName || task.site || '',
        percent: Math.max(0, Math.min(100, Number(task.percent) || 0)),
        status: task.status || '',
        statusText: task.status === 'done' ? '已完成' : '下载中',
        sizeText: sizeText(task.total),
    }));

    const data = {
        url: TARGET,
        host: parsed.host,
        title: parsed.title || parsed.host,
        description: '原生 APP 视图 · 曲库、搜索、站点资源与播放器',
        capturedAt: new Date().toISOString(),
        blocks: blocks,
        links: parsed.links || [],
        app: {
            title: parsed.title || '小鹅 · 影音播放器',
            library: library,
            sites: sites,
            tasks: tasks,
        },
    };

    fs.mkdirSync(path.dirname(OUT), { recursive: true });
    fs.writeFileSync(OUT,
        '// 由 tools/make-demo-snapshot.js 生成，请勿手工编辑\n' +
        '// 离线兜底：保留结构化的 APP 数据，避免再退回到「一整页文字」的预览效果。\n' +
        'module.exports = ' + JSON.stringify(data, null, 4) + ';\n',
        'utf8');

    console.log('已生成:', path.relative(ROOT, OUT));
    console.log('站点:', data.title, '| 曲库:', library.length, '| 资源站点:', sites.length, '| 任务:', tasks.length);
    console.log('抓取时间:', data.capturedAt, '| 字节:', fs.statSync(OUT).size);
})().catch(error => {
    console.error('生成失败:', error.message);
    console.error('保留已有快照不变（若有）');
    process.exit(1);
});
