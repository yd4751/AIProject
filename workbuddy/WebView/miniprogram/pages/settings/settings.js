const checker = require('../../utils/site-checker');
const proxy = require('../../utils/request-proxy');
const { createLocalSiteStore } = require('../../utils/site-store');
const { getDebugSite, getFilingChecklist } = require('../../utils/debug-site');

const store = createLocalSiteStore({
    storageKey: 'sitedock-sites-v1',
});

const TIMEOUT_OPTIONS = [
    { value: 3000, label: '3 秒' },
    { value: 6000, label: '6 秒' },
    { value: 10000, label: '10 秒' },
    { value: 15000, label: '15 秒' },
];

Page({
    data: {
        timeoutLabels: [],
        timeoutIndex: 1,
        siteCount: 0,
        statusCacheCount: 0,
        proxyTemplate: '',
        proxyToken: '',
        proxyOn: false,
        proxyError: '',
        debugSite: getDebugSite(),
        debugChecklist: getFilingChecklist(),
    },

    onShow() {
        const config = proxy.getProxyConfig();
        this.setData({
            proxyTemplate: config.template,
            proxyToken: config.token,
            proxyOn: config.template !== '',
            proxyError: '',
        });
        const settings = checker.getSettings();
        let index = 1;
        for (let i = 0; i < TIMEOUT_OPTIONS.length; i += 1) {
            if (TIMEOUT_OPTIONS[i].value === settings.timeout) {
                index = i;
                break;
            }
        }
        const statusMap = checker.loadStatusMap();
        let cacheCount = 0;
        for (const key in statusMap) {
            if (Object.prototype.hasOwnProperty.call(statusMap, key)) {
                cacheCount += 1;
            }
        }
        store.list().then(result => {
            this.setData({
                timeoutLabels: TIMEOUT_OPTIONS.map(option => option.label),
                timeoutIndex: index,
                siteCount: result.error ? 0 : (Array.isArray(result.data) ? result.data.length : 0),
                statusCacheCount: cacheCount,
            });
        });
    },

    handleTimeoutChange(event) {
        const index = Number(event.detail.value);
        if (!Number.isFinite(index) || index < 0 || index >= TIMEOUT_OPTIONS.length) {
            return;
        }
        checker.saveSettings({ timeout: TIMEOUT_OPTIONS[index].value });
        wx.vibrateShort({ type: 'light' });
        this.setData({ timeoutIndex: index });
    },

    handleProxyTemplateInput(event) {
        this.setData({ proxyTemplate: event.detail.value, proxyError: '' });
    },

    handleProxyTokenInput(event) {
        this.setData({ proxyToken: event.detail.value, proxyError: '' });
    },

    handleSaveProxy() {
        const template = this.data.proxyTemplate.trim();
        if (template !== '' && !/^https?:\/\//i.test(template)) {
            this.setData({ proxyError: '代理地址需以 https:// 开头' });
            return;
        }
        if (template !== '' && !/^https:\/\//i.test(template)) {
            this.setData({ proxyError: '非 https 代理仅能在开发者工具关闭域名校验时使用' });
            return;
        }
        proxy.saveProxyConfig({ template, token: this.data.proxyToken.trim() });
        wx.vibrateShort({ type: 'light' });
        this.setData({
            proxyOn: template !== '',
            proxyError: '',
        });
        wx.showToast({ title: template === '' ? '已关闭' : '已保存', icon: 'success' });
    },

    handleClearProxy() {
        proxy.clearProxyConfig();
        wx.vibrateShort({ type: 'light' });
        this.setData({
            proxyTemplate: '',
            proxyToken: '',
            proxyOn: false,
            proxyError: '',
        });
        wx.showToast({ title: '已清除', icon: 'success' });
    },

    handleCopyLegalDomain() {
        const value = getDebugSite().legalDomain;
        wx.setClipboardData({ data: value });
        wx.showToast({ title: '已复制登记值', icon: 'none' });
    },

    handleRestoreDebugSite() {
        const site = getDebugSite();
        store.list().then(result => {
            const items = result.error || !Array.isArray(result.data) ? [] : result.data;
            const exists = items.some(item => item.url === site.url);
            if (exists) {
                wx.showToast({ title: '站点已在列表中', icon: 'none' });
                return;
            }
            store.create({
                name: site.name,
                url: site.url,
                group: site.group,
                note: site.note,
                pinned: true,
            }).then(created => {
                if (created.error || !created.data) {
                    wx.showToast({ title: '添加失败', icon: 'none' });
                    return;
                }
                wx.vibrateShort({ type: 'light' });
                wx.showToast({ title: '已恢复到站点列表', icon: 'success' });
            });
        });
    },

    handleClearCache() {
        if (this.data.statusCacheCount === 0) {
            wx.showToast({ title: '没有检测缓存', icon: 'none' });
            return;
        }
        checker.clearStatusCache();
        wx.vibrateShort({ type: 'light' });
        this.setData({ statusCacheCount: 0 });
        wx.showToast({ title: '已清除', icon: 'success' });
    },

    handleClearAll() {
        wx.showModal({
            title: '清空全部站点',
            content: '将删除本机保存的全部 ' + this.data.siteCount + ' 个站点（不影响站点本身），此操作不可恢复。',
            confirmColor: '#d4380d',
            success: result => {
                if (!result.confirm) {
                    return;
                }
                wx.showModal({
                    title: '再次确认',
                    content: '真的要清空吗？',
                    confirmColor: '#d4380d',
                    success: again => {
                        if (!again.confirm) {
                            return;
                        }
                        wx.removeStorageSync('sitedock-sites-v1');
                        checker.clearStatusCache();
                        wx.vibrateShort({ type: 'medium' });
                        this.setData({ siteCount: 0, statusCacheCount: 0 });
                        wx.showToast({ title: '已清空', icon: 'success' });
                    },
                });
            },
        });
    },
});
