// 应用站点客户端：面向「带 JSON 接口的自建应用」，不再把网页拆成文字。
// 小程序原生组件直接调用站点 API，拿到结构化数据后渲染成可操作的 APP 界面。
// 所有请求都走站点同源地址，不经过抓取代理（代理只用于纯 HTML 站点）。
const VIDEO_EXT = ['mp4', 'mkv', 'mov', 'webm', 'avi', 'm4v', 'flv'];
const AUDIO_EXT = ['mp3', 'flac', 'wav', 'm4a', 'aac', 'ogg', 'wma'];

function originOf(url) {
    const match = /^(https?:\/\/[^/?#]+)/i.exec(String(url || ''));
    return match ? match[1] : '';
}

function apiUrl(base, path) {
    return originOf(base) + path;
}

function encode(value) {
    return encodeURIComponent(value === undefined || value === null ? '' : String(value));
}

function requestJSON(base, path, timeout) {
    return new Promise((resolve, reject) => {
        const origin = originOf(base);
        if (!origin) {
            reject(new Error('站点地址无效'));
            return;
        }
        if (typeof wx === 'undefined' || typeof wx.request !== 'function') {
            reject(new Error('当前环境不支持网络请求'));
            return;
        }
        wx.request({
            url: origin + path,
            method: 'GET',
            timeout: timeout || 12000,
            header: { Accept: 'application/json' },
            success: response => {
                if (response.statusCode >= 400) {
                    reject(new Error('接口返回 ' + response.statusCode));
                    return;
                }
                let data = response.data;
                if (typeof data === 'string') {
                    try {
                        data = JSON.parse(data);
                    } catch (error) {
                        reject(new Error('接口返回的不是 JSON'));
                        return;
                    }
                }
                if (!data || typeof data !== 'object') {
                    reject(new Error('接口数据为空'));
                    return;
                }
                if (data.ok === false) {
                    reject(new Error(data.error || '接口调用失败'));
                    return;
                }
                resolve(data);
            },
            fail: error => {
                const message = error && error.errMsg ? error.errMsg : '';
                reject(new Error(message.indexOf('timeout') >= 0 ? '接口请求超时' : '无法连接到应用接口'));
            },
        });
    });
}

// 探测站点是否为「带 API 的应用」。普通站点会很快失败，不影响后续 HTML 浏览。
function detectApp(base) {
    return requestJSON(base, '/api/sites', 8000)
        .then(data => (Array.isArray(data.sites) ? data.sites.map(normalizeSite) : []))
        .catch(() => null);
}

function fileExt(name) {
    const dot = String(name || '').lastIndexOf('.');
    return dot < 0 ? '' : String(name).slice(dot + 1).toLowerCase();
}

function isVideo(nameOrExt) {
    const ext = nameOrExt.indexOf('.') >= 0 ? fileExt(nameOrExt) : String(nameOrExt || '').toLowerCase();
    return VIDEO_EXT.indexOf(ext) >= 0;
}

function isAudio(nameOrExt) {
    const ext = nameOrExt.indexOf('.') >= 0 ? fileExt(nameOrExt) : String(nameOrExt || '').toLowerCase();
    return AUDIO_EXT.indexOf(ext) >= 0;
}

function formatSize(bytes) {
    const value = Number(bytes) || 0;
    if (value >= 1024 * 1024 * 1024) return (value / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    if (value >= 1024 * 1024) return (value / 1024 / 1024).toFixed(1) + ' MB';
    if (value >= 1024) return (value / 1024).toFixed(0) + ' KB';
    return value + ' B';
}

function splitName(name) {
    const raw = String(name || '').replace(/\.[^.]+$/, '').trim();
    const parts = raw.split(/\s+[-–—]\s+/);
    if (parts.length >= 2) {
        return { artist: parts[0].trim(), title: parts.slice(1).join(' - ').trim() };
    }
    return { artist: '本地文件', title: raw || '未命名' };
}

// 远程封面统一走站点图片代理，规避外域防盗链与小程序图片域名限制。
function imageUrl(base, url) {
    if (!url) return '';
    if (url.indexOf('data:') === 0 || url.indexOf('/api/') === 0) return url;
    if (url.indexOf('http') !== 0) return apiUrl(base, url);
    return apiUrl(base, '/api/img?url=' + encode(url));
}

function streamUrl(base, path) {
    return apiUrl(base, '/api/stream?file=' + encode(path));
}

function playUrl(base, siteId, id) {
    return apiUrl(base, '/api/site/play?site=' + encode(siteId) + '&id=' + encode(id));
}

function toTrack(file, base) {
    const name = file.name || file.title || '未命名';
    const parts = splitName(name);
    const ext = fileExt(name);
    return {
        id: 'local|' + (file.path || name),
        title: parts.title,
        artist: parts.artist,
        album: '本地曲库',
        cover: imageUrl(base, file.cover || ''),
        src: streamUrl(base, file.path || name),
        sizeText: formatSize(file.size),
        ext: ext,
        video: isVideo(ext),
        kind: 'local',
        lyric: '',
        size: Number(file.size) || 0,
        path: file.path || '',
    };
}

function getLibrary(base) {
    return requestJSON(base, '/api/browse?dir=@downloads')
        .then(data => {
            const files = Array.isArray(data.files) ? data.files : [];
            return files
                .filter(file => isAudio(file.name) || isVideo(file.name))
                .map(file => toTrack(file, base));
        });
}

function normalizeSite(raw) {
    return {
        id: raw.id || '',
        name: raw.name || raw.id || '未命名站点',
        kind: raw.kind === 'movie' ? 'movie' : 'music',
        enabled: raw.enabled !== false,
        lyric: !!raw.lyric,
        builtin: !!raw.builtin,
        kindText: raw.kind === 'movie' ? '影视' : '音乐',
    };
}

function getSites(base) {
    return requestJSON(base, '/api/sites', 10000)
        .then(data => (Array.isArray(data.sites) ? data.sites.map(normalizeSite) : []));
}

function normalizeItem(raw, site, base) {
    const title = stripAmp(raw.title || '未命名');
    return {
        id: String(raw.id || ''),
        title: title,
        artist: stripAmp(raw.artist || raw.album || ''),
        cover: imageUrl(base, raw.cover || ''),
        hasCover: !!raw.cover,
        hasLyric: !!raw.hasLyric,
        url: raw.url || '',
        siteId: site.id,
        siteName: site.name,
        kind: site.kind,
        kindText: site.kind === 'movie' ? '影视' : '音乐',
    };
}

function stripAmp(text) {
    return String(text)
        .replace(/&amp;/g, '&')
        .replace(/&lt;/g, '<')
        .replace(/&gt;/g, '>')
        .replace(/&quot;/g, '"')
        .replace(/&#39;/g, "'")
        .trim();
}

function searchAll(base, query, sites, kind) {
    const target = (sites || []).filter(site => site.enabled && (!kind || kind === 'all' || site.kind === kind));
    if (target.length === 0) {
        return Promise.resolve({ groups: [], total: 0, message: '没有启用的检索站点' });
    }
    const q = String(query || '').trim();
    const jobs = target.map(site => requestJSON(
        base,
        '/api/site/search?site=' + encode(site.id) + '&q=' + encode(q),
        15000
    ).then(data => ({
        site: site,
        items: (Array.isArray(data.items) ? data.items : []).map(item => normalizeItem(item, site, base)),
        error: '',
    })).catch(error => ({
        site: site,
        items: [],
        error: error.message || '检索失败',
    })));
    return Promise.all(jobs).then(groups => ({
        groups: groups,
        total: groups.reduce((sum, group) => sum + group.items.length, 0),
        message: '',
    }));
}

function getDetail(base, siteId, id) {
    return requestJSON(base, '/api/site/detail?site=' + encode(siteId) + '&id=' + encode(id), 15000)
        .then(data => ({
            subject: data.subject || null,
            lyric: typeof data.lyric === 'string' ? data.lyric : '',
            playUrl: typeof data.audioUrl === 'string' && data.audioUrl ? data.audioUrl : '',
        }));
}

function getTasks(base) {
    return requestJSON(base, '/api/site/tasks', 10000)
        .then(data => (Array.isArray(data.tasks) ? data.tasks : []).map(task => ({
            id: task.id || '',
            title: task.title || task.name || '下载任务',
            artist: task.artist || '',
            siteName: task.siteName || task.site || '',
            percent: Math.max(0, Math.min(100, Number(task.percent) || 0)),
            status: task.status || '',
            statusText: taskStatusText(task.status, task.error),
            sizeText: formatSize(task.total),
        })));
}

function taskStatusText(status, error) {
    if (status === 'done') return '已完成';
    if (status === 'error' || error) return '失败';
    if (status === 'downloading') return '下载中';
    return '排队中';
}

function startDownload(base, item) {
    const path = '/api/site/download?site=' + encode(item.siteId)
        + '&id=' + encode(item.id)
        + '&title=' + encode(item.title)
        + '&artist=' + encode(item.artist);
    return requestJSON(base, path, 15000);
}

function lookup(base, title, kind) {
    const path = '/api/douban/lookup?title=' + encode(title)
        + '&kind=' + encode(kind || 'all')
        + '&prefer=' + encode(kind === 'music' ? 'music' : 'movie');
    return requestJSON(base, path, 15000).then(data => {
        const items = Array.isArray(data.items) ? data.items : [];
        return items.map(item => ({
            id: item.id || '',
            title: item.title || '',
            year: item.year || '',
            cover: imageUrl(base, item.cover || ''),
            rating: item.rating && item.rating.value ? String(item.rating.value) : '',
            abstract: item.abstract || '',
            url: item.url || '',
        }));
    });
}

function buildTrackFromItem(base, item, detail) {
    const subject = detail && detail.subject ? detail.subject : null;
    const cover = subject && subject.cover ? subject.cover : item.cover;
    const src = detail && detail.playUrl ? detail.playUrl : playUrl(base, item.siteId, item.id);
    return {
        id: item.siteId + '|' + item.id,
        title: item.title,
        artist: item.artist || (subject && subject.singer ? subject.singer.join(' / ') : ''),
        album: item.siteName || item.album || '',
        cover: imageUrl(base, cover),
        src: src,
        sizeText: item.kindText,
        ext: 'mp3',
        video: false,
        kind: 'remote',
        lyric: detail ? detail.lyric : '',
        siteId: item.siteId,
        itemId: item.id,
    };
}

module.exports = {
    originOf,
    apiUrl,
    requestJSON,
    detectApp,
    getLibrary,
    getSites,
    searchAll,
    getDetail,
    getTasks,
    startDownload,
    lookup,
    buildTrackFromItem,
    streamUrl,
    playUrl,
    imageUrl,
    formatSize,
    splitName,
    isVideo,
    isAudio,
    fileExt,
};
