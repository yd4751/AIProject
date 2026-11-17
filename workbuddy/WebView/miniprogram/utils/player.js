// 全局播放器：跨页面共享播放状态，mini player 与全屏播放页读同一份数据。
// 优先用后台音频管理器（锁屏/后台可播，最像原生 APP），环境不支持时降级到普通音频实例。
const RATES = [0.75, 1, 1.25, 1.5, 2];

let audio = null;
let audioKind = 'none';
let bound = false;
const listeners = [];

const state = {
    playlist: [],
    index: -1,
    track: null,
    playing: false,
    video: false,
    currentTime: 0,
    duration: 0,
    rate: 1,
    loop: 'all',
    error: '',
};

function snapshot() {
    return {
        playlist: state.playlist,
        index: state.index,
        track: state.track,
        playing: state.playing,
        video: state.video,
        currentTime: state.currentTime,
        duration: state.duration,
        rate: state.rate,
        loop: state.loop,
        error: state.error,
        hasTrack: !!state.track,
    };
}

function notify() {
    const data = snapshot();
    for (let i = 0; i < listeners.length; i += 1) {
        try {
            listeners[i](data);
        } catch (error) {
            // 单个订阅者异常不影响播放
        }
    }
}

function subscribe(fn) {
    if (typeof fn !== 'function') return function () {};
    listeners.push(fn);
    fn(snapshot());
    return function () {
        const at = listeners.indexOf(fn);
        if (at >= 0) listeners.splice(at, 1);
    };
}

function ensureAudio() {
    if (audio) return audio;
    if (typeof wx === 'undefined') return null;
    try {
        if (typeof wx.getBackgroundAudioManager === 'function') {
            audio = wx.getBackgroundAudioManager();
            audioKind = 'background';
        }
    } catch (error) {
        audio = null;
    }
    if (!audio) {
        try {
            if (typeof wx.createInnerAudioContext === 'function') {
                audio = wx.createInnerAudioContext();
                audioKind = 'inner';
            }
        } catch (error) {
            audio = null;
        }
    }
    if (!audio) return null;

    if (!bound) {
        bound = true;
        try {
            audio.onPlay(function () { state.playing = true; state.error = ''; notify(); });
            audio.onPause(function () { state.playing = false; notify(); });
            audio.onStop(function () { state.playing = false; notify(); });
            audio.onTimeUpdate(function () {
                state.currentTime = Number(audio.currentTime) || 0;
                state.duration = Number(audio.duration) || 0;
                notify();
            });
            audio.onEnded(function () { next(true); });
            audio.onError(function (error) {
                state.playing = false;
                state.error = (error && error.errMsg) ? error.errMsg : '播放失败';
                notify();
            });
        } catch (error) {
            // 预览环境可能只实现部分事件，忽略即可
        }
    }
    return audio;
}

function load(index) {
    const track = state.playlist[index];
    if (!track) return;
    state.index = index;
    state.track = track;
    state.video = !!track.video;
    state.currentTime = 0;
    state.duration = 0;
    state.error = '';
    state.playing = false;

    if (track.video) {
        // 视频由播放页的原生 video 组件负责，这里只同步列表状态
        if (audio) {
            try { audio.stop(); } catch (error) { /* noop */ }
        }
        notify();
        return;
    }

    const instance = ensureAudio();
    if (!instance) {
        state.error = '当前环境不支持音频播放';
        notify();
        return;
    }
    try {
        if (audioKind === 'background') {
            instance.title = track.title || '未知曲目';
            instance.singer = track.artist || '';
            instance.albumName = track.album || '';
            instance.coverImgUrl = track.cover || '';
            instance.epname = '站点坞';
        }
        instance.src = track.src;
        instance.playbackRate = state.rate;
        instance.play();
        state.playing = true;
    } catch (error) {
        state.error = error && error.message ? error.message : '无法开始播放';
    }
    notify();
}

function play(playlist, index) {
    const list = Array.isArray(playlist) ? playlist : [];
    state.playlist = list;
    const at = typeof index === 'number' ? index : 0;
    load(Math.max(0, Math.min(at, list.length - 1)));
}

function playOne(track) {
    if (!track) return;
    const at = state.playlist.findIndex(item => item.id === track.id);
    if (at >= 0) {
        load(at);
        return;
    }
    state.playlist = [track].concat(state.playlist);
    load(0);
}

function toggle() {
    if (!state.track) return;
    if (state.video) {
        // video 组件自身控制，页面会通过 reportVideoState 同步
        return;
    }
    const instance = ensureAudio();
    if (!instance) return;
    try {
        if (state.playing) instance.pause();
        else instance.play();
    } catch (error) {
        state.error = error && error.message ? error.message : '播放控制失败';
        notify();
    }
}

function next(auto) {
    if (state.playlist.length === 0) return;
    let at = state.index + 1;
    if (at >= state.playlist.length) {
        if (auto && state.loop === 'one') {
            load(state.index);
            return;
        }
        if (auto && state.loop === 'off') {
            state.playing = false;
            notify();
            return;
        }
        at = 0;
    }
    load(at);
}

function prev() {
    if (state.playlist.length === 0) return;
    let at = state.index - 1;
    if (at < 0) at = state.playlist.length - 1;
    load(at);
}

function seek(seconds) {
    if (!state.track) return;
    if (state.video) return;
    const instance = ensureAudio();
    if (!instance) return;
    try {
        instance.seek(Math.max(0, Number(seconds) || 0));
        state.currentTime = Math.max(0, Number(seconds) || 0);
        notify();
    } catch (error) { /* noop */ }
}

function cycleRate() {
    const at = RATES.indexOf(state.rate);
    state.rate = RATES[(at + 1 + RATES.length) % RATES.length];
    if (audio && !state.video) {
        try { audio.playbackRate = state.rate; } catch (error) { /* noop */ }
    }
    notify();
    return state.rate;
}

function cycleLoop() {
    const modes = ['all', 'one', 'off'];
    state.loop = modes[(modes.indexOf(state.loop) + 1) % modes.length];
    notify();
    return state.loop;
}

function stop() {
    if (audio) {
        try { audio.stop(); } catch (error) { /* noop */ }
    }
    state.playing = false;
    state.track = null;
    state.video = false;
    state.currentTime = 0;
    notify();
}

// 供 video 组件回传播放状态与时间
function reportVideoState(playing, currentTime, duration) {
    if (!state.video) return;
    state.playing = !!playing;
    state.currentTime = Number(currentTime) || 0;
    state.duration = Number(duration) || 0;
    notify();
}

function formatTime(seconds) {
    const value = Math.max(0, Math.floor(Number(seconds) || 0));
    const min = Math.floor(value / 60);
    const sec = value % 60;
    return (min < 10 ? '0' : '') + min + ':' + (sec < 10 ? '0' : '') + sec;
}

function parseLrc(text) {
    if (!text) return [];
    const lines = [];
    const rows = String(text).split(/\r?\n/);
    for (let i = 0; i < rows.length; i += 1) {
        const matches = rows[i].match(/\[(\d{1,2}):(\d{1,2}(?:\.\d+)?)\]/g);
        if (!matches) continue;
        const content = rows[i].replace(/\[[^\]]+\]/g, '').trim();
        if (!content) continue;
        for (let j = 0; j < matches.length; j += 1) {
            const parts = /\[(\d{1,2}):(\d{1,2}(?:\.\d+)?)\]/.exec(matches[j]);
            if (!parts) continue;
            lines.push({ time: Number(parts[1]) * 60 + Number(parts[2]), text: content });
        }
    }
    lines.sort((a, b) => a.time - b.time);
    return lines;
}

function currentLyricIndex(lines, currentTime) {
    let at = -1;
    for (let i = 0; i < lines.length; i += 1) {
        if (lines[i].time <= currentTime) at = i;
        else break;
    }
    return at;
}

module.exports = {
    subscribe,
    getState: snapshot,
    play,
    playOne,
    toggle,
    next,
    prev,
    seek,
    cycleRate,
    cycleLoop,
    stop,
    reportVideoState,
    formatTime,
    parseLrc,
    currentLyricIndex,
    RATES,
};
