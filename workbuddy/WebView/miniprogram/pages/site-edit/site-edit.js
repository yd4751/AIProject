const { createLocalSiteStore, normalizeUrl, isValidHttpUrl, hostOf } = require('../../utils/site-store');

const store = createLocalSiteStore({
    storageKey: 'sitedock-sites-v1',
});

Page({
    data: {
        mode: 'create',
        editingId: '',
        formName: '',
        formUrl: '',
        formGroup: '',
        formNote: '',
        formPinned: false,
        formError: '',
        submitting: false,
    },

    onLoad(options) {
        const id = options && options.id ? decodeURIComponent(options.id) : '';
        if (id === '') {
            this.setData({ mode: 'create' });
            return;
        }
        store.list().then(result => {
            if (result.error) {
                wx.showToast({ title: '站点信息读取失败', icon: 'none' });
                return;
            }
            const items = Array.isArray(result.data) ? result.data : [];
            let site = null;
            for (let i = 0; i < items.length; i += 1) {
                if (items[i].id === id) {
                    site = items[i];
                    break;
                }
            }
            if (!site) {
                wx.showToast({ title: '站点不存在，可能已被删除', icon: 'none' });
                setTimeout(() => wx.navigateBack(), 600);
                return;
            }
            this.setData({
                mode: 'edit',
                editingId: site.id,
                formName: site.name,
                formUrl: site.url,
                formGroup: site.group,
                formNote: site.note,
                formPinned: site.pinned,
            });
            wx.setNavigationBarTitle({ title: '编辑站点' });
        });
    },

    handleNameInput(event) {
        this.setData({ formName: event.detail.value, formError: '' });
    },

    handleUrlInput(event) {
        this.setData({ formUrl: event.detail.value, formError: '' });
    },

    handleGroupInput(event) {
        this.setData({ formGroup: event.detail.value, formError: '' });
    },

    handleNoteInput(event) {
        this.setData({ formNote: event.detail.value, formError: '' });
    },

    handlePinnedChange(event) {
        this.setData({ formPinned: event.detail.value });
    },

    handleSave() {
        if (this.data.submitting) {
            return;
        }

        const url = normalizeUrl(this.data.formUrl);
        if (!isValidHttpUrl(url)) {
            this.setData({ formError: '请填写有效的站点地址，例如 blog.example.com' });
            return;
        }

        const payload = {
            name: this.data.formName,
            url: url,
            group: this.data.formGroup,
            note: this.data.formNote,
            pinned: this.data.formPinned,
        };

        const request = this.data.mode === 'edit'
            ? store.update(this.data.editingId, payload)
            : store.create(payload);

        this.setData({ submitting: true, formError: '' });

        request.then(result => {
            if (result.error) {
                this.setData({
                    formError: result.error.message || '保存失败，请稍后重试',
                    submitting: false,
                });
                return;
            }
            wx.vibrateShort({ type: 'light' });
            wx.showToast({ title: '已保存', icon: 'success' });
            setTimeout(() => wx.navigateBack(), 400);
        });
    },

    handleDelete() {
        if (this.data.mode !== 'edit' || this.data.submitting) {
            return;
        }
        wx.showModal({
            title: '删除站点',
            content: '将删除「' + (this.data.formName || hostOf(normalizeUrl(this.data.formUrl))) + '」，是否继续？',
            confirmColor: '#d4380d',
            success: result => {
                if (!result.confirm) {
                    return;
                }
                store.remove(this.data.editingId).then(removeResult => {
                    if (removeResult.error) {
                        wx.showToast({ title: removeResult.error.message || '删除失败', icon: 'none' });
                        return;
                    }
                    wx.vibrateShort({ type: 'light' });
                    wx.showToast({ title: '已删除', icon: 'success' });
                    setTimeout(() => wx.navigateBack(), 400);
                });
            },
        });
    },
});
