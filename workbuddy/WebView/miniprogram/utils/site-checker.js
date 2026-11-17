// 站点健康检测：发起轻量 GET 请求，记录状态、往返延迟与检测时间。
// 状态缓存独立存储，不污染站点记录本身，方便上云时整体替换。
const { buildRequestUrl } = require('./request-proxy');

const STATUS_STORAGE_KEY = 'sitedock-status-v1';
const SETTINGS_STORAGE_KEY = 'sitedock-settings-v1';
const DEFAULT_TIMEOUT = 6000;
const BATCH_SIZE = 4;

function getSettings() {
    const value = wx.getStorageSync(SETTINGS_STORAGE_KEY);
    if (!value || typeof value !== 'object') {
        return { timeout: DEFAULT_TIMEOUT };
    }
    const timeout = Number(value.timeout);
    return {
        timeout: Number.isFinite(timeout) && timeout >= 2000 && timeout <= 30000 ? timeout : DEFAULT_TIMEOUT,
    };
}

function saveSettings(patch) {
    const current = getSettings();
    const next = {
        timeout: typeof patch.timeout === 'number' ? patch.timeout : current.timeout,
    };
    wx.setStorageSync(SETTINGS_STORAGE_KEY, next);
    return next;
}

function loadStatusMap() {
    const value = wx.getStorageSync(STATUS_STORAGE_KEY);
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
        return {};
    }
    return value;
}

function saveStatusMap(map) {
    wx.setStorageSync(STATUS_STORAGE_KEY, map);
}

function recordStatus(siteId, result) {
    const map = loadStatusMap();
    map[siteId] = {
        status: result.status,
        latency: result.latency,
        httpStatus: result.httpStatus,
        checkedAt: Date.now(),
    };
    saveStatusMap(map);
}

function checkSite(site, timeout) {
    const startedAt = Date.now();
    return new Promise(resolve => {
        wx.request({
            url: buildRequestUrl(site.url),
            method: 'GET',
            timeout: timeout,
            header: {
                Accept: 'text/html,application/xhtml+xml',
            },
            success: response => {
                const latency = Date.now() - startedAt;
                const code = response.statusCode;
                let status = 'online';
                if (code >= 500) {
                    status = 'warn';
                }
                resolve({ status, latency, httpStatus: code });
            },
            fail: () => {
                resolve({ status: 'offline', latency: Date.now() - startedAt, httpStatus: 0 });
            },
        });
    });
}

// 顺序分批检测全部站点；每个站点完成后通过 onUpdate(siteId, result) 通知页面增量刷新。
function checkAll(sites, timeout, onUpdate) {
    const list = Array.isArray(sites) ? sites.slice() : [];
    const effectiveTimeout = timeout || getSettings().timeout;
    let cursor = 0;
    let checked = 0;

    return new Promise(resolve => {
        function runBatch() {
            const batch = list.slice(cursor, cursor + BATCH_SIZE);
            if (batch.length === 0) {
                resolve({ checked });
                return;
            }
            cursor += batch.length;
            const tasks = batch.map(site => checkSite(site, effectiveTimeout).then(result => {
                recordStatus(site.id, result);
                checked += 1;
                if (typeof onUpdate === 'function') {
                    onUpdate(site.id, result, checked, list.length);
                }
            }));
            Promise.all(tasks).then(runBatch);
        }
        runBatch();
    });
}

module.exports = {
    getSettings,
    saveSettings,
    loadStatusMap,
    recordStatus,
    checkSite,
    checkAll,
    clearStatusCache() {
        wx.removeStorageSync(STATUS_STORAGE_KEY);
    },
};
