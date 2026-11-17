const { createLocalSiteStore, hostOf } = require('../../utils/site-store');
const { isDirectlyReachable } = require('../../utils/request-proxy');
const { isDebugSite } = require('../../utils/debug-site');
const checker = require('../../utils/site-checker');

const store = createLocalSiteStore({
    storageKey: 'sitedock-sites-v1',
});

Page({
    data: {
        viewState: 'loading',
        errorMessage: '加载失败，请稍后重试',
        items: [],
        groups: [],
        activeGroup: 'all',
        stats: {
            total: 0,
            online: 0,
            avgLatencyText: '—',
            onlineRateText: '—',
        },
        checking: false,
        checkProgressText: '',
    },

    onLoad() {
        this.loadItems();
    },

    onShow() {
        this.loadItems();
    },

    onPullDownRefresh() {
        if (this.data.items.length === 0) {
            wx.stopPullDownRefresh();
            return;
        }
        this.runCheck(true).then(() => {
            wx.stopPullDownRefresh();
        });
    },

    handleRetry() {
        this.loadItems();
    },

    loadItems() {
        this.setData({
            viewState: 'loading',
            errorMessage: '',
        });

        store.list().then(result => {
            if (result.error) {
                this.setData({
                    viewState: 'error',
                    errorMessage: result.error.message || '加载失败，请稍后重试',
                });
                return;
            }

            const source = Array.isArray(result.data) ? result.data : [];
            this.siteSource = source;
            const statusMap = checker.loadStatusMap();
            this.rebuildView(source, statusMap);
        });
    },

    rebuildView(source, statusMap) {
        const items = source.map(site => mapSiteView(site, statusMap[site.id]));
        const groups = buildGroups(source);
        const filtered = filterByGroup(items, this.data.activeGroup);
        this.setData({
            items: filtered,
            groups: groups,
            viewState: filtered.length === 0 && this.data.activeGroup !== 'all' ? 'ready' : (source.length === 0 ? 'empty' : 'ready'),
            stats: buildStats(items),
        });
    },

    handleGroupTap(event) {
        const key = event.currentTarget.dataset.key;
        if (!key || key === this.data.activeGroup) {
            return;
        }
        const statusMap = checker.loadStatusMap();
        const items = (this.siteSource || []).map(site => mapSiteView(site, statusMap[site.id]));
        this.setData({
            activeGroup: key,
            items: filterByGroup(items, key),
        });
        wx.vibrateShort({ type: 'light' });
    },

    openCreate() {
        wx.navigateTo({ url: '/pages/site-edit/site-edit' });
    },

    openBrowse(event) {
        const itemId = event.currentTarget.dataset.id;
        const site = this.findSiteById(itemId);
        if (!site) {
            return;
        }
        wx.navigateTo({ url: '/pages/browse/browse?id=' + encodeURIComponent(site.id) });
    },

    handleCardLongPress(event) {
        const itemId = event.currentTarget.dataset.id;
        const site = this.findSiteById(itemId);
        if (!site) {
            return;
        }
        wx.vibrateShort({ type: 'light' });
        const pinText = site.pinned ? '取消置顶' : '置顶';
        wx.showActionSheet({
            itemList: ['浏览', '编辑', pinText, '复制链接', '删除'],
            itemColor: '#1f2329',
            success: result => {
                if (result.tapIndex === 0) {
                    wx.navigateTo({ url: '/pages/browse/browse?id=' + encodeURIComponent(site.id) });
                } else if (result.tapIndex === 1) {
                    wx.navigateTo({ url: '/pages/site-edit/site-edit?id=' + encodeURIComponent(site.id) });
                } else if (result.tapIndex === 2) {
                    this.togglePinned(site);
                } else if (result.tapIndex === 3) {
                    wx.setClipboardData({ data: site.url });
                } else if (result.tapIndex === 4) {
                    this.confirmRemove(site);
                }
            },
        });
    },

    togglePinned(site) {
        store.update(site.id, { pinned: !site.pinned }).then(result => {
            if (result.error) {
                wx.showToast({ title: result.error.message || '操作失败', icon: 'none' });
                return;
            }
            wx.vibrateShort({ type: 'light' });
            this.loadItems();
        });
    },

    confirmRemove(site) {
        wx.showModal({
            title: '删除站点',
            content: '将删除「' + site.name + '」，不影响站点本身，是否继续？',
            confirmColor: '#d4380d',
            success: result => {
                if (!result.confirm) {
                    return;
                }
                store.remove(site.id).then(removeResult => {
                    if (removeResult.error) {
                        wx.showToast({ title: removeResult.error.message || '删除失败', icon: 'none' });
                        return;
                    }
                    wx.vibrateShort({ type: 'light' });
                    this.loadItems();
                });
            },
        });
    },

    handleCheckTap() {
        if (this.data.checking) {
            return;
        }
        this.runCheck(false);
    },

    runCheck(silent) {
        const sites = this.siteSource || [];
        if (sites.length === 0) {
            return Promise.resolve();
        }
        this.setData({
            checking: true,
            checkProgressText: '检测中 0/' + sites.length,
        });
        if (!silent) {
            wx.vibrateShort({ type: 'light' });
        }
        return checker.checkAll(sites, 0, (siteId, result, checked, total) => {
            this.setData({
                checkProgressText: '检测中 ' + checked + '/' + total,
            });
            if (this.data.activeGroup === 'all') {
                this.refreshSingleItem(siteId, result);
            }
        }).then(() => {
            const statusMap = checker.loadStatusMap();
            const items = (this.siteSource || []).map(site => mapSiteView(site, statusMap[site.id]));
            this.setData({
                checking: false,
                checkProgressText: '',
                items: filterByGroup(items, this.data.activeGroup),
                stats: buildStats(items),
            });
        });
    },

    refreshSingleItem(siteId, result) {
        const items = this.data.items || [];
        for (let i = 0; i < items.length; i += 1) {
            if (items[i].id === siteId) {
                const patch = statusPatch(result);
                this.setData({
                    ['items[' + i + '].statusClass']: patch.statusClass,
                    ['items[' + i + '].statusText']: patch.statusText,
                    ['items[' + i + '].latencyText']: patch.latencyText,
                    ['items[' + i + '].checkedText']: patch.checkedText,
                });
                return;
            }
        }
    },

    findSiteById(itemId) {
        const sites = this.siteSource || [];
        for (let i = 0; i < sites.length; i += 1) {
            if (sites[i].id === itemId) {
                return sites[i];
            }
        }
        return null;
    },
});

function mapSiteView(site, status) {
    const patch = statusPatch(status || null);
    const name = site.name || '';
    return {
        id: String(site.id),
        name: name,
        avatar: name.charAt(0) || '站',
        url: site.url,
        host: hostOf(site.url),
        group: site.group,
        groupText: site.group === '' ? '未分组' : site.group,
        pinned: site.pinned === true,
        note: site.note,
        restricted: !isDirectlyReachable(site.url),
        debug: isDebugSite(site.url),
        statusClass: patch.statusClass,
        statusText: patch.statusText,
        latencyText: patch.latencyText,
        checkedText: patch.checkedText,
    };
}

function statusPatch(status) {
    if (!status || !status.status) {
        return {
            statusClass: '',
            statusText: '未检测',
            latencyText: '—',
            checkedText: '',
        };
    }
    if (status.status === 'online') {
        return {
            statusClass: 'ok',
            statusText: '在线',
            latencyText: status.latency + 'ms',
            checkedText: '刚刚检测',
        };
    }
    if (status.status === 'warn') {
        return {
            statusClass: 'warn',
            statusText: '服务异常',
            latencyText: status.latency + 'ms',
            checkedText: formatTime(status.checkedAt),
        };
    }
    return {
        statusClass: 'bad',
        statusText: '离线',
        latencyText: '超时',
        checkedText: formatTime(status.checkedAt),
    };
}

function buildGroups(source) {
    const seen = {};
    const names = [];
    for (let i = 0; i < source.length; i += 1) {
        const group = source[i].group;
        if (group !== '' && !seen[group]) {
            seen[group] = true;
            names.push(group);
        }
    }
    const groups = [{ key: 'all', text: '全部' }];
    for (let i = 0; i < names.length; i += 1) {
        groups.push({ key: 'group:' + names[i], text: names[i] });
    }
    return groups;
}

function filterByGroup(items, activeGroup) {
    if (activeGroup === 'all' || !activeGroup) {
        return items;
    }
    const prefix = 'group:';
    if (activeGroup.indexOf(prefix) !== 0) {
        return items;
    }
    const name = activeGroup.slice(prefix.length);
    return items.filter(item => item.group === name);
}

function buildStats(items) {
    let online = 0;
    let latencySum = 0;
    let latencyCount = 0;
    for (let i = 0; i < items.length; i += 1) {
        if (items[i].statusClass === 'ok') {
            online += 1;
        }
        if (items[i].statusClass === 'ok' || items[i].statusClass === 'warn') {
            const value = Number(items[i].latencyText.replace('ms', ''));
            if (Number.isFinite(value) && value > 0) {
                latencySum += value;
                latencyCount += 1;
            }
        }
    }
    return {
        total: items.length,
        online: online,
        avgLatencyText: latencyCount === 0 ? '—' : Math.round(latencySum / latencyCount) + 'ms',
        onlineRateText: items.length === 0 ? '—' : Math.round((online / items.length) * 100) + '%',
    };
}

function formatTime(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
        return '';
    }
    const month = pad(date.getMonth() + 1);
    const day = pad(date.getDate());
    const hours = pad(date.getHours());
    const minutes = pad(date.getMinutes());
    return month + '-' + day + ' ' + hours + ':' + minutes;
}

function pad(value) {
    return value < 10 ? '0' + String(value) : String(value);
}
