// 站点本地数据层：与 list-crud 骨架的 createLocalItemStore 保持同一调用契约
// （list / create / update / remove，均返回 { data, error }，不 reject）。
// 数据上云时，把页面里的 store 替换为 cloud-service 的 createTableAccess 返回对象即可。

const NOT_FOUND_OR_FORBIDDEN_ERROR = {
    code: 'NOT_FOUND_OR_FORBIDDEN',
    message: '没有保存成功，这条站点可能已被删除',
};

const INVALID_ID_ERROR = {
    code: 'INVALID_ID',
    message: '缺少站点 id',
};

const INVALID_PAYLOAD_ERROR = {
    code: 'INVALID_PAYLOAD',
    message: '没有可提交的字段',
};

let idCounter = 0;

function createLocalSiteStore(options) {
    const config = options || {};
    if (typeof config.storageKey !== 'string' || config.storageKey.trim() === '') {
        throw new TypeError('storageKey 不能为空');
    }
    const storageKey = config.storageKey;

    return {
        list() {
            return safely(() => {
                const items = readItems(storageKey);
                return { data: items, error: null };
            });
        },

        create(payload) {
            return safely(() => {
                const writable = toCreateRow(payload);
                if (!hasWritableFields(writable)) {
                    return { data: null, error: INVALID_PAYLOAD_ERROR };
                }

                const now = Date.now();
                const item = {
                    id: createItemId(now),
                    name: writable.name,
                    url: writable.url,
                    group: writable.group,
                    note: writable.note,
                    pinned: writable.pinned,
                    createdAt: now,
                    updatedAt: now,
                };

                const items = readItems(storageKey);
                items.unshift(item);
                writeItems(storageKey, items);
                return { data: item, error: null };
            });
        },

        update(id, patch) {
            if (isInvalidId(id)) {
                return Promise.resolve({ data: null, error: INVALID_ID_ERROR });
            }
            return safely(() => {
                const writable = toUpdateRow(patch);
                if (!hasWritableFields(writable)) {
                    return { data: null, error: INVALID_PAYLOAD_ERROR };
                }

                const items = readItems(storageKey);
                const index = findIndexById(items, String(id));
                if (index === -1) {
                    return { data: null, error: NOT_FOUND_OR_FORBIDDEN_ERROR };
                }

                const current = items[index];
                const next = {
                    id: current.id,
                    name: writable.name === undefined ? current.name : writable.name,
                    url: writable.url === undefined ? current.url : writable.url,
                    group: writable.group === undefined ? current.group : writable.group,
                    note: writable.note === undefined ? current.note : writable.note,
                    pinned: writable.pinned === undefined ? current.pinned : writable.pinned,
                    createdAt: current.createdAt,
                    updatedAt: Date.now(),
                };

                items[index] = next;
                writeItems(storageKey, items);
                return { data: next, error: null };
            });
        },

        remove(id) {
            if (isInvalidId(id)) {
                return Promise.resolve({ data: null, error: INVALID_ID_ERROR });
            }
            return safely(() => {
                const itemId = String(id);
                const items = readItems(storageKey);
                const filtered = items.filter(item => item.id !== itemId);
                const removed = items.length - filtered.length;
                if (removed === 0) {
                    return { data: null, error: NOT_FOUND_OR_FORBIDDEN_ERROR };
                }
                writeItems(storageKey, filtered);
                return { data: { removed }, error: null };
            });
        },
    };
}

function normalizeUrl(value) {
    if (typeof value !== 'string') {
        return '';
    }
    let text = value.trim();
    if (text === '') {
        return '';
    }
    if (!/^https?:\/\//i.test(text)) {
        text = 'https://' + text.replace(/^\/+/, '');
    }
    return text;
}

function isValidHttpUrl(value) {
    if (typeof value !== 'string') {
        return false;
    }
    return /^https?:\/\/[a-z0-9.-]+\.[a-z]{2,}(:\d+)?(\/|$|\?|#)/i.test(value)
        || /^https?:\/\/localhost(:\d+)?(\/|$|\?|#)/i.test(value)
        || /^https?:\/\/\d{1,3}(\.\d{1,3}){3}(:\d+)?(\/|$|\?|#)/.test(value);
}

function safely(executor) {
    return Promise.resolve()
        .then(executor)
        .catch(error => ({ data: null, error: toClientException(error) }));
}

function toClientException(error) {
    const message = error && typeof error === 'object' && typeof error.message === 'string'
        ? error.message
        : '';
    return { code: 'CLIENT_EXCEPTION', message };
}

function hasWritableFields(payload) {
    return payload !== null && typeof payload === 'object' && !Array.isArray(payload) && Object.keys(payload).length > 0;
}

function toCreateRow(payload) {
    const source = isPlainObject(payload) ? payload : {};
    const row = {};
    const url = normalizeUrl(source.url);
    if (!isValidHttpUrl(url)) {
        return {};
    }
    row.url = url;
    row.name = typeof source.name === 'string' && source.name.trim() !== '' ? source.name.trim() : '';
    row.group = typeof source.group === 'string' ? source.group.trim() : '';
    row.note = typeof source.note === 'string' ? source.note.trim() : '';
    row.pinned = source.pinned === true;
    if (row.name === '') {
        row.name = hostOf(url);
    }
    return row;
}

function toUpdateRow(patch) {
    const source = isPlainObject(patch) ? patch : {};
    const row = {};
    if (Object.prototype.hasOwnProperty.call(source, 'url')) {
        const url = normalizeUrl(source.url);
        if (!isValidHttpUrl(url)) {
            return {};
        }
        row.url = url;
    }
    if (Object.prototype.hasOwnProperty.call(source, 'name') && typeof source.name === 'string') {
        row.name = source.name.trim();
    }
    if (Object.prototype.hasOwnProperty.call(source, 'group') && typeof source.group === 'string') {
        row.group = source.group.trim();
    }
    if (Object.prototype.hasOwnProperty.call(source, 'note') && typeof source.note === 'string') {
        row.note = source.note.trim();
    }
    if (Object.prototype.hasOwnProperty.call(source, 'pinned')) {
        row.pinned = source.pinned === true;
    }
    return row;
}

function hostOf(url) {
    const match = /^https?:\/\/([^/:?#]+)/i.exec(url);
    return match ? match[1] : url;
}

function isPlainObject(value) {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function readItems(storageKey) {
    const value = wx.getStorageSync(storageKey);
    if (!Array.isArray(value)) {
        return [];
    }
    return value
        .filter(isStoredItem)
        .sort(compareSites)
        .map(item => ({
            id: String(item.id),
            name: item.name,
            url: item.url,
            group: item.group,
            note: item.note,
            pinned: item.pinned === true,
            createdAt: item.createdAt,
            updatedAt: item.updatedAt,
        }));
}

function compareSites(left, right) {
    if (left.pinned !== right.pinned) {
        return left.pinned ? -1 : 1;
    }
    return right.updatedAt - left.updatedAt;
}

function writeItems(storageKey, items) {
    wx.setStorageSync(storageKey, items);
}

function findIndexById(items, itemId) {
    for (let i = 0; i < items.length; i += 1) {
        if (items[i].id === itemId) {
            return i;
        }
    }
    return -1;
}

function createItemId(timestamp) {
    idCounter = (idCounter + 1) % 1000000;
    return String(timestamp) + '-' + String(idCounter);
}

function isInvalidId(id) {
    if (typeof id === 'string') {
        return id.trim() === '';
    }
    if (typeof id === 'number') {
        return !Number.isFinite(id);
    }
    return true;
}

function isStoredItem(item) {
    return item !== null
        && typeof item === 'object'
        && (typeof item.id === 'string' || typeof item.id === 'number')
        && typeof item.name === 'string'
        && typeof item.url === 'string'
        && typeof item.group === 'string'
        && typeof item.note === 'string'
        && typeof item.pinned === 'boolean'
        && typeof item.createdAt === 'number'
        && typeof item.updatedAt === 'number';
}

module.exports = {
    createLocalSiteStore,
    normalizeUrl,
    isValidHttpUrl,
    hostOf,
};
