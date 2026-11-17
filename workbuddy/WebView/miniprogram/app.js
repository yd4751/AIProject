const { createLocalSiteStore } = require('./utils/site-store');
const { ensureBuiltinSite } = require('./utils/bootstrap');

App({
    globalData: {
        appName: '站点坞',
    },

    onLaunch() {
        const store = createLocalSiteStore({ storageKey: 'sitedock-sites-v1' });
        ensureBuiltinSite(store, wx).catch(() => {
            // 初始化失败不影响启动，用户仍可手动添加站点
        });
    },
});
