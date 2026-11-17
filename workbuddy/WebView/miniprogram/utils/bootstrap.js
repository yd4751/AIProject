// 首次启动时把调试站点写入本地列表，避免打开小程序是一片空态。
// 纯逻辑放在这里，便于在 Node 环境下做冒烟测试。

const { getDebugSite, BUILTIN_MARKER_KEY } = require('./debug-site');

function ensureBuiltinSite(store, io) {
    const storage = io || wx;
    if (storage.getStorageSync(BUILTIN_MARKER_KEY)) {
        return Promise.resolve({ created: false, reason: 'already' });
    }
    return store.list().then(result => {
        if (storage.getStorageSync(BUILTIN_MARKER_KEY)) {
            return { created: false, reason: 'already' };
        }
        if (result.error) {
            return { created: false, reason: 'error' };
        }
        const items = Array.isArray(result.data) ? result.data : [];
        if (items.length > 0) {
            storage.setStorageSync(BUILTIN_MARKER_KEY, true);
            return { created: false, reason: 'has-sites' };
        }
        const site = getDebugSite();
        return store.create({
            name: site.name,
            url: site.url,
            group: site.group,
            note: site.note,
            pinned: true,
        }).then(created => {
            if (created.error || !created.data) {
                return { created: false, reason: 'error' };
            }
            storage.setStorageSync(BUILTIN_MARKER_KEY, true);
            return { created: true, reason: 'created' };
        });
    });
}

module.exports = {
    ensureBuiltinSite,
    BUILTIN_MARKER_KEY,
};
