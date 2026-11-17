const {
    fetchPage,
    parseHtml,
    parseAppView,
    isScriptDriven,
    toRichNodes,
    fetchStyles,
} = require('../../utils/html-lite');
const { createLocalSiteStore } = require('../../utils/site-store');
const { isUsingProxy, diagnoseUrl } = require('../../utils/request-proxy');
const { isDebugSite } = require('../../utils/debug-site');
const checker = require('../../utils/site-checker');
const appClient = require('../../utils/app-client');
const player = require('../../utils/player');
const snapshot = require('../../data/demo-snapshot.js');

const store = createLocalSiteStore({ storageKey: 'sitedock-sites-v1' });
const FONT_LEVELS = ['font-small', 'font-medium', 'font-large'];
const APP_TABS = [
    { key: 'library', label: '曲库' },
    { key: 'search', label: '搜索' },
    { key: 'sites', label: '站点' },
    { key: 'tasks', label: '任务' },
];
const KINDS = [
    { key: 'all', label: '全部' },
    { key: 'music', label: '音乐' },
    { key: 'movie', label: '影视' },
];

Page({
    data: {
        viewState: 'loading',
        errorMessage: '',
        reasons: [],
        proxyOn: false,
        url: '',
        host: '',
        loadedAt: 0,

        // 一级视图：检测到带 API 的应用时，可在「应用」与「网页」之间切换
        hasApp: false,
        mainTab: 'site',
        appTitle: '',
        appStatus: '',

        // 通用网页浏览
        viewMode: 'cards',
        pageTitle: '',
        description: '',
        hero: '',
        heroBroken: false,
        tabs: [],
        cards: [],
        items: [],
        headings: [],
        blocks: [],
        links: [],
        linkCountText: '',
        richHtml: '',
        richAvailable: false,
        scriptDriven: false,
        contentTip: '',

        // 应用加速模式
        appTabs: APP_TABS,
        appTab: 'library',
        appLoading: false,
        appError: '',
        library: [],
        libraryCountText: '',
        sites: [],
        tasks: [],
        query: '',
        kinds: KINDS,
        searchKind: 'all',
        searching: false,
        searchGroups: [],
        searchTotal: 0,
        searchHint: '',
        siteFilter: '',

        // 播放控件
        mini: { hasTrack: false, title: '', artist: '', cover: '', playing: false, progressText: '', percent: 0, error: '' },
        showPlayerSheet: false,
        sheetVideo: false,
        sheetSrc: '',
        lyricText: '',
        lyricLines: [],
        lyricIndex: -1,
        rateText: '1.0×',
        loopText: '列表循环',

        // 离线兜底
        snapshot: false,
        snapshotNote: '',
        snapshotError: '',
        snapshotReasons: [],
        showSnapshotReasons: false,

        fontClass: 'font-medium',
        fontLevel: 1,
    },

    onLoad(options) {
        const self = this;
        this.unsubscribe = player.subscribe(function (state) {
            self.syncPlayer(state);
        });
        const id = options && options.id ? decodeURIComponent(options.id) : '';
        const rawUrl = options && options.url ? decodeURIComponent(options.url) : '';
        if (id !== '') {
            store.list().then(result => {
                const items = result && Array.isArray(result.data) ? result.data : [];
                let site = null;
                for (let i = 0; i < items.length; i += 1) {
                    if (items[i].id === id) { site = items[i]; break; }
                }
                self.startLoad(site ? site.url : rawUrl);
            });
            return;
        }
        this.startLoad(rawUrl);
    },

    onUnload() {
        if (this.unsubscribe) this.unsubscribe();
    },

    startLoad(url) {
        if (!url) {
            this.setData({ viewState: 'error', errorMessage: '没有可浏览的站点地址' });
            return;
        }
        const canUseSnapshot = isDebugSite(url) && snapshot && snapshot.app;
        const base = {
            url: url,
            errorMessage: '',
            reasons: [],
            snapshotError: '',
            snapshotReasons: [],
            showSnapshotReasons: false,
        };

        if (canUseSnapshot) {
            // 缓存优先：先给出结构化的 APP 界面，不再退化成一整页文字
            this.setData(Object.assign({}, base, {
                viewState: 'ready',
                host: snapshot.host,
                pageTitle: snapshot.title,
                description: snapshot.description,
                hasApp: true,
                mainTab: 'app',
                appTitle: snapshot.app.title,
                appStatus: '离线快照',
                library: snapshot.app.library || [],
                libraryCountText: this.countText((snapshot.app.library || []).length, '首'),
                sites: snapshot.app.sites || [],
                tasks: [],
                snapshot: true,
                snapshotNote: '离线快照 · 正在连接站点',
                blocks: this.toViewBlocks(snapshot.blocks),
                links: snapshot.links || [],
            }));
            wx.setNavigationBarTitle({ title: snapshot.app.title });
        } else {
            this.setData(Object.assign({}, base, {
                viewState: 'loading',
                hasApp: false,
                mainTab: 'site',
                snapshot: false,
                snapshotNote: '',
            }));
        }
        this.load(url);
    },

    toViewBlocks(blocks) {
        return (blocks || []).map((block, index) => {
            const item = { type: block.type, key: block.key || ('b' + index) };
            if (block.type === 'image') {
                item.src = block.src;
                item.broken = false;
            } else {
                item.text = block.text;
                item.level = block.level || 2;
            }
            return item;
        });
    },

    countText(count, unit) {
        return count > 0 ? count + ' ' + unit : '暂无内容';
    },

    load(url) {
        const self = this;
        const settings = checker.getSettings();
        const timeout = Math.max(settings.timeout, 10000);
        const keepSnapshot = this.data.snapshot === true && isDebugSite(url);

        // HTML 与接口探测彼此独立：只有接口能通时，网页视图空着也不影响应用模式
        const htmlJob = fetchPage(url, timeout)
            .then(html => ({ ok: true, html: html }))
            .catch(error => ({ ok: false, error: error }));
        const appJob = appClient.detectApp(url).catch(() => null);

        Promise.all([htmlJob, appJob]).then(results => {
            const pageResult = results[0];
            const sites = results[1];
            if (!pageResult.ok && !sites) throw pageResult.error;
            const html = pageResult.ok ? pageResult.html : '';
            const parsed = parseHtml(html, url);
            const appView = parseAppView(html, url);
            const scriptDriven = isScriptDriven(html);
            return fetchStyles(html, url, 6000).then(css => ({
                html: html,
                sites: sites,
                parsed: parsed,
                appView: appView,
                scriptDriven: scriptDriven,
                richHtml: toRichNodes(html, url, css),
            }));
        }).then(result => {
            const parsed = result.parsed;
            const appView = result.appView;
            const title = parsed.title === '' ? parsed.host : parsed.title;
            const richText = String(result.richHtml).replace(/<[^>]+>/g, '').replace(/\s+/g, '');
            const hasApp = Array.isArray(result.sites) && result.sites.length > 0;
            const tip = result.scriptDriven
                ? '这个页面由脚本动态渲染，静态内容有限。可切换「网页原貌」查看完整排版，或复制链接在浏览器打开。'
                : '';

            self.applyWebData({
                host: parsed.host,
                pageTitle: title,
                description: parsed.description,
                hero: appView.hero,
                tabs: appView.tabs,
                cards: appView.cards,
                items: appView.items,
                headings: appView.headings,
                blocks: self.toViewBlocks(parsed.blocks),
                links: parsed.links,
                linkCountText: self.linkCount(parsed.links),
                richHtml: result.richHtml,
                richAvailable: richText.length > 20,
                scriptDriven: result.scriptDriven,
                contentTip: tip,
                viewMode: appView.linkCount > 0 || richText.length <= 20 ? 'cards' : 'page',
            });

            self.setData({
                viewState: 'ready',
                snapshot: false,
                snapshotNote: '',
                snapshotError: '',
                snapshotReasons: [],
                hasApp: hasApp,
                mainTab: hasApp ? 'app' : 'site',
                appTitle: hasApp ? title : '',
                loadedAt: Date.now(),
            });
            wx.setNavigationBarTitle({ title: title });

            if (hasApp) self.enterApp(url, result.sites);
        }).catch(error => {
            const reasons = error && Array.isArray(error.reasons) ? error.reasons : [];
            const message = error && error.message ? error.message : '加载失败';
            if (keepSnapshot) {
                // 实时失败也不清空内容：保留快照，把原因折叠在横幅里
                self.setData({
                    snapshotNote: '实时连接失败，当前显示离线快照',
                    snapshotError: message,
                    snapshotReasons: reasons.length > 0 ? reasons : diagnoseUrl(url),
                    proxyOn: isUsingProxy(),
                });
                return;
            }
            self.setData({
                viewState: 'error',
                errorMessage: message,
                reasons: reasons.length > 0 ? reasons : diagnoseUrl(url),
                proxyOn: isUsingProxy(),
            });
        });
    },

    applyWebData(data) {
        this.setData(Object.assign({
            heroBroken: false,
        }, data));
    },

    linkCount(links) {
        const count = Array.isArray(links) ? links.length : 0;
        return count > 0 ? '站内链接 ' + count + ' 条' : '';
    },

    // ---- 应用加速模式：站点自带 JSON 接口时，用原生控件真正操作它 ----
    enterApp(url, sites) {
        this.refreshAppData(url, sites);
    },

    refreshAppData(url, sites) {
        const self = this;
        this.setData({ appLoading: true, appError: '' });
        Promise.all([
            appClient.getLibrary(url),
            appClient.getSites(url),
            appClient.getTasks(url),
        ]).then(results => {
            const library = results[0];
            const siteList = (sites && sites.length > 0) ? sites : results[1];
            self.setData({
                library: library,
                libraryCountText: self.countText(library.length, '首'),
                sites: siteList,
                tasks: results[2],
                appStatus: '已连接 · ' + siteList.length + ' 个资源站点',
                appLoading: false,
            });
        }).catch(error => {
            self.setData({
                appLoading: false,
                appError: error && error.message ? error.message : '接口数据加载失败',
            });
        });
    },

    refreshTasks() {
        const self = this;
        appClient.getTasks(this.data.url).then(tasks => {
            self.setData({ tasks: tasks });
        }).catch(() => {});
    },

    handleMainTab(event) {
        const key = event.currentTarget.dataset.key;
        if (!key) return;
        this.setData({ mainTab: key });
        if (key === 'app' && this.data.appTab === 'tasks') this.refreshTasks();
    },

    handleViewMode(event) {
        const key = event.currentTarget.dataset.key;
        if (!key) return;
        this.setData({ viewMode: key });
    },

    handleAppTab(event) {
        const key = event.currentTarget.dataset.key;
        if (!key) return;
        this.setData({ appTab: key });
        if (key === 'tasks') this.refreshTasks();
    },

    handleKindTap(event) {
        const key = event.currentTarget.dataset.key;
        if (!key) return;
        this.setData({ searchKind: key, searchTotal: 0 });
        if (this.data.query) this.runSearch();
    },

    handleSiteTap(event) {
        const index = Number(event.currentTarget.dataset.index);
        const site = this.data.sites[index];
        if (!site) return;
        this.setData({
            siteFilter: site.id,
            searchHint: '仅检索 ' + site.name,
            appTab: 'search',
            mainTab: 'app',
        });
    },

    handleClearSiteFilter() {
        this.setData({ siteFilter: '', searchHint: '' });
        if (this.data.query) this.runSearch();
    },

    handleQueryInput(event) {
        this.setData({ query: event.detail.value || '' });
    },

    handleClearQuery() {
        this.setData({ query: '', searchGroups: [], searchTotal: 0, searchHint: this.data.siteFilter ? this.data.searchHint : '' });
    },

    handleSearch() {
        this.runSearch();
    },

    runSearch() {
        const self = this;
        const q = String(this.data.query || '').trim();
        if (!q) {
            wx.showToast({ title: '输入关键词再搜索', icon: 'none' });
            return;
        }
        let sites = this.data.sites || [];
        if (this.data.siteFilter) {
            sites = sites.filter(site => site.id === this.data.siteFilter);
        }
        this.setData({ searching: true, searchGroups: [], searchTotal: 0, appTab: 'search', mainTab: 'app' });
        appClient.searchAll(this.data.url, q, sites, this.data.searchKind).then(result => {
            const groups = result.groups.map(group => Object.assign({}, group, { key: group.site.id }));
            self.setData({
                searching: false,
                searchGroups: groups,
                searchTotal: result.total,
                searchHint: result.total > 0 ? '找到 ' + result.total + ' 条结果' : (result.message || '没有找到匹配内容'),
            });
        }).catch(error => {
            self.setData({
                searching: false,
                searchHint: error && error.message ? error.message : '搜索失败',
            });
        });
    },

    handleLibraryTap(event) {
        const index = Number(event.currentTarget.dataset.index);
        const item = this.data.library[index];
        if (!item) return;
        if (!item.src) {
            wx.showToast({ title: '离线快照不可播放，请先联网', icon: 'none' });
            return;
        }
        player.play(this.data.library, index);
        if (item.video) this.setData({ showPlayerSheet: true });
    },

    handleResultTap(event) {
        const gi = Number(event.currentTarget.dataset.gi);
        const ii = Number(event.currentTarget.dataset.ii);
        const group = this.data.searchGroups[gi];
        const item = group && group.items ? group.items[ii] : null;
        if (!item) return;
        const self = this;
        wx.showLoading({ title: '准备播放', mask: true });
        appClient.getDetail(this.data.url, item.siteId, item.id).then(detail => {
            wx.hideLoading();
            const track = appClient.buildTrackFromItem(self.data.url, item, detail);
            player.playOne(track);
        }).catch(() => {
            wx.hideLoading();
            player.playOne(appClient.buildTrackFromItem(self.data.url, item, null));
        });
    },

    handleResultDownload(event) {
        const gi = Number(event.currentTarget.dataset.gi);
        const ii = Number(event.currentTarget.dataset.ii);
        const group = this.data.searchGroups[gi];
        const item = group && group.items ? group.items[ii] : null;
        if (!item) return;
        appClient.startDownload(this.data.url, item).then(() => {
            wx.showToast({ title: '已加入下载', icon: 'success' });
            this.refreshTasks();
        }).catch(error => {
            wx.showToast({ title: (error && error.message) || '下载失败', icon: 'none' });
        });
    },

    handleTaskRefresh() {
        this.refreshTasks();
    },

    // ---- 通用网页浏览 ----
    handleLinkTap(event) {
        this.openLink(event.currentTarget.dataset.href);
    },

    openLink(href) {
        if (!href) return;
        wx.vibrateShort({ type: 'light' });
        const pages = getCurrentPages();
        const target = '/pages/browse/browse?url=' + encodeURIComponent(href);
        if (pages.length >= 10) wx.redirectTo({ url: target });
        else wx.navigateTo({ url: target });
    },

    handleOpenOrigin() {
        wx.setClipboardData({ data: this.data.url });
    },

    handleImageError(event) {
        const index = event.currentTarget.dataset.index;
        if (typeof index !== 'number' && typeof index !== 'string') return;
        this.setData({ ['blocks[' + index + '].broken']: true });
    },

    handleHeroError() {
        this.setData({ heroBroken: true });
    },

    handleFontTap() {
        const next = (this.data.fontLevel + 1) % FONT_LEVELS.length;
        wx.vibrateShort({ type: 'light' });
        this.setData({ fontLevel: next, fontClass: FONT_LEVELS[next] });
    },

    handleRefreshTap() {
        if (!this.data.url || this.data.viewState === 'loading') return;
        wx.vibrateShort({ type: 'light' });
        if (this.data.snapshot) this.setData({ snapshotNote: '离线快照 · 正在重新连接' });
        this.load(this.data.url);
    },

    handleRetry() {
        if (!this.data.snapshot) this.setData({ viewState: 'loading' });
        this.load(this.data.url);
    },

    handleCopyLink() {
        wx.setClipboardData({ data: this.data.url });
    },

    handleToggleReasons() {
        this.setData({ showSnapshotReasons: !this.data.showSnapshotReasons });
    },

    handleCopyDiagnosis() {
        const lines = ['目标地址：' + this.data.url, '错误：' + this.data.errorMessage, ''];
        for (let i = 0; i < this.data.reasons.length; i += 1) lines.push((i + 1) + '. ' + this.data.reasons[i]);
        wx.setClipboardData({ data: lines.join('\n') });
    },

    handleCopySnapshotReasons() {
        const lines = ['目标地址：' + this.data.url, '错误：' + this.data.snapshotError, ''];
        for (let i = 0; i < this.data.snapshotReasons.length; i += 1) lines.push((i + 1) + '. ' + this.data.snapshotReasons[i]);
        wx.setClipboardData({ data: lines.join('\n') });
    },

    handleGoSettings() {
        wx.switchTab({ url: '/pages/settings/settings' });
    },

    // ---- 播放控件 ----
    syncPlayer(state) {
        const percent = state.duration > 0 ? Math.round(state.currentTime / state.duration * 100) : 0;
        const lines = player.parseLrc(state.track ? state.track.lyric : '');
        const lyricAt = player.currentLyricIndex(lines, state.currentTime);
        const loopMap = { all: '列表循环', one: '单曲循环', off: '不循环' };
        this.setData({
            mini: {
                hasTrack: state.hasTrack,
                title: state.track ? state.track.title : '',
                artist: state.track ? state.track.artist : '',
                cover: state.track ? state.track.cover : '',
                playing: state.playing,
                progressText: player.formatTime(state.currentTime) + ' / ' + player.formatTime(state.duration),
                percent: percent,
                error: state.error,
            },
            sheetVideo: state.video,
            sheetSrc: state.track ? state.track.src : '',
            lyricText: lyricAt >= 0 ? lines[lyricAt].text : (state.track ? state.track.album || '暂无歌词' : ''),
            lyricLines: lines,
            lyricIndex: lyricAt,
            rateText: state.rate.toFixed(2).replace(/0$/, '') + '×',
            loopText: loopMap[state.loop] || '列表循环',
        });
    },

    noop() {},

    handleOpenPlayer() {
        if (!this.data.mini.hasTrack) return;
        this.setData({ showPlayerSheet: true });
    },

    handleClosePlayer() {
        this.setData({ showPlayerSheet: false });
    },

    handlePlayerToggle() {
        player.toggle();
    },

    handlePlayerNext() {
        player.next(false);
    },

    handlePlayerPrev() {
        player.prev();
    },

    handleSeek(event) {
        const percent = Number(event.detail.value) || 0;
        const state = player.getState();
        player.seek(percent / 100 * state.duration);
    },

    handleRateTap() {
        player.cycleRate();
    },

    handleLoopTap() {
        player.cycleLoop();
    },

    handleVideoTimeUpdate(event) {
        const state = player.getState();
        player.reportVideoState(state.playing, event.detail.currentTime, event.detail.duration);
    },

    handleVideoPlay() {
        const state = player.getState();
        player.reportVideoState(true, state.currentTime, state.duration);
    },

    handleVideoPause() {
        const state = player.getState();
        player.reportVideoState(false, state.currentTime, state.duration);
    },

    onPullDownRefresh() {
        if (this.data.url) this.load(this.data.url);
        wx.stopPullDownRefresh();
    },

    onShareAppMessage() {
        return {
            title: this.data.pageTitle || this.data.host || '站点浏览',
            path: '/pages/browse/browse?url=' + encodeURIComponent(this.data.url),
        };
    },
});
