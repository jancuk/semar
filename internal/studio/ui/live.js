'use strict';

// The live studio: a person watches an agent drive the device, takes over, acts by hand, and hands the
// device back. Like app.js it draws only what the server sends and puts every text in with textContent,
// because element texts and tool names are data. It keeps no secret: a text that a person types is sent
// once and the field is emptied, and the server never sends it back.
//
// It is listed before app.js in index.html only so that it can read the token from the address before
// app.js removes it. It starts once the page is parsed, so the recording studio is set up first, and a
// studio that has no live side (a 404 on /api/live/state) is left exactly as it was.
(function () {
  const m = /(?:^|[#&])token=([0-9a-f]+)/.exec(location.hash);
  const token = m ? m[1] : null;
  if (!token) return;

  const $ = (id) => document.getElementById(id);
  const POLL_SECONDS = 20;
  const SCREEN_GAP_MS = 500; // at most 2 pictures a second

  let state = null;
  let version = 0;
  let events = []; // the call feed, newest last
  let shotURL = '';
  let shotAt = 0;
  let shotTimer = 0;
  let shotBusy = false;
  let acting = false;
  let stopped = false;
  let drag = null;

  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'text') el.textContent = v;
      else if (k === 'class') el.className = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k in el && k !== 'list') el[k] = v;
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids) if (kid != null) el.append(kid);
    return el;
  }

  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }

  async function api(method, path, body) {
    const opts = { method, headers: { 'X-Semar-Token': token } };
    if (method !== 'GET') {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body === undefined ? {} : body);
    }
    let res;
    try {
      res = await fetch(path, opts);
    } catch (e) {
      return { ok: false, status: 0, data: { error: 'The studio did not answer.' } };
    }
    let data = {};
    try { data = await res.json(); } catch (e) { data = { error: 'The answer was not understood.' }; }
    return { ok: res.ok, status: res.status, data };
  }

  // --- the page ---

  const ui = {};

  function build() {
    document.title = 'Semar · Live studio';
    for (const sel of ['.topbar', 'main.work', '#banner']) {
      const el = document.querySelector(sel);
      if (el) el.hidden = true;
    }
    for (const d of document.querySelectorAll('dialog')) d.remove();

    ui.badge = h('span', { class: 'live-badge', role: 'status' });
    ui.pause = h('button', { type: 'button', class: 'btn primary', text: 'Pause / Take over', onclick: pause });
    ui.note = h('input', { type: 'text', class: 'live-note', maxLength: 500, placeholder: 'A note for the agent (what you did)', autocomplete: 'off', 'aria-label': 'Note for the agent' });
    ui.back = h('button', { type: 'button', class: 'btn primary', text: 'Hand back', onclick: handBack });
    ui.recordBox = h('input', { type: 'checkbox', id: 'live-record', onchange: () => setRecord(ui.recordBox.checked) });
    ui.record = h('label', { class: 'check live-record', title: 'The steps that you do by hand become steps of the recording. Off: they are not recorded, and the recording cannot be saved until you undo past them.' }, ui.recordBox, document.createTextNode(' Record my actions'));
    ui.banner = h('div', { class: 'banner', role: 'alert', hidden: true });
    ui.bar = h('header', { class: 'live-bar' },
      h('span', { class: 'brand' }, h('span', { class: 'brand-mark', 'aria-hidden': 'true', text: 'S' }), document.createTextNode('Semar live studio')),
      ui.badge, h('span', { class: 'spacer' }), ui.record, ui.pause, ui.note, ui.back);

    ui.img = h('img', { alt: 'The screen of the device', class: 'live-shot', draggable: false });
    ui.overlay = h('div', { class: 'live-overlay' });
    ui.hidden = h('div', { class: 'live-hidden', hidden: true, text: 'The screen shows a secret, so the picture is hidden.' });
    ui.empty = h('div', { class: 'live-hidden', hidden: true });
    ui.stage = h('div', { class: 'live-stage' }, ui.img, ui.overlay, ui.hidden, ui.empty);
    ui.text = h('input', { type: 'text', class: 'live-text', placeholder: 'Text to type in the focused field', autocomplete: 'off', spellcheck: false, 'aria-label': 'Text to type' });
    ui.secret = h('input', { type: 'text', class: 'live-secret', placeholder: 'or the name of a secret variable', autocomplete: 'off', spellcheck: false, 'aria-label': 'Name of a secret variable' });
    ui.replace = h('input', { type: 'checkbox' });
    ui.submit = h('input', { type: 'checkbox' });
    ui.keys = h('div', { class: 'live-keys' },
      ...['BACK', 'HOME', 'ENTER', 'TAB', 'DEL'].map((k) => h('button', { type: 'button', class: 'btn', text: k, onclick: () => act({ do: 'press', key: k }) })));
    ui.typeRow = h('div', { class: 'live-type' }, ui.text, ui.secret,
      h('label', { class: 'check' }, ui.replace, document.createTextNode(' replace')),
      h('label', { class: 'check' }, ui.submit, document.createTextNode(' Enter after')),
      h('button', { type: 'button', class: 'btn', text: 'Type', onclick: typeText }));
    ui.hint = h('p', { class: 'hint' });
    ui.mirror = h('section', { class: 'mirror live-mirror', 'aria-label': 'Device' }, ui.stage, ui.typeRow, ui.keys, ui.hint);

    ui.elements = h('div', { class: 'live-elements' });
    ui.steps = h('ol', { class: 'steps' });
    ui.stepsEmpty = h('p', { class: 'hint', text: 'No steps. This studio is not recording.' });
    ui.feed = h('ol', { class: 'live-feed' });
    ui.side = h('aside', { class: 'side live-side', 'aria-label': 'Details' },
      h('h2', { text: 'Elements' }), ui.elements,
      h('h2', { text: 'Timeline' }), ui.steps, ui.stepsEmpty,
      h('h2', { text: 'Agent calls' }), ui.feed);
    ui.root = h('div', { class: 'live-root' }, ui.bar, ui.banner, h('main', { class: 'work live-work' }, ui.mirror, ui.side));
    document.body.append(ui.root);

    ui.img.addEventListener('mousedown', (ev) => { if (canAct()) drag = { x: ev.clientX, y: ev.clientY }; });
    ui.img.addEventListener('mouseup', onMouseUp);
    ui.img.addEventListener('dblclick', (ev) => { const p = place(ev); if (p && canAct()) act({ do: 'doubleTap', x: p.x, y: p.y }); });
  }

  function say(text, bad) {
    if (!text) { ui.banner.hidden = true; return; }
    ui.banner.textContent = text;
    ui.banner.className = 'banner' + (bad ? ' bad' : '');
    ui.banner.hidden = false;
  }

  function canAct() {
    return !!state && state.owner === 'human' && !state.pending && !state.observe && !state.secret && !acting;
  }

  // place turns a point of the picture into a point of the screen.
  function place(ev) {
    if (!state || !state.width || !state.height) return null;
    const r = ui.img.getBoundingClientRect();
    if (!r.width || !r.height) return null;
    return {
      x: Math.round((ev.clientX - r.left) * state.width / r.width),
      y: Math.round((ev.clientY - r.top) * state.height / r.height),
    };
  }

  function onMouseUp(ev) {
    const start = drag;
    drag = null;
    if (!start || !canAct()) return;
    const a = place({ clientX: start.x, clientY: start.y });
    const b = place(ev);
    if (!a || !b) return;
    const moved = Math.hypot(ev.clientX - start.x, ev.clientY - start.y);
    if (moved > 24) act({ do: 'swipe', x: a.x, y: a.y, x2: b.x, y2: b.y });
    else if (ev.shiftKey) act({ do: 'longPress', x: b.x, y: b.y });
    else act({ do: 'tap', x: b.x, y: b.y });
  }

  // --- what the page does ---

  async function pause() {
    say('');
    ui.pause.disabled = true;
    say('Waiting for the agent to finish what it is doing…');
    const r = await api('POST', '/api/live/pause', { reason: 'paused from the studio' });
    say(r.ok ? '' : r.data.error, !r.ok);
    if (r.ok) apply(r.data);
  }

  async function handBack() {
    ui.back.disabled = true;
    const r = await api('POST', '/api/live/resume', { note: ui.note.value });
    if (r.ok) { ui.note.value = ''; say(''); apply(r.data); } else say(r.data.error, true);
  }

  async function setRecord(on) {
    const r = await api('POST', '/api/live/record', { on });
    if (r.ok) apply(r.data); else say(r.data.error, true);
  }

  async function act(body) {
    if (!canAct()) return;
    acting = true;
    ui.stage.classList.add('busy');
    const r = await api('POST', '/api/live/act', body);
    acting = false;
    ui.stage.classList.remove('busy');
    if (r.ok) { say(''); apply(r.data); } else { say(r.data.error, true); poll(0); }
  }

  function typeText() {
    const secret = ui.secret.value.trim();
    const body = { do: 'type', replace: ui.replace.checked, submit: ui.submit.checked };
    if (secret) body.secretEnv = secret; else body.text = ui.text.value;
    if (!secret && ui.text.value === '') { say('Write the text to type, or the name of a secret variable.', true); return; }
    // The text is sent once. The field is emptied at once, and the server never sends it back.
    ui.text.value = '';
    ui.secret.value = '';
    act(body);
  }

  // --- drawing ---

  function apply(s) {
    state = s;
    if (s.version > version) version = s.version;
    if (s.events && s.events.length) {
      for (const e of s.events) if (!events.some((x) => x.seq === e.seq)) events.push(e);
      events.sort((a, b) => a.seq - b.seq);
      if (events.length > 200) events = events.slice(events.length - 200);
    }
    render();
    wantShot();
  }

  function describe(s) {
    if (s.pending) return ['wait', 'Pausing: waiting for the agent’s call to finish…'];
    if (s.owner === 'human') {
      if (s.asked) return ['ask', 'The agent asks for help: ' + s.asked];
      return ['you', 'You are in control' + (s.reason ? ': ' + s.reason : '')];
    }
    return ['agent', 'Agent driving'];
  }

  function render() {
    const s = state;
    if (!s) return;
    const [kind, text] = describe(s);
    ui.badge.className = 'live-badge ' + kind;
    ui.badge.textContent = text;
    const human = s.owner === 'human' && !s.pending;
    ui.pause.hidden = s.owner === 'human';
    ui.pause.disabled = !!s.pending;
    ui.note.hidden = ui.back.hidden = !human;
    ui.back.disabled = false;
    ui.record.hidden = s.mode !== 'record';
    ui.recordBox.checked = !!s.recordHuman;
    ui.recordBox.disabled = !human;
    ui.typeRow.hidden = ui.keys.hidden = !human || !!s.observe;
    for (const b of ui.keys.querySelectorAll('button')) b.disabled = !canAct();
    ui.stage.classList.toggle('can-act', canAct());

    const parts = [];
    if (s.observe) parts.push('This server only observes (' + (s.observeReason || 'observe mode') + '): you can pause and look, not act.');
    else if (human) parts.push('Click an element or the picture to tap. Shift+click holds. Drag to swipe.');
    else parts.push('The agent is driving. Press Pause to take over.');
    if (s.mode === 'record' && s.gap) parts.push('Steps that you did by hand after step ' + s.gapAt + ' are not recorded, so the recording cannot be saved until you undo past them.');
    ui.hint.textContent = parts.join(' ');

    drawElements(s);
    drawSteps(s);
    drawFeed();
    ui.hidden.hidden = !s.secret;
    ui.empty.hidden = !s.noDevice;
    ui.empty.textContent = s.noDevice ? 'The agent has not opened the device yet. Press Pause to look at it.' : '';
  }

  function drawElements(s) {
    clear(ui.elements);
    clear(ui.overlay);
    if (s.busy && !(s.windows || []).length) {
      ui.elements.append(h('p', { class: 'hint', text: 'The agent is running a call…' }));
      return;
    }
    for (const w of s.windows || []) {
      const group = h('section', { class: 'live-window win-' + w.kind });
      group.append(h('h3', { text: w.kind + (w.package ? ' · ' + w.package : '') }));
      const list = h('ul', { class: 'live-list' });
      for (const e of w.elements) {
        const label = e.text || e.desc || e.hint || e.id || e.kind;
        const flags = [e.clickable ? 'clickable' : '', e.editable ? 'editable' : '', e.password ? 'password' : '', e.enabled ? '' : 'disabled'].filter(Boolean).join(', ');
        const row = h('li', { class: 'live-el' + (e.clickable ? ' clickable' : '') },
          h('button', { type: 'button', class: 'live-el-btn', disabled: !canAct(), onclick: () => act({ do: 'tap', x: Math.round((e.left + e.right) / 2), y: Math.round((e.top + e.bottom) / 2) }) },
            h('span', { class: 'live-el-kind', text: e.kind }), document.createTextNode(' '), h('span', { class: 'live-el-text', text: label }),
            flags ? h('span', { class: 'live-el-flags', text: ' [' + flags + ']' }) : null));
        list.append(row);
        if (s.width && s.height && e.right > e.left && e.bottom > e.top) {
          const box = h('div', { class: 'live-box win-' + w.kind + (e.clickable ? ' clickable' : '') });
          box.style.left = (100 * e.left / s.width) + '%';
          box.style.top = (100 * e.top / s.height) + '%';
          box.style.width = (100 * (e.right - e.left) / s.width) + '%';
          box.style.height = (100 * (e.bottom - e.top) / s.height) + '%';
          box.title = label;
          ui.overlay.append(box);
        }
      }
      group.append(list);
      ui.elements.append(group);
    }
    if (!ui.elements.firstChild) ui.elements.append(h('p', { class: 'hint', text: 'Nothing on the screen to act on.' }));
  }

  function drawSteps(s) {
    clear(ui.steps);
    const steps = s.steps || [];
    ui.stepsEmpty.hidden = s.mode === 'record' || steps.length > 0;
    for (const st of steps) {
      ui.steps.append(h('li', { class: 'step' + (st.pending ? ' pending' : '') + (st.who === 'human' ? ' human' : '') },
        h('span', { class: 'step-n', text: String(st.n) }), document.createTextNode(' '),
        h('span', { class: 'step-text', text: st.text }),
        st.who === 'human' ? h('span', { class: 'step-who', text: ' (you)' }) : null));
    }
    if (s.mode === 'record' && !steps.length) ui.steps.append(h('li', { class: 'hint', text: 'No steps yet.' }));
  }

  function drawFeed() {
    clear(ui.feed);
    for (const e of events.slice(-60).reverse()) {
      const when = new Date(e.at);
      const hhmmss = isNaN(when) ? '' : when.toLocaleTimeString();
      ui.feed.append(h('li', { class: 'feed-' + e.who + (e.ok ? '' : ' bad') },
        h('time', { text: hhmmss }), document.createTextNode(' '),
        h('b', { text: e.who === 'human' ? 'you' : 'agent' }), document.createTextNode(' '),
        h('span', { class: 'feed-tool', text: e.tool }),
        e.summary ? h('span', { class: 'feed-summary', text: ' ' + e.summary }) : null,
        e.ms ? h('span', { class: 'feed-ms', text: ' ' + e.ms + ' ms' }) : null,
        e.ok ? null : h('span', { class: 'feed-fail', text: ' failed' })));
    }
  }

  // --- the picture ---

  // wantShot asks for a new picture, at most twice a second, when the version changed.
  function wantShot() {
    if (!state || state.noDevice || state.secret) return;
    if (shotTimer) return;
    const wait = Math.max(0, SCREEN_GAP_MS - (Date.now() - shotAt));
    shotTimer = setTimeout(() => { shotTimer = 0; fetchShot(); }, wait);
  }

  async function fetchShot() {
    if (shotBusy || stopped) return;
    shotBusy = true;
    shotAt = Date.now();
    try {
      const res = await fetch('/api/live/screen', { headers: { 'X-Semar-Token': token } });
      if (res.ok) {
        const blob = await res.blob();
        // The page may load a picture only from itself or a data address.
        const url = await new Promise((resolve, reject) => {
          const fr = new FileReader();
          fr.onload = () => resolve(fr.result);
          fr.onerror = () => reject(fr.error);
          fr.readAsDataURL(blob);
        });
        shotURL = url;
        ui.img.src = shotURL;
        ui.hidden.hidden = true;
      } else if (res.status === 403) {
        ui.hidden.hidden = false;
      }
    } catch (e) { /* the next change asks again */ }
    shotBusy = false;
  }

  // --- the long poll, which is also the heartbeat ---

  async function poll(wait) {
    const r = await api('GET', '/api/live/state?since=' + version + '&wait=' + wait);
    if (!r.ok) {
      if (r.status === 0 || r.status >= 500) say('The studio did not answer. Trying again…', true);
      else say(r.data.error, true);
      return false;
    }
    say('');
    apply(r.data);
    return true;
  }

  async function loop() {
    while (!stopped) {
      const ok = await poll(POLL_SECONDS);
      if (!ok) await new Promise((r) => setTimeout(r, 2000));
    }
  }

  async function start() {
    // A studio with no live side answers 404, and the page stays as it is.
    const probe = await api('GET', '/api/live/state?wait=0');
    if (!probe.ok) return;
    build();
    apply(probe.data);
    loop();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
