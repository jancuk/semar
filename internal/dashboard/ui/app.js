'use strict';

// The page draws everything from the state that the server sends, and puts every text in
// with textContent, because provider IDs, model names, and sign-in output are data.
(function () {
  const TOKEN_KEY = 'semar-dashboard-token';
  const DAYS_KEY = 'semar-dashboard-days';
  const OTHER = '__other__'; // no model ID can start with an underscore
  const CONFIRM_MS = 6000;
  const SAVED_MS = 2000;

  const LEVEL_NAMES = {
    none: 'None', minimal: 'Minimal', low: 'Low', medium: 'Medium',
    high: 'High', xhigh: 'Extra high', max: 'Max', ultra: 'Ultra',
  };
  const LEVEL_ORDER = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra'];

  const PERIODS = [
    { days: 1, label: 'Today' },
    { days: 7, label: '7 days' },
    { days: 30, label: '30 days' },
  ];

  const ADD_KINDS = [
    { key: 'claude', name: 'Claude Code', help: 'Uses your Claude Code sign-in', kind: 'claude', id: 'claude' },
    { key: 'codex', name: 'Codex', help: 'Uses your Codex sign-in', kind: 'codex', id: 'codex' },
    { key: 'anthropic', name: 'Anthropic API', help: 'Needs an API key', kind: 'anthropic', id: 'anthropic', env: 'ANTHROPIC_API_KEY' },
    { key: 'openai', name: 'OpenAI API', help: 'Needs an API key', kind: 'openai', id: 'openai', env: 'OPENAI_API_KEY' },
    { key: 'deepseek', name: 'DeepSeek', help: 'Needs an API key', kind: 'openai', id: 'deepseek', baseUrl: 'https://api.deepseek.com', env: 'DEEPSEEK_API_KEY' },
    { key: 'other', name: 'Another server', help: 'Any server with the OpenAI chat format', kind: 'openai', id: 'server', address: true, env: 'ACME_API_KEY' },
  ];

  const WINDOW_NAMES = { five_hour: '5-hour window', seven_day: '7-day window' };

  let token = null;
  let state = null;
  let usage = null;
  let usageDays = 30;
  let rendering = false;
  let uid = 0;

  const views = new Map(); // provider ID to what the person is doing on its card
  const kept = new Map(); // text fields kept across drawings, so typed text is never lost
  const nodes = {};

  // --- small helpers ---

  const PROPS = { value: 1, checked: 1, disabled: 1, open: 1, hidden: 1, selected: 1 };

  function h(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const k of kids.flat(Infinity)) {
      if (k === null || k === undefined || k === false) continue;
      e.append(k instanceof Node ? k : String(k));
    }
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === null || v === undefined || v === false) continue;
      if (k === 'class') e.className = v;
      else if (k === 'text') e.textContent = v;
      else if (k === 'on') for (const [ev, fn] of Object.entries(v)) e.addEventListener(ev, fn);
      else if (PROPS[k]) e[k] = v;
      else e.setAttribute(k, v === true ? '' : String(v));
    }
    return e;
  }

  const code = (text) => h('code', { text });

  function nextId(prefix) {
    uid += 1;
    return prefix + '-' + uid;
  }

  function enc(id) {
    return encodeURIComponent(id);
  }

  function cap(s) {
    return s ? s.charAt(0).toUpperCase() + s.slice(1) : '';
  }

  function quoteArg(a) {
    return /^[A-Za-z0-9_./:=@%+-]+$/.test(a) ? a : "'" + a.replace(/'/g, "'\\''") + "'";
  }

  function readOnly() {
    return Boolean(state && state.fileError);
  }

  function reducedMotion() {
    return window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }

  // --- formats ---

  const numberFormat = new Intl.NumberFormat();
  const compactFormat = new Intl.NumberFormat(undefined, { notation: 'compact', maximumSignificantDigits: 3 });
  const percentFormat = new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 0 });
  const timeOnly = new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' });
  const dayTime = new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' });
  const dateTime = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });

  function short(n) {
    n = n || 0;
    return Math.abs(n) < 1000 ? numberFormat.format(n) : compactFormat.format(n);
  }

  function cost(v) {
    if (!v) return '—';
    return new Intl.NumberFormat(undefined, {
      style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: v < 0.01 ? 4 : 2,
    }).format(v);
  }

  function toDate(s) {
    const d = new Date(s);
    return isNaN(d.getTime()) || d.getFullYear() < 2000 ? null : d;
  }

  function sameDay(a, b) {
    return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  }

  function when(s) {
    const d = toDate(s);
    if (!d) return '—';
    return sameDay(d, new Date()) ? 'Today ' + timeOnly.format(d) : dateTime.format(d);
  }

  function resetsAt(s) {
    const d = toDate(s);
    if (!d) return '';
    const days = (d.getTime() - Date.now()) / 86400000;
    if (sameDay(d, new Date())) return 'today ' + timeOnly.format(d);
    return days < 6 ? dayTime.format(d) : dateTime.format(d);
  }

  function duration(ms) {
    if (!ms) return '—';
    const s = Math.round(ms / 1000);
    if (s < 60) return s + ' s';
    const m = Math.floor(s / 60);
    return m < 60 ? m + ' min ' + (s % 60) + ' s' : Math.floor(m / 60) + ' h ' + (m % 60) + ' min';
  }

  function levelName(l) {
    return LEVEL_NAMES[l] || cap(l);
  }

  function sortLevels(levels) {
    const rank = (l) => {
      const i = LEVEL_ORDER.indexOf(l);
      return i < 0 ? LEVEL_ORDER.length : i;
    };
    return levels.slice().sort((a, b) => rank(a) - rank(b));
  }

  // --- products and their status ---

  const isAccountKind = (kind) => kind === 'claude' || kind === 'codex';
  const isAPIKind = (kind) => kind === 'anthropic' || kind === 'openai';

  function hostOf(url) {
    try {
      return new URL(url).hostname.toLowerCase();
    } catch (e) {
      return '';
    }
  }

  function productName(p) {
    switch (p.kind) {
      case 'claude': return 'Claude Code';
      case 'codex': return 'Codex';
      case 'anthropic': return 'Anthropic API';
      case 'openai': {
        const host = hostOf(p.baseUrl || '');
        if (!host || host === 'api.openai.com') return 'OpenAI API';
        if (host === 'api.deepseek.com') return 'DeepSeek';
        return host;
      }
      default: return p.id || p.kind || 'Provider';
    }
  }

  function nameOfId(id, kind) {
    const p = state && state.providers.find((x) => x.id === id);
    if (p) return showsId(p) ? productName(p) + ' (' + p.id + ')' : productName(p);
    return isAccountKind(kind) || kind === 'anthropic' ? productName({ id, kind }) : id;
  }

  function signedInText(p) {
    const a = p.account || {};
    const method = (a.method || '').toLowerCase();
    if (method === 'api key') return { long: 'Signed in with an API key.', short: 'Signed in with an API key' };
    if (method === 'access token') return { long: 'Signed in with an access token.', short: 'Signed in with an access token' };
    if (method === 'subscription' && p.kind === 'codex') return { long: 'Signed in with ChatGPT.', short: 'Signed in with ChatGPT' };
    if (method === 'subscription') {
      const plan = a.plan ? ', plan ' + cap(a.plan) : '';
      return { long: 'Signed in with your Claude subscription' + plan + '.', short: a.plan ? 'Signed in, plan ' + cap(a.plan) : 'Signed in with your subscription' };
    }
    return { long: 'Signed in.', short: 'Signed in' };
  }

  function keyRef(p) {
    const ref = p.apiKey || '';
    if (ref.startsWith('env:')) return { type: 'env', name: ref.slice(4) };
    if (ref.startsWith('keychain:')) return { type: 'keychain', name: ref.slice(9) };
    return { type: ref ? 'other' : 'none', name: '' };
  }

  function keyWhere(p) {
    const r = keyRef(p);
    if (r.type === 'env') return 'Read from the variable ' + r.name + '.';
    if (r.type === 'keychain') return 'Stored in your keychain.';
    if (r.type === 'none') return 'This server needs no key.';
    return 'The key is set in the settings file.';
  }

  function keyMissing(p) {
    const r = keyRef(p);
    if (r.type === 'env') return 'The variable ' + r.name + ' is not set.';
    if (r.type === 'keychain') return 'The key is not in your keychain.';
    return 'The API key is missing.';
  }

  // status says what the summary row and the hero show, and whether the provider can plan.
  function status(p) {
    const name = productName(p);
    if (isAccountKind(p.kind)) {
      if (p.health && !p.health.installed) {
        return { pill: 'Not installed', tone: 'bad', meta: 'Not installed', blocked: true, why: name + ' is not installed on this computer.', focus: 'check' };
      }
      if (p.account && p.account.signedIn) {
        const s = signedInText(p);
        return { pill: 'Ready', tone: 'ok', meta: s.short, sentence: s.long };
      }
      if (p.account) {
        return { pill: 'Sign in needed', tone: 'warn', meta: 'Not signed in', blocked: true, why: name + ' is not signed in.', focus: 'signin' };
      }
      return { pill: 'Not checked', tone: '', meta: 'The sign-in could not be checked', sentence: p.error || 'The sign-in could not be checked.' };
    }
    if (!p.keyReady) {
      return { pill: 'Key needed', tone: 'warn', meta: 'Key missing', blocked: true, why: keyMissing(p), focus: 'key' };
    }
    if (!p.model) {
      return { pill: 'Model needed', tone: 'warn', meta: 'Key ready', blocked: true, why: 'Choose a model for ' + name + '.', focus: 'model' };
    }
    const r = keyRef(p);
    const sentence = r.type === 'env' ? 'The key is read from the variable ' + r.name + '.'
      : r.type === 'none' ? 'This server needs no key.' : 'The key is stored in your keychain.';
    return { pill: 'Ready', tone: 'ok', meta: 'Key ready', sentence };
  }

  // --- views ---

  function view(id) {
    let v = views.get(id);
    if (!v) {
      v = {
        open: undefined, models: null, other: false, replacing: false, envMode: false,
        login: null, confirm: false, confirmTimer: 0, pending: {}, errors: {}, saved: null,
        reasoningNote: '', checkNote: '', denyOpen: undefined, queue: Promise.resolve(),
      };
      views.set(id, v);
    }
    return v;
  }

  function provider(id) {
    return state ? state.providers.find((p) => p.id === id) : null;
  }

  function active() {
    return state ? provider(state.default) : null;
  }

  function pendingOr(v, key, saved) {
    return Object.prototype.hasOwnProperty.call(v.pending, key) ? v.pending[key] : saved;
  }

  // modelInfo names the models a card can show and what is known about one model.
  function modelInfo(id, model) {
    const v = view(id);
    const data = v.models && v.models.data;
    const listed = data && Array.isArray(data.models) ? data.models : [];
    let entry = model ? listed.find((m) => m.id === model) : null;
    let levels = [];
    let def = '';
    if (entry) {
      levels = Array.isArray(entry.reasoning) ? entry.reasoning : [];
      def = entry.defaultReasoning || '';
    } else if (!model) {
      const last = data && data.lastUsed && data.lastUsed.model;
      const lastEntry = last ? listed.find((m) => m.id === last) : null;
      if (lastEntry) {
        levels = Array.isArray(lastEntry.reasoning) ? lastEntry.reasoning : [];
        def = lastEntry.defaultReasoning || '';
      } else {
        const all = new Set();
        listed.forEach((m) => (m.reasoning || []).forEach((l) => all.add(l)));
        levels = sortLevels([...all]);
      }
    }
    return { listed, entry, levels, def, known: Boolean(entry && Array.isArray(entry.reasoning)), data };
  }

  function modelLabel(id, model) {
    const info = modelInfo(id, model);
    return info.entry && info.entry.label ? info.entry.label : model;
  }

  // --- the token and the API ---

  function readToken() {
    const m = /(?:^#|&)token=([^&]*)/.exec(location.hash);
    if (m) {
      const t = decodeURIComponent(m[1]);
      try {
        sessionStorage.setItem(TOKEN_KEY, t);
      } catch (e) {
        // Without storage the page still works until it is reloaded.
      }
      history.replaceState(null, '', location.pathname + location.search);
      return t || null;
    }
    try {
      return sessionStorage.getItem(TOKEN_KEY);
    } catch (e) {
      return null;
    }
  }

  function forgetToken() {
    token = null;
    try {
      sessionStorage.removeItem(TOKEN_KEY);
    } catch (e) {
      // Nothing was kept.
    }
  }

  class TokenError extends Error {}

  async function api(method, path, body, headers) {
    if (!token) throw new TokenError('This page has no token.');
    const opts = { method, headers: Object.assign({ 'X-Semar-Token': token }, headers || {}), cache: 'no-store' };
    if (method !== 'GET') {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body || {});
    }
    let res;
    try {
      res = await fetch(path, opts);
    } catch (e) {
      throw new Error('The dashboard does not answer. Check that semar dashboard is still running.');
    }
    let data = null;
    try {
      data = await res.json();
    } catch (e) {
      data = null;
    }
    if (res.status === 403 && data && /token/i.test(data.error || '')) {
      forgetToken();
      showStart();
      throw new TokenError(data.error);
    }
    if (!res.ok) throw new Error((data && data.error) || 'The dashboard answered with status ' + res.status + '.');
    return data;
  }

  function errorText(err) {
    return err instanceof TokenError ? '' : err.message;
  }

  // --- feedback ---

  let toastTimer = 0;
  function toast(text) {
    nodes.toast.textContent = text;
    nodes.toast.classList.add('is-on');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => nodes.toast.classList.remove('is-on'), 2600);
  }

  function announce(text) {
    nodes.live.textContent = '';
    setTimeout(() => { nodes.live.textContent = text; }, 30);
  }

  function savedTag(id, key) {
    const v = view(id);
    const on = v.saved && v.saved.key === key;
    return h('span', { class: 'saved' + (on ? ' is-on' : ''), 'aria-hidden': 'true', 'data-saved': id + ':' + key, text: 'Saved' });
  }

  function flash(id, key) {
    const v = view(id);
    const mark = {};
    v.saved = { key, mark };
    announce('Saved');
    setTimeout(() => {
      if (!v.saved || v.saved.mark !== mark) return;
      v.saved = null;
      document.querySelectorAll('[data-saved="' + CSS.escape(id + ':' + key) + '"]').forEach((n) => n.classList.remove('is-on'));
    }, SAVED_MS);
  }

  function errorNote(id, key) {
    const msg = view(id).errors[key];
    return msg ? h('p', { class: 'note bad', role: 'alert', text: msg }) : null;
  }

  async function busy(button, label, work) {
    const text = button.textContent;
    button.disabled = true;
    if (label) button.textContent = label;
    try {
      await work();
    } finally {
      button.disabled = false;
      button.textContent = text;
    }
  }

  // --- drawing while keeping what the person is doing ---

  // preserve runs a drawing and puts the focus back on the same control, found by its
  // data-k, or on prefer when that control is gone.
  function preserve(draw, prefer) {
    const a = document.activeElement;
    const key = a && a !== document.body ? a.getAttribute('data-k') : null;
    let sel = null;
    try {
      if (a && typeof a.selectionStart === 'number') sel = [a.selectionStart, a.selectionEnd];
    } catch (e) {
      sel = null;
    }
    rendering = true;
    try {
      draw();
    } finally {
      rendering = false;
    }
    const find = (k) => (k ? document.querySelector('[data-k="' + CSS.escape(k) + '"]') : null);
    let target = null;
    if (key) target = a.isConnected ? a : find(key);
    if (!target && key && prefer) target = find(prefer);
    if (!target && key && key.includes(':')) target = find(key.slice(0, key.lastIndexOf(':')).split(':')[0] + ':summary');
    if (target && document.activeElement !== target) {
      target.focus({ preventScroll: true });
      if (sel && target === a) {
        try {
          a.setSelectionRange(sel[0], sel[1]);
        } catch (e) {
          // Not a text field.
        }
      }
    }
  }

  function focusKey(k) {
    const n = document.querySelector('[data-k="' + CSS.escape(k) + '"]');
    if (n) n.focus({ preventScroll: true });
    return n;
  }

  // keptInput returns the same text field on every drawing. A field that has focus or holds
  // typed text keeps its value; any other one shows the saved value.
  function keptInput(key, attrs, saved, commit, onEnter) {
    let input = kept.get(key);
    if (!input) {
      input = h('input', Object.assign({ type: 'text', class: 'input grow', autocomplete: 'off', spellcheck: 'false', 'data-k': key }, attrs));
      input.addEventListener('input', () => { input.dataset.dirty = '1'; });
      const send = () => {
        if (!commit || !input.dataset.dirty) return;
        delete input.dataset.dirty;
        commit(input.value, input);
      };
      if (commit) input.addEventListener('blur', () => { if (!rendering) send(); });
      input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && (commit || onEnter)) {
          e.preventDefault();
          if (commit) send();
          else onEnter();
        } else if (e.key === 'Escape' && commit && input.dataset.dirty) {
          delete input.dataset.dirty;
          input.value = input.semarSaved || '';
        }
      });
      kept.set(key, input);
    }
    if (saved !== undefined) {
      input.semarSaved = saved;
      if (document.activeElement !== input && !input.dataset.dirty) input.value = saved;
    }
    input.disabled = readOnly();
    return input;
  }

  function resetInput(key) {
    const input = kept.get(key);
    if (input) {
      delete input.dataset.dirty;
      input.value = input.semarSaved || '';
    }
  }

  function forgetKept(id) {
    for (const k of [...kept.keys()]) if (k.startsWith(id + ':')) kept.delete(k);
  }

  // --- saving ---

  function definitionOf(p) {
    const d = {
      id: p.id, kind: p.kind, model: p.model || '', reasoning: p.reasoning || '',
      baseUrl: p.baseUrl || '', apiKey: p.apiKey || '',
    };
    if (isAccountKind(p.kind)) d.setup = p.setup || { mcp: true, skills: true, deny: [] };
    return d;
  }

  // save sends one change at once. Changes to one provider go one after another, and each
  // starts from what the server saved last. A refusal puts the control back.
  function save(id, patch, key, opts) {
    const v = view(id);
    opts = opts || {};
    Object.assign(v.pending, patch);
    delete v.errors[key];
    renderAll();
    v.queue = v.queue.then(async () => {
      const p = provider(id);
      if (!p) return;
      const body = Object.assign(definitionOf(p), patch);
      let ok = false;
      try {
        const res = await api('PUT', '/api/providers/' + enc(id), body);
        Object.assign(p, body, res || {});
        ok = true;
      } catch (err) {
        const msg = errorText(err);
        if (msg) v.errors[key] = msg;
        if (opts.input) resetInput(opts.input);
        if (opts.onFail) opts.onFail();
      }
      for (const k of Object.keys(patch)) if (v.pending[k] === patch[k]) delete v.pending[k];
      if (ok) {
        flash(id, key);
        if (opts.after) opts.after();
      }
      renderAll();
    });
    return v.queue;
  }

  // --- state ---

  async function loadState() {
    let s;
    try {
      s = await api('GET', '/api/state');
    } catch (err) {
      const msg = errorText(err);
      if (msg) showProblem(msg);
      return false;
    }
    s.providers = (s.providers || []).map((p) => Object.assign({ model: '', reasoning: '', baseUrl: '', apiKey: '' }, p));
    state = s;
    for (const id of [...views.keys()]) {
      if (!provider(id)) {
        views.delete(id);
        forgetKept(id);
      }
    }
    renderAll();
    return true;
  }

  function showProblem(msg) {
    nodes.banner.replaceChildren(h('b', { text: 'The page cannot reach the dashboard' }), h('span', { text: msg }));
    nodes.banner.hidden = false;
    if (!state) {
      nodes.hero.replaceChildren();
      nodes.list.replaceChildren();
      nodes.list.removeAttribute('aria-busy');
      nodes.hero.removeAttribute('aria-busy');
    }
  }

  async function fetchModels(id) {
    const v = view(id);
    const p = provider(id);
    if (readOnly()) {
      v.models = { status: 'off', data: null };
      renderAll();
      return;
    }
    if (p && isAPIKind(p.kind) && !p.keyReady) {
      v.models = { status: 'error', data: v.models ? v.models.data : null, error: 'Save the key first, and the models of ' + productName(p) + ' appear in a list here.' };
      renderAll();
      return;
    }
    v.models = { status: 'loading', data: v.models ? v.models.data : null };
    renderAll();
    let data;
    try {
      data = await api('GET', '/api/providers/' + enc(id) + '/models');
    } catch (err) {
      if (!views.has(id)) return;
      v.models = { status: 'error', data: v.models.data, error: errorText(err) || 'The models could not be listed.' };
      renderAll();
      return;
    }
    if (!views.has(id)) return;
    v.models = { status: data.error ? 'error' : 'ok', data, error: data.error || '' };
    renderAll();
  }

  function ensureModels(id) {
    if (!view(id).models) fetchModels(id);
  }

  // --- the whole page ---

  function ordered() {
    const list = state.providers.slice();
    const i = list.findIndex((p) => p.id === state.default);
    if (i > 0) list.unshift(list.splice(i, 1)[0]);
    return list;
  }

  function renderAll(prefer) {
    if (!state) return;
    preserve(() => {
      renderBanner();
      nodes.hero.replaceChildren(renderHero());
      nodes.hero.removeAttribute('aria-busy');
      nodes.list.replaceChildren(...ordered().map(renderCard));
      nodes.list.removeAttribute('aria-busy');
      nodes.addToggle.disabled = readOnly();
      if (readOnly() && !nodes.addPanel.hidden) closeAdd(false);
      renderFooter();
      if (usage) renderUsage();
    }, prefer);
  }

  function renderBanner() {
    if (state.fileError) {
      nodes.banner.replaceChildren(
        h('b', { text: 'The settings file has a problem' }),
        h('span', { class: 'wrap', text: state.fileError }),
        h('span', { text: 'Changes are off until the file is fixed. The list below shows the built-in providers.' }),
      );
      nodes.banner.hidden = false;
    } else {
      nodes.banner.replaceChildren();
      nodes.banner.hidden = true;
    }
  }

  function renderFooter() {
    nodes.footer.replaceChildren(
      h('p', {}, 'Settings file: ', h('span', { class: 'mono wrap', text: state.fileDisplay || state.file }), state.exists ? '' : ' (not written yet)'),
      h('p', { text: 'You can edit the file by hand. Reload this page to see the change.' }),
      h('p', {}, 'This page runs on your computer until you stop ', code('semar dashboard'), '.'),
    );
  }

  function showStart() {
    nodes.start.hidden = false;
    for (const n of [nodes.hero, nodes.providers, nodes.usage, nodes.footer, nodes.banner]) n.hidden = true;
  }

  // --- the provider in use ---

  function renderHero() {
    const p = active();
    if (!p) {
      return h('section', { class: 'hero is-blocked', 'aria-label': 'Provider in use' },
        h('p', { class: 'eyebrow', text: 'Needs attention' }),
        h('p', { class: 'headline', text: 'No provider plans your tests yet' }),
        h('p', { text: 'Choose one below with Use for planning.' }));
    }
    const name = productName(p);
    const st = status(p);
    if (st.blocked) {
      return h('section', { class: 'hero is-blocked', 'aria-label': 'Provider in use' },
        h('p', { class: 'eyebrow', text: 'Needs attention' }),
        h('p', { class: 'headline', text: name + ' cannot plan yet' }),
        h('p', { text: st.why }),
        h('div', { class: 'actions' },
          h('button', {
            type: 'button', class: 'btn primary', 'data-k': 'hero:go', text: 'Go to ' + name,
            on: { click: () => jumpTo(p.id, st.focus) },
          })));
    }
    const v = view(p.id);
    const model = pendingOr(v, 'model', p.model);
    const reasoning = pendingOr(v, 'reasoning', p.reasoning);
    const parts = [];
    if (model) parts.push(name, modelLabel(p.id, model));
    else parts.push(name + "'s default model");
    if (reasoning) parts.push(levelName(reasoning) + ' reasoning');
    const headline = h('p', { class: 'headline' });
    parts.forEach((part, i) => {
      if (i > 0) headline.append(h('span', { class: 'sep', 'aria-hidden': 'true', text: '·' }), ' ');
      headline.append(h('span', { text: part }), i < parts.length - 1 ? ' ' : '');
    });
    const data = v.models && v.models.data;
    const last = !model && data && data.lastUsed && data.lastUsed.model;
    return h('section', { class: 'hero', 'aria-label': 'Provider in use' },
      h('p', { class: 'eyebrow', text: 'Planning with' }),
      headline,
      last ? h('p', { class: 'muted', text: 'The last run used ' + modelLabel(p.id, last) + '.' }) : null,
      h('p', { text: st.sentence }));
  }

  function jumpTo(id, what) {
    const v = view(id);
    v.open = true;
    renderAll();
    const card = document.getElementById('provider-' + id);
    if (!card) return;
    card.open = true;
    card.scrollIntoView({ block: 'start', behavior: reducedMotion() ? 'auto' : 'smooth' });
    const keys = { signin: [':signin'], check: [':check'], key: [':key', ':env'], model: [':model', ':model-free'] }[what] || [];
    if (!keys.some((k) => focusKey(id + k))) focusKey(id + ':summary');
  }

  // --- a provider card ---

  function showsId(p) {
    if (p.id === p.kind) return false;
    return p.id.toLowerCase() !== productName(p).toLowerCase().replace(/\s+/g, '');
  }

  function chipText(p, v) {
    const model = pendingOr(v, 'model', p.model);
    const reasoning = pendingOr(v, 'reasoning', p.reasoning);
    let text = model ? modelLabel(p.id, model) : isAccountKind(p.kind) ? 'Default model' : 'No model';
    if (reasoning) text += ' · ' + levelName(reasoning);
    return text;
  }

  function renderCard(p) {
    const v = view(p.id);
    const isActive = p.id === state.default;
    if (v.open === undefined) v.open = isActive;
    const name = productName(p);
    const st = status(p);
    const chip = chipText(p, v);
    const card = h('details', { class: 'provider' + (isActive ? ' is-active' : ''), id: 'provider-' + p.id, open: v.open });
    card.addEventListener('toggle', () => {
      if (rendering) return;
      v.open = card.open;
      if (card.open) ensureModels(p.id);
    });
    card.append(
      h('summary', { 'data-k': p.id + ':summary' },
        h('span', { class: 'avatar', 'aria-hidden': 'true', text: name.charAt(0).toUpperCase() }),
        h('span', { class: 'title' },
          h('span', { class: 'name' },
            h('span', { text: name }),
            showsId(p) ? h('span', { class: 'mono small faint', text: p.id }) : null,
            isActive ? h('span', { class: 'pill accent plain', text: 'In use' }) : null),
          h('span', { class: 'meta', text: st.meta })),
        h('span', { class: 'summary-end' },
          h('span', { class: 'pill' + (st.tone ? ' ' + st.tone : ''), text: st.pill }),
          h('span', { class: 'chip', title: chip, text: chip }),
          h('span', { class: 'chevron', 'aria-hidden': 'true' }))),
      h('div', { class: 'body' },
        isActive ? null : useRow(p),
        isAccountKind(p.kind) ? accountRow(p, v) : null,
        isAPIKind(p.kind) ? keyRow(p, v) : null,
        p.kind === 'openai' ? addressRow(p, v) : null,
        modelRow(p, v),
        reasoningRow(p, v),
        isAccountKind(p.kind) ? toolsRow(p, v) : null,
        removeRow(p, v, isActive)));
    return card;
  }

  function setting(o, ...controls) {
    const label = o.forId
      ? h('label', { class: 'setting-label', for: o.forId, id: o.labelId, text: o.label })
      : h('div', { class: 'setting-label', id: o.labelId, text: o.label });
    return h('div', { class: 'setting' },
      h('div', {}, label, o.help ? h('p', { class: 'setting-help' }, o.help) : null),
      h('div', { class: 'setting-control' }, controls));
  }

  // a. Use for planning

  function useRow(p) {
    const button = h('button', { type: 'button', class: 'btn primary', 'data-k': p.id + ':use', text: 'Use for planning', disabled: readOnly() });
    button.addEventListener('click', () => busy(button, null, () => useForPlanning(p.id)));
    return setting({ label: 'Use for planning', help: 'New plans run with this provider.' },
      button, errorNote(p.id, 'use'));
  }

  async function useForPlanning(id) {
    const v = view(id);
    delete v.errors.use;
    try {
      await api('PUT', '/api/default', { id });
    } catch (err) {
      const msg = errorText(err);
      if (msg) v.errors.use = msg;
      renderAll();
      return;
    }
    state.default = id;
    v.open = true;
    renderAll(id + ':summary');
    const card = document.getElementById('provider-' + id);
    if (card) card.scrollIntoView({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' });
    toast('Now planning with ' + productName(provider(id)));
    ensureModels(id);
  }

  // b. Account

  function accountRow(p, v) {
    const name = productName(p);
    const o = { label: 'Account', help: 'Semar plans with your own ' + name + ' account.' };
    if (v.login) return setting(o, loginPanel(p, v));
    if (p.health && !p.health.installed) {
      const check = h('button', { type: 'button', class: 'btn', 'data-k': p.id + ':check', text: 'Check again' });
      check.addEventListener('click', () => busy(check, 'Checking…', () => checkAgain(p.id)));
      return setting(o,
        h('p', { class: 'note full', text: name + ' is not installed on this computer. Install it, then check again.' }),
        check,
        v.checkNote ? h('p', { class: 'small muted', role: 'status', text: v.checkNote }) : null);
    }
    if (p.account && p.account.signedIn) return setting(o, h('p', { text: signedInText(p).long }));
    if (p.account) {
      const can = Array.isArray(p.loginCommand) && p.loginCommand.length > 0;
      const button = h('button', { type: 'button', class: 'btn primary', 'data-k': p.id + ':signin', text: 'Sign in', disabled: readOnly() || !can });
      button.addEventListener('click', () => startLogin(p.id));
      return setting(o,
        h('p', { class: 'muted', text: name + ' is not signed in. Sign in once, and Semar uses that account.' }),
        button,
        can ? null : h('p', { class: 'note', text: name + ' cannot be started from here. Sign in to it in a terminal.' }),
        errorNote(p.id, 'signin'));
    }
    const check = h('button', { type: 'button', class: 'btn', 'data-k': p.id + ':check', text: 'Check again' });
    check.addEventListener('click', () => busy(check, 'Checking…', () => checkAgain(p.id)));
    return setting(o, h('p', { class: 'note full', text: p.error || 'The sign-in could not be checked.' }), check);
  }

  async function checkAgain(id) {
    const before = provider(id);
    const wasMissing = before && before.health && !before.health.installed;
    view(id).checkNote = '';
    await loadState();
    const p = provider(id);
    if (p && wasMissing && p.health && !p.health.installed) {
      view(id).checkNote = productName(p) + ' is still not found.';
      renderAll();
    }
  }

  // The sign-in

  function firstURL(lines) {
    for (const line of lines || []) {
      const m = /https:\/\/[^\s"'<>\u001b]+/.exec(line);
      if (!m) continue;
      try {
        const u = new URL(m[0].replace(/[).,;]+$/, ''));
        if (u.protocol === 'https:') return u.href;
      } catch (e) {
        // Not an address.
      }
    }
    return '';
  }

  function loginPanel(p, v) {
    const name = productName(p);
    const login = v.login;
    const job = login.job;
    const running = login.starting || (job && job.state === 'running');
    const lines = job && Array.isArray(job.lines) ? job.lines : [];
    const panel = h('div', { class: 'signin', 'data-login': p.id });
    if (running) {
      const url = firstURL(lines);
      const first = h('li', {}, h('span', {},
        'A browser window opens. ',
        url
          ? ['If it does not, ', h('a', { href: url, target: '_blank', rel: 'noopener noreferrer', 'data-k': p.id + ':signin-link', text: 'open the sign-in page' }), '.']
          : 'If it does not, the sign-in address appears in the output below.'));
      const cancel = h('button', { type: 'button', class: 'btn', 'data-k': p.id + ':signin-cancel', text: 'Cancel' });
      cancel.addEventListener('click', () => cancelLogin(p.id));
      panel.append(
        h('div', { class: 'row', role: 'status' }, h('span', { class: 'spinner', 'aria-hidden': 'true' }), h('b', { text: 'Waiting for you to finish in the browser' })),
        h('ol', { class: 'steps' }, first, h('li', {}, h('span', { text: 'Sign in there, then come back to this page.' }))),
        h('div', { class: 'actions' }, cancel));
    } else {
      let text = 'The sign-in did not finish.';
      if (job && job.state === 'cancelled') text = 'The sign-in was cancelled.';
      else if (job && job.state === 'done') text = 'The sign-in ended, but ' + name + ' still shows as not signed in.';
      else if (job && job.detail) text = 'The sign-in did not finish: ' + job.detail.replace(/\.$/, '') + '.';
      const again = h('button', { type: 'button', class: 'btn primary', 'data-k': p.id + ':signin', text: 'Sign in', disabled: readOnly() });
      again.addEventListener('click', () => startLogin(p.id));
      panel.append(h('p', { class: 'note warn', role: 'status', text: text }), h('div', { class: 'actions' }, again));
    }
    if (login.error) panel.append(h('p', { class: 'note bad', role: 'alert', text: login.error }));
    if (lines.length) {
      const log = h('pre', { class: 'log', text: lines.join('\n') });
      const more = h('details', { class: 'more', open: login.logOpen }, h('summary', { 'data-k': p.id + ':signin-log', text: 'Show the output' }), log);
      more.addEventListener('toggle', () => { if (!rendering) login.logOpen = more.open; });
      panel.append(more);
    }
    if (Array.isArray(p.loginCommand) && p.loginCommand.length) {
      panel.append(h('p', { class: 'small faint wrap' }, 'In a terminal: ', code(p.loginCommand.map(quoteArg).join(' '))));
    }
    return panel;
  }

  // redrawLogin draws only the sign-in panel, so that a poll does not touch the rest.
  function redrawLogin(id) {
    const old = document.querySelector('[data-login="' + CSS.escape(id) + '"]');
    const p = provider(id);
    const v = view(id);
    if (!old || !p || !v.login) {
      renderAll();
      return;
    }
    preserve(() => old.replaceWith(loginPanel(p, v)), id + ':signin-cancel');
  }

  async function startLogin(id) {
    const v = view(id);
    delete v.errors.signin;
    const login = { starting: true, job: null, logOpen: false, error: '', seq: {} };
    v.login = login;
    renderAll(id + ':signin-cancel');
    let job;
    try {
      job = await api('POST', '/api/providers/' + enc(id) + '/login', {});
    } catch (err) {
      v.login = null;
      const msg = errorText(err);
      if (msg) v.errors.signin = msg;
      renderAll(id + ':signin');
      return;
    }
    if (v.login !== login) return;
    login.starting = false;
    login.job = job;
    redrawLogin(id);
    if (login.cancelWanted && job.state === 'running') cancelLogin(id);
    else if (job.state === 'running') poll(id, login);
    else finishLogin(id, login);
  }

  function poll(id, login) {
    const seq = login.seq;
    setTimeout(async () => {
      const v = view(id);
      if (v.login !== login || login.seq !== seq) return;
      let job;
      try {
        job = await api('GET', '/api/logins/' + enc(login.job.id));
      } catch (err) {
        if (v.login !== login || login.seq !== seq) return;
        login.error = errorText(err);
        if (login.error) redrawLogin(id);
        return;
      }
      if (v.login !== login || login.seq !== seq) return;
      login.error = '';
      login.job = job;
      redrawLogin(id);
      if (job.state === 'running') poll(id, login);
      else finishLogin(id, login);
    }, 1000);
  }

  async function finishLogin(id, login) {
    if (login.job.state !== 'done') {
      redrawLogin(id);
      return;
    }
    await loadState();
    const v = view(id);
    if (v.login !== login) return;
    const p = provider(id);
    if (p && p.account && p.account.signedIn) {
      v.login = null;
      renderAll(id + ':summary');
      toast('Signed in');
      fetchModels(id);
    } else {
      redrawLogin(id);
    }
  }

  async function cancelLogin(id) {
    const v = view(id);
    const login = v.login;
    if (!login) return;
    if (!login.job) {
      login.cancelWanted = true;
      return;
    }
    login.seq = {};
    try {
      login.job = await api('DELETE', '/api/logins/' + enc(login.job.id), {});
    } catch (err) {
      login.error = errorText(err);
    }
    if (login.job.state === 'running') login.job = Object.assign({}, login.job, { state: 'cancelled' });
    redrawLogin(id);
    focusKey(id + ':signin');
  }

  // c. API key

  function keyRow(p, v) {
    const name = productName(p);
    const o = { label: 'API key', help: 'Semar sends it only to ' + name + '.' };
    const id = p.id;
    if (p.keyReady && !v.replacing && !v.envMode) {
      const replace = h('button', { type: 'button', class: 'btn quiet', 'data-k': id + ':replace', text: 'Replace key', disabled: readOnly() });
      replace.addEventListener('click', () => {
        v.replacing = true;
        renderAll();
        focusKey(id + ':key');
      });
      return setting(o, h('div', { class: 'row' }, h('span', { text: keyWhere(p) }), replace, savedTag(id, 'key')), errorNote(id, 'key'));
    }
    const controls = [];
    if (!p.keyReady) controls.push(h('p', { class: 'note warn', text: keyMissing(p) }));
    if (v.envMode) {
      const r = keyRef(p);
      const env = keptInput(id + ':env', { placeholder: 'ACME_API_KEY', 'aria-describedby': id + '-env-help' }, r.type === 'env' ? r.name : '', (value) => saveEnv(id, value));
      const envId = env.id || (env.id = nextId('env'));
      const back = h('button', { type: 'button', class: 'btn quiet', 'data-k': id + ':paste', text: 'Paste a key instead' });
      back.addEventListener('click', () => {
        v.envMode = false;
        renderAll();
        focusKey(id + ':key');
      });
      controls.push(
        h('label', { class: 'field full', for: envId }, h('span', { text: 'Variable name' }), h('span', { class: 'row' }, env, savedTag(id, 'env'))),
        h('p', { class: 'small muted', id: id + '-env-help', text: 'Semar reads the key from this variable each time it plans.' }),
        errorNote(id, 'env'),
        h('div', { class: 'actions' }, back, keepButton(p, v)));
      return setting(o, controls);
    }
    const key = keptInput(id + ':key', { type: 'password', autocomplete: 'new-password', placeholder: 'Paste the key' }, undefined, null, () => saveKey(id));
    const keyId = key.id || (key.id = nextId('key'));
    const store = h('button', { type: 'button', class: 'btn primary', 'data-k': id + ':save-key', text: 'Save key', disabled: readOnly() });
    store.addEventListener('click', () => busy(store, 'Saving…', () => saveKey(id)));
    const useEnv = h('button', { type: 'button', class: 'btn quiet', 'data-k': id + ':use-env', text: 'Use an environment variable instead', disabled: readOnly() });
    useEnv.addEventListener('click', () => {
      v.envMode = true;
      renderAll();
      focusKey(id + ':env');
    });
    controls.push(
      h('label', { class: 'sr-only', for: keyId, text: 'API key for ' + name }),
      h('div', { class: 'row full' }, key, store, savedTag(id, 'key')),
      h('p', { class: 'small muted', text: 'The key goes to your keychain. This page never shows it again.' }),
      errorNote(id, 'key'),
      h('div', { class: 'actions' }, useEnv, keepButton(p, v)));
    return setting(o, controls);
  }

  function keepButton(p, v) {
    if (!p.keyReady) return null;
    const keep = h('button', { type: 'button', class: 'btn quiet', 'data-k': p.id + ':keep-key', text: 'Keep the current key' });
    keep.addEventListener('click', () => {
      v.replacing = false;
      v.envMode = false;
      const input = kept.get(p.id + ':key');
      if (input) input.value = '';
      renderAll(p.id + ':replace');
      focusKey(p.id + ':replace');
    });
    return keep;
  }

  async function saveKey(id) {
    const v = view(id);
    const input = kept.get(id + ':key');
    if (!input) return;
    delete v.errors.key;
    if (!input.value.trim()) {
      v.errors.key = 'Paste the key first.';
      renderAll();
      return;
    }
    const request = api('PUT', '/api/providers/' + enc(id) + '/key', { value: input.value });
    input.value = '';
    delete input.dataset.dirty;
    try {
      await request;
    } catch (err) {
      const msg = errorText(err);
      if (msg) v.errors.key = msg;
      renderAll();
      return;
    }
    v.replacing = false;
    v.envMode = false;
    await loadState();
    flash(id, 'key');
    toast('Key saved');
    renderAll(id + ':replace');
    fetchModels(id);
  }

  function saveEnv(id, value) {
    const name = value.trim();
    const v = view(id);
    const p = provider(id);
    if (!p) return;
    if (!name) {
      v.errors.env = 'Give the name of the variable.';
      resetInput(id + ':env');
      renderAll();
      return;
    }
    if (p.apiKey === 'env:' + name) return;
    save(id, { apiKey: 'env:' + name }, 'env', {
      input: id + ':env',
      after: async () => {
        await loadState();
        if (provider(id) && provider(id).keyReady) v.envMode = false;
        renderAll();
        fetchModels(id);
      },
    });
  }

  // Server address

  function addressRow(p, v) {
    const input = keptInput(p.id + ':address', { type: 'url', placeholder: 'https://api.openai.com/v1', inputmode: 'url' }, p.baseUrl || '', (value) => {
      const next = value.trim();
      const cur = provider(p.id);
      if (!cur || next === (cur.baseUrl || '')) return;
      save(p.id, { baseUrl: next }, 'address', { input: p.id + ':address', after: () => fetchModels(p.id) });
    });
    const inputId = input.id || (input.id = nextId('address'));
    return setting({ label: 'Server address', help: 'Leave it empty for OpenAI.', forId: inputId },
      h('div', { class: 'row full' }, input, savedTag(p.id, 'address')),
      errorNote(p.id, 'address'));
  }

  // d. Model

  function modelRow(p, v) {
    const name = productName(p);
    const id = p.id;
    const current = pendingOr(v, 'model', p.model);
    const info = modelInfo(id, current);
    const m = v.models;
    const data = info.data;
    const used = data && Array.isArray(data.used) ? data.used : [];
    const help = isAccountKind(p.kind) ? 'The model ' + name + ' uses to plan your tests.' : 'The model that plans your tests.';
    const controls = [];

    if (m && m.status === 'error') {
      const listId = 'models-used-' + id;
      const text = keptInput(id + ':model-free', {
        list: used.length ? listId : null,
        placeholder: isAccountKind(p.kind) ? 'Empty for ' + name + "'s default" : 'Model ID',
      }, current, (value) => saveModelText(id, value, id + ':model-free'));
      const textId = text.id || (text.id = nextId('model'));
      controls.push(
        h('div', { class: 'row full' }, text, savedTag(id, 'model')),
        used.length ? h('datalist', { id: listId }, used.map((u) => h('option', { value: u }))) : null,
        h('p', { class: 'note', text: m.error }),
        errorNote(id, 'model'));
      return setting({ label: 'Model', help, forId: textId }, controls);
    }

    const selId = 'model-' + id;
    const select = h('select', { class: 'input grow', id: selId, 'data-k': id + ':model', disabled: readOnly() });
    if (isAccountKind(p.kind)) select.append(h('option', { value: '', text: name + "'s default" }));
    else select.append(h('option', { value: '', disabled: true, text: 'Choose a model' }));
    const listedIds = new Set(info.listed.map((x) => x.id));
    for (const x of info.listed) {
      select.append(h('option', { value: x.id, text: x.label && x.label !== x.id ? x.label + ' (' + x.id + ')' : x.id }));
    }
    const usedOnly = used.filter((u) => !listedIds.has(u));
    if (current && !listedIds.has(current) && !usedOnly.includes(current)) select.append(h('option', { value: current, text: current }));
    if (usedOnly.length) select.append(h('optgroup', { label: 'Used before' }, usedOnly.map((u) => h('option', { value: u, text: u }))));
    select.append(h('option', { value: OTHER, text: 'Another model…' }));
    select.value = v.other ? OTHER : current;
    select.addEventListener('change', () => onModelChange(id, select.value));
    controls.push(h('div', { class: 'row full' }, select, savedTag(id, 'model')));

    if (v.other) {
      const text = keptInput(id + ':model-text', { placeholder: 'Model ID, such as acme-large' }, undefined, (value) => saveModelText(id, value, id + ':model-text'));
      const textId = text.id || (text.id = nextId('model'));
      controls.push(h('label', { class: 'sr-only', for: textId, text: 'Model ID' }), text,
        h('p', { class: 'small muted', text: 'Press Enter to use it.' }));
    }
    if (m && m.status === 'loading' && !info.listed.length) {
      controls.push(h('p', { class: 'small muted', role: 'status', text: 'Loading the models…' }));
    } else if (info.entry && info.entry.description) {
      controls.push(h('p', { class: 'small muted', text: info.entry.description }));
    } else if (!current && data && data.lastUsed && data.lastUsed.model) {
      controls.push(h('p', { class: 'small muted', text: 'The last run used ' + modelLabel(id, data.lastUsed.model) + '.' }));
    }
    controls.push(errorNote(id, 'model'));
    return setting({ label: 'Model', help, forId: selId }, controls);
  }

  function onModelChange(id, value) {
    const v = view(id);
    if (value === OTHER) {
      v.other = true;
      const input = kept.get(id + ':model-text');
      if (input) {
        input.value = '';
        delete input.dataset.dirty;
      }
      renderAll();
      focusKey(id + ':model-text');
      return;
    }
    v.other = false;
    changeModel(id, value);
  }

  function saveModelText(id, value, input) {
    const next = value.trim();
    const p = provider(id);
    if (!p) return;
    if (next === (p.model || '')) {
      view(id).other = false;
      renderAll(id + ':model');
      return;
    }
    if (!next && !isAccountKind(p.kind)) {
      view(id).errors.model = 'Give the ID of a model.';
      renderAll();
      return;
    }
    changeModel(id, next, input);
  }

  // changeModel saves a model, and sets reasoning back to Default when the new model does
  // not know the saved level.
  function changeModel(id, model, input) {
    const v = view(id);
    const p = provider(id);
    const patch = { model };
    v.reasoningNote = '';
    const level = pendingOr(v, 'reasoning', p.reasoning);
    const info = modelInfo(id, model);
    if (level && info.known && !info.levels.includes(level)) {
      patch.reasoning = '';
      v.reasoningNote = (info.entry.label || model) + ' has no ' + levelName(level) + ' level, so reasoning is back to Default.';
    }
    save(id, patch, 'model', {
      input,
      after: () => { if (input) v.other = false; },
      onFail: () => { v.reasoningNote = ''; },
    });
  }

  // e. Reasoning

  function reasoningRow(p, v) {
    const id = p.id;
    const model = pendingOr(v, 'model', p.model);
    const current = pendingOr(v, 'reasoning', p.reasoning) || '';
    const info = modelInfo(id, model);
    const levels = info.levels.slice();
    if (current && !levels.includes(current)) levels.push(current);
    if (!levels.length) return null;
    const labelId = 'reasoning-label-' + id;
    const group = h('div', { class: 'segmented', role: 'radiogroup', 'aria-labelledby': labelId });
    for (const l of [''].concat(levels)) {
      const input = h('input', { type: 'radio', name: 'reasoning-' + id, value: l, checked: l === current, disabled: readOnly(), 'data-k': id + ':reasoning:' + (l || 'default') });
      input.addEventListener('change', () => {
        if (!input.checked) return;
        v.reasoningNote = '';
        save(id, { reasoning: l }, 'reasoning');
      });
      group.append(h('label', {}, input, h('span', { text: l ? levelName(l) : 'Default' })));
    }
    return setting({ label: 'Reasoning', labelId, help: 'More reasoning finds more, and takes more time and tokens.' },
      h('div', { class: 'row' }, group, savedTag(id, 'reasoning')),
      !current && info.def ? h('p', { class: 'small muted', text: "This model's default is " + levelName(info.def) + '.' }) : null,
      v.reasoningNote ? h('p', { class: 'note', role: 'status', text: v.reasoningNote }) : null,
      errorNote(id, 'reasoning'));
  }

  // f. Your tools

  function toolsRow(p, v) {
    const id = p.id;
    const name = productName(p);
    const setup = pendingOr(v, 'setup', p.setup) || { mcp: true, skills: true, deny: [] };
    const sw = (key, label, help) => {
      const input = h('input', { type: 'checkbox', role: 'switch', checked: setup[key] !== false, disabled: readOnly(), 'data-k': id + ':' + key });
      input.addEventListener('change', () => {
        const cur = provider(id);
        const base = pendingOr(v, 'setup', cur.setup) || { mcp: true, skills: true, deny: [] };
        save(id, { setup: Object.assign({}, base, { [key]: input.checked }) }, key);
      });
      return h('label', { class: 'switch' }, input, h('span', { class: 'track', 'aria-hidden': 'true' }),
        h('span', {}, h('span', { class: 'label' }, label, ' ', savedTag(id, key)), h('span', { class: 'help', text: help })));
    };
    const note = h('p', { class: 'note full' },
      'A plan uses more tokens with these on, and the planner has them while it reads a repository. ',
      code('semar plan --no-setup'), ' turns them off for one run.',
      p.kind === 'codex' ? ' Codex always reads your own instruction file.' : '');
    const controls = [
      sw('mcp', 'MCP servers', 'The planner can call the tools you set up in ' + name + '.'),
      errorNote(id, 'mcp'),
      sw('skills', 'Skills', 'The planner can use your skills.'),
      errorNote(id, 'skills'),
      note,
    ];
    if (p.kind === 'claude') {
      const deny = Array.isArray(setup.deny) ? setup.deny : [];
      const input = keptInput(id + ':deny', { placeholder: 'Bash, WebFetch' }, deny.join(', '), (value) => saveDeny(id, value));
      const inputId = input.id || (input.id = nextId('deny'));
      if (v.denyOpen === undefined) v.denyOpen = deny.length > 0;
      const more = h('details', { class: 'more full', open: v.denyOpen },
        h('summary', { 'data-k': id + ':deny-toggle', text: deny.length ? 'Tools that never run (' + deny.length + ')' : 'Tools that never run' }),
        h('div', { class: 'stack' },
          h('label', { class: 'field', for: inputId }, h('span', { text: 'Tool names, separated by commas' }), h('span', { class: 'row' }, input, savedTag(id, 'deny'))),
          h('p', { class: 'small muted', text: 'The planner can never call these, whatever your permission rules say.' }),
          errorNote(id, 'deny')));
      more.addEventListener('toggle', () => { if (!rendering) v.denyOpen = more.open; });
      controls.push(more);
    }
    return setting({ label: 'Your tools', help: 'What the planner may use from your ' + name + ' setup.' }, controls);
  }

  function saveDeny(id, value) {
    const v = view(id);
    const p = provider(id);
    if (!p) return;
    const list = value.split(',').map((s) => s.trim()).filter(Boolean);
    const base = pendingOr(v, 'setup', p.setup) || { mcp: true, skills: true, deny: [] };
    if (list.join(',') === (base.deny || []).join(',')) return;
    save(id, { setup: Object.assign({}, base, { deny: list }) }, 'deny', { input: id + ':deny' });
  }

  // g. Remove

  function removeRow(p, v, isActive) {
    if (isActive) return setting({ label: 'Remove', help: 'To remove this provider, use another one for planning first.' });
    const id = p.id;
    const help = keyRef(p).type === 'keychain'
      ? 'Takes it off the list, and deletes its key from your keychain.'
      : 'Takes it off the list.';
    if (v.confirm) {
      const yes = h('button', { type: 'button', class: 'btn danger', 'data-k': id + ':remove-yes', text: 'Remove' });
      const no = h('button', { type: 'button', class: 'btn quiet', 'data-k': id + ':remove-no', text: 'Keep' });
      yes.addEventListener('click', () => busy(yes, 'Removing…', () => removeProvider(id)));
      no.addEventListener('click', () => endConfirm(id, true));
      return setting({ label: 'Remove', help },
        h('p', { class: 'small', role: 'status', text: 'Remove ' + productName(p) + ' from the list?' }),
        h('div', { class: 'actions' }, yes, no),
        errorNote(id, 'remove'));
    }
    const button = h('button', { type: 'button', class: 'btn danger', 'data-k': id + ':remove', text: 'Remove provider', disabled: readOnly() });
    button.addEventListener('click', () => {
      v.confirm = true;
      delete v.errors.remove;
      clearTimeout(v.confirmTimer);
      v.confirmTimer = setTimeout(() => endConfirm(id, false), CONFIRM_MS);
      renderAll();
      focusKey(id + ':remove-no');
    });
    return setting({ label: 'Remove', help }, button, errorNote(id, 'remove'));
  }

  function endConfirm(id, moveFocus) {
    const v = views.get(id);
    if (!v || !v.confirm) return;
    clearTimeout(v.confirmTimer);
    v.confirm = false;
    renderAll(id + ':remove');
    if (moveFocus) focusKey(id + ':remove');
  }

  async function removeProvider(id) {
    const v = view(id);
    clearTimeout(v.confirmTimer);
    const name = productName(provider(id));
    try {
      await api('DELETE', '/api/providers/' + enc(id), {});
    } catch (err) {
      const msg = errorText(err);
      if (msg) v.errors.remove = msg;
      v.confirm = false;
      renderAll(id + ':remove');
      return;
    }
    await loadState();
    const first = document.querySelector('#provider-list summary');
    if (first) first.focus({ preventScroll: true });
    toast(name + ' removed');
  }

  // --- adding a provider ---

  const add = { built: false, kind: null, envMode: false, nameEdited: false };

  function freeName(base) {
    const taken = new Set((state ? state.providers : []).map((p) => p.id.toLowerCase()));
    if (!taken.has(base)) return base;
    for (let i = 2; ; i++) if (!taken.has(base + '-' + i)) return base + '-' + i;
  }

  function isLoopback(url) {
    const host = hostOf(url);
    return host === 'localhost' || host === '127.0.0.1' || host === '[::1]' || host === '::1';
  }

  function buildAdd() {
    const n = {};
    add.n = n;
    const titleId = 'add-title';
    const kinds = h('div', { class: 'kinds', role: 'radiogroup', 'aria-labelledby': titleId });
    for (const k of ADD_KINDS) {
      const input = h('input', { type: 'radio', name: 'add-kind', value: k.key, 'data-k': 'add:' + k.key });
      input.addEventListener('change', () => { if (input.checked) chooseKind(k); });
      kinds.append(h('label', { class: 'kind' }, input, h('span', { class: 'face' }, h('b', { text: k.name }), h('span', { text: k.help }))));
    }
    const field = (label, input, wide, extra) => {
      input.id = input.id || nextId('add');
      return h('label', { class: 'field' + (wide ? ' wide' : ''), for: input.id }, h('span', { text: label }), input, extra || null);
    };
    const enter = (input) => input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        submitAdd();
      }
    });
    n.name = h('input', { type: 'text', class: 'input', autocomplete: 'off', spellcheck: 'false', 'data-k': 'add:name' });
    n.name.addEventListener('input', () => { add.nameEdited = true; });
    n.address = h('input', { type: 'url', class: 'input', autocomplete: 'off', spellcheck: 'false', placeholder: 'https://llm.example.com/v1', 'data-k': 'add:address' });
    n.key = h('input', { type: 'password', class: 'input', autocomplete: 'new-password', placeholder: 'Paste the key', 'data-k': 'add:key' });
    n.env = h('input', { type: 'text', class: 'input', autocomplete: 'off', spellcheck: 'false', 'data-k': 'add:env' });
    [n.name, n.address, n.key, n.env].forEach(enter);
    n.nameField = field('Name', n.name, false, h('span', { class: 'small faint', text: 'Letters, digits, dot, underscore, or hyphen.' }));
    n.addressField = field('Server address', n.address, true);
    n.keyField = field('API key', n.key, false, h('span', { class: 'small faint', text: 'It goes to your keychain.' }));
    n.envField = field('Variable name', n.env, false, h('span', { class: 'small faint', text: 'Semar reads the key from it each time.' }));
    n.toggle = h('button', { type: 'button', class: 'btn quiet', 'data-k': 'add:env-toggle' });
    n.toggle.addEventListener('click', () => {
      add.envMode = !add.envMode;
      showAddParts();
      (add.envMode ? n.env : n.key).focus();
    });
    n.toggleRow = h('div', { class: 'wide' }, n.toggle);
    n.note = h('div', { class: 'wide', 'aria-live': 'polite' });
    n.submit = h('button', { type: 'button', class: 'btn primary', 'data-k': 'add:submit', text: 'Add provider' });
    n.submit.addEventListener('click', submitAdd);
    const cancel = h('button', { type: 'button', class: 'btn quiet', 'data-k': 'add:cancel', text: 'Cancel' });
    cancel.addEventListener('click', () => closeAdd(true));
    n.form = h('div', { class: 'form', hidden: true },
      n.nameField, n.keyField, n.envField, n.addressField, n.toggleRow, n.note,
      h('div', { class: 'actions wide' }, n.submit, cancel));
    nodes.addPanel.append(
      h('div', { class: 'stack' }, h('h3', { id: titleId, text: 'Add a provider' }),
        h('p', { class: 'small muted', text: 'Choose what plans your tests. You pick its model after you add it.' })),
      kinds, n.form);
    add.built = true;
  }

  function chooseKind(k) {
    add.kind = k;
    const n = add.n;
    if (!add.nameEdited || !n.name.value.trim()) {
      n.name.value = freeName(k.id);
      add.nameEdited = false;
    }
    n.env.placeholder = k.env || 'ACME_API_KEY';
    n.note.replaceChildren();
    showAddParts();
  }

  function showAddParts() {
    const n = add.n;
    const k = add.kind;
    const api = Boolean(k && isAPIKind(k.kind));
    n.form.hidden = !k;
    n.addressField.hidden = !(k && k.address);
    n.keyField.hidden = !api || add.envMode;
    n.envField.hidden = !api || !add.envMode;
    n.toggleRow.hidden = !api;
    n.toggle.textContent = add.envMode ? 'Paste a key instead' : 'Use an environment variable instead';
  }

  function openAdd() {
    if (!add.built) buildAdd();
    nodes.addPanel.hidden = false;
    nodes.addToggle.setAttribute('aria-expanded', 'true');
    const first = nodes.addPanel.querySelector('input[name="add-kind"]');
    if (first) first.focus();
  }

  function closeAdd(moveFocus) {
    nodes.addPanel.hidden = true;
    nodes.addToggle.setAttribute('aria-expanded', 'false');
    if (add.built) {
      const n = add.n;
      nodes.addPanel.querySelectorAll('input[name="add-kind"]').forEach((i) => { i.checked = false; });
      for (const i of [n.name, n.address, n.key, n.env]) i.value = '';
      add.kind = null;
      add.envMode = false;
      add.nameEdited = false;
      n.note.replaceChildren();
      showAddParts();
    }
    if (moveFocus) nodes.addToggle.focus();
  }

  function addProblem(text) {
    add.n.note.replaceChildren(h('p', { class: 'note bad', role: 'alert', text }));
  }

  async function submitAdd() {
    const n = add.n;
    const k = add.kind;
    if (!k || n.submit.disabled) return;
    const id = n.name.value.trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(id)) {
      addProblem('Give a name of letters, digits, dot, underscore, or hyphen, starting with a letter or digit.');
      n.name.focus();
      return;
    }
    const body = { id, kind: k.kind, model: '', reasoning: '', baseUrl: k.baseUrl || '', apiKey: '' };
    if (k.address) {
      body.baseUrl = n.address.value.trim();
      if (!body.baseUrl) {
        addProblem('Give the address of the server, such as https://llm.example.com/v1.');
        n.address.focus();
        return;
      }
    }
    let pasted = false;
    if (isAPIKind(k.kind)) {
      if (add.envMode) {
        const env = n.env.value.trim();
        if (!env) {
          addProblem('Give the name of the variable that holds the key.');
          n.env.focus();
          return;
        }
        body.apiKey = 'env:' + env;
      } else if (n.key.value.trim()) {
        body.apiKey = 'keychain:' + id;
        pasted = true;
      } else if (!isLoopback(body.baseUrl)) {
        addProblem('Paste the API key, or use an environment variable.');
        n.key.focus();
        return;
      }
    }
    n.note.replaceChildren();
    await busy(n.submit, 'Adding…', async () => {
      try {
        await api('PUT', '/api/providers/' + enc(id), body, { 'If-None-Match': '*' });
      } catch (err) {
        const msg = errorText(err);
        if (msg) addProblem(msg);
        return;
      }
      let keyError = '';
      if (pasted) {
        const request = api('PUT', '/api/providers/' + enc(id) + '/key', { value: n.key.value });
        n.key.value = '';
        try {
          await request;
        } catch (err) {
          keyError = errorText(err);
        }
      }
      closeAdd(false);
      const v = view(id);
      v.open = true;
      if (keyError) v.errors.key = keyError;
      await loadState();
      const card = document.getElementById('provider-' + id);
      if (card) {
        card.scrollIntoView({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' });
        focusKey(id + ':summary');
      }
      toast(keyError ? 'Added, but the key was not saved' : 'Added');
      fetchModels(id);
    });
  }

  // --- usage ---

  function renderTabs() {
    nodes.tabs.replaceChildren(...PERIODS.map((p) => {
      const b = h('button', { type: 'button', 'aria-pressed': String(p.days === usageDays), text: p.label, 'data-k': 'days:' + p.days });
      b.addEventListener('click', () => {
        if (usageDays === p.days) return;
        usageDays = p.days;
        try {
          localStorage.setItem(DAYS_KEY, String(p.days));
        } catch (e) {
          // The choice lasts for this visit only.
        }
        renderTabs();
        loadUsage();
      });
      return b;
    }));
  }

  async function loadUsage() {
    const days = usageDays;
    if (!usage) nodes.usageBody.replaceChildren(h('div', { class: 'skeleton row-h', 'aria-hidden': 'true' }));
    let u;
    try {
      u = await api('GET', '/api/usage?days=' + days);
    } catch (err) {
      const msg = errorText(err);
      if (msg && days === usageDays) nodes.usageBody.replaceChildren(h('p', { class: 'note bad', role: 'alert', text: msg }));
      return;
    }
    if (days !== usageDays) return;
    usage = u;
    renderUsage();
  }

  function stat(label, value, full, sub) {
    return h('div', { class: 'stat' },
      h('span', { class: 'label', text: label }),
      h('span', { class: 'value', title: full || null, text: value }),
      sub ? h('span', { class: 'sub', text: sub }) : null);
  }

  function windowName(w) {
    if (WINDOW_NAMES[w]) return WINDOW_NAMES[w];
    const m = /^(five_hour|seven_day)_(.+)$/.exec(w || '');
    if (m) return WINDOW_NAMES[m[1]] + ', ' + m[2].replace(/_/g, ' ');
    return cap((w || 'Window').replace(/_/g, ' '));
  }

  function meter(w) {
    const u = Math.max(0, Math.min(1, Number(w.utilization) || 0));
    const tone = u >= 0.9 ? ' bad' : u >= 0.75 ? ' warn' : '';
    const fill = h('i');
    fill.style.width = (u * 100).toFixed(1) + '%';
    const name = windowName(w.window);
    const resets = resetsAt(w.resetsAt);
    const reached = w.status === 'rejected';
    return h('div', { class: 'meter' + tone },
      h('div', { class: 'head' }, h('b', { text: name }), h('span', { class: 'num', text: percentFormat.format(u) })),
      h('div', { class: 'bar', role: 'progressbar', 'aria-label': name, 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(Math.round(u * 100)) }, fill),
      resets || reached ? h('div', { class: 'foot', text: (reached ? 'Limit reached. ' : '') + (resets ? 'Resets ' + resets : '') }) : null);
  }

  function outcomePill(outcome) {
    if (outcome === 'ok') return h('span', { class: 'pill ok', text: 'Done' });
    if (outcome === 'rejected') return h('span', { class: 'pill warn', text: 'Rejected' });
    const why = (outcome || '').startsWith('failed:') ? outcome.slice(7).replace(/_/g, ' ') : '';
    return h('span', { class: 'pill bad', title: why ? 'Failed: ' + why : null, text: why ? 'Failed, ' + why : 'Failed' });
  }

  function cell(text, num, title) {
    return h('td', { class: num ? 'num' : null, title: title || null, text });
  }

  function table(labelId, heads, rows) {
    return h('div', { class: 'table-wrap' },
      h('table', { 'aria-labelledby': labelId },
        h('thead', {}, h('tr', {}, heads.map((x) => h('th', { scope: 'col', class: x.num ? 'num' : null, text: x.label })))),
        h('tbody', {}, rows)));
  }

  function renderUsage() {
    const u = usage;
    const rows = Array.isArray(u.rows) ? u.rows : [];
    if (!rows.length) {
      const title = u.days === 1 ? 'No runs today' : u.days === 7 ? 'No runs in the last 7 days' : 'No usage yet';
      nodes.usageBody.replaceChildren(h('div', { class: 'empty' },
        h('b', { text: title }),
        h('span', {}, 'Usage appears here after your first ', code('semar plan'), '.')));
      return;
    }
    const sum = (k) => rows.reduce((a, r) => a + (Number(r[k]) || 0), 0);
    const runs = sum('runs');
    const failed = sum('failed');
    const input = sum('inputTokens');
    const cached = sum('cacheReadTokens');
    const output = sum('outputTokens');
    const spent = sum('costUsd');
    const out = [];
    out.push(h('div', { class: 'stats' },
      stat('Runs', numberFormat.format(runs), null, failed ? numberFormat.format(failed) + ' failed' : null),
      stat('Input tokens', short(input), numberFormat.format(input), cached ? short(cached) + ' read from cache' : null),
      stat('Output tokens', short(output), numberFormat.format(output)),
      stat('Cost', cost(spent), null, spent ? 'as the provider reported' : null)));

    const limits = Object.entries(u.limits || {}).filter(([, l]) => l && Array.isArray(l.windows) && l.windows.length);
    limits.sort((a, b) => nameOfId(a[0], '').localeCompare(nameOfId(b[0], '')));
    for (const [id, l] of limits) {
      const d = toDate(l.at);
      const headId = nextId('limits');
      out.push(h('div', { class: 'limits', role: 'group', 'aria-labelledby': headId },
        h('div', { class: 'section-head' },
          h('h3', { id: headId, text: nameOfId(id, '') + ' subscription' }),
          d ? h('span', { class: 'small faint', text: 'as of ' + when(l.at) }) : null),
        h('div', { class: 'meters' }, l.windows.map(meter))));
    }

    const byId = nextId('by-model');
    out.push(h('h3', { id: byId, text: 'By model' }));
    out.push(table(byId, [
      { label: 'Provider' }, { label: 'Model' }, { label: 'Runs', num: true }, { label: 'Failed', num: true },
      { label: 'Turns', num: true }, { label: 'Input', num: true }, { label: 'Output', num: true }, { label: 'Cost', num: true },
    ], rows.map((r) => h('tr', {},
      cell(nameOfId(r.provider, r.kind)),
      cell(r.model ? modelLabel(r.provider, r.model) : 'Default model'),
      cell(numberFormat.format(r.runs || 0), true),
      cell(numberFormat.format(r.failed || 0), true),
      cell(numberFormat.format(r.turns || 0), true),
      cell(short(r.inputTokens), true, numberFormat.format(r.inputTokens || 0)),
      cell(short(r.outputTokens), true, numberFormat.format(r.outputTokens || 0)),
      cell(cost(r.costUsd), true)))));

    const recent = Array.isArray(u.recent) ? u.recent : [];
    if (recent.length) {
      const latestId = nextId('latest');
      out.push(h('details', { class: 'more' },
        h('summary', { id: latestId, 'data-k': 'usage:latest', text: 'Latest runs' }),
        table(latestId, [
          { label: 'When' }, { label: 'Provider' }, { label: 'Model' }, { label: 'Reasoning' }, { label: 'Result' },
          { label: 'Turns', num: true }, { label: 'Tokens', num: true }, { label: 'Cost', num: true }, { label: 'Time', num: true },
        ], recent.map((r) => {
          const tokens = (r.inputTokens || 0) + (r.outputTokens || 0);
          return h('tr', {},
            cell(when(r.at)),
            cell(nameOfId(r.provider, r.kind)),
            cell(r.model ? modelLabel(r.provider, r.model) : 'Default model'),
            cell(r.reasoning ? levelName(r.reasoning) : 'Default'),
            h('td', {}, outcomePill(r.outcome)),
            cell(numberFormat.format(r.turns || 0), true),
            cell(short(tokens), true, numberFormat.format(tokens)),
            cell(cost(r.costUsd), true),
            cell(duration(r.durationMs), true));
        }))));
    }
    const wasOpen = nodes.usageBody.querySelector('details.more[open]');
    nodes.usageBody.replaceChildren(...out);
    if (wasOpen) {
      const d = nodes.usageBody.querySelector('details.more');
      if (d) d.open = true;
    }
  }

  // --- start ---

  function start() {
    nodes.banner = document.getElementById('banner');
    nodes.start = document.getElementById('start');
    nodes.hero = document.getElementById('hero');
    nodes.providers = document.getElementById('providers');
    nodes.list = document.getElementById('provider-list');
    nodes.addToggle = document.getElementById('add-toggle');
    nodes.addPanel = document.getElementById('add-panel');
    nodes.usage = document.getElementById('usage');
    nodes.tabs = document.getElementById('usage-tabs');
    nodes.usageBody = document.getElementById('usage-body');
    nodes.footer = document.getElementById('footer');
    nodes.toast = document.getElementById('toast');
    nodes.live = document.getElementById('live');

    token = readToken();
    if (!token) {
      showStart();
      return;
    }
    try {
      const d = Number(localStorage.getItem(DAYS_KEY));
      if (PERIODS.some((p) => p.days === d)) usageDays = d;
    } catch (e) {
      // The first visit shows 30 days.
    }
    nodes.addToggle.addEventListener('click', () => {
      if (nodes.addPanel.hidden) openAdd();
      else closeAdd(true);
    });
    nodes.addPanel.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') closeAdd(true);
    });
    renderTabs();
    loadUsage();
    loadState().then((ok) => {
      if (!ok || !state) return;
      for (const p of state.providers) if (view(p.id).open) ensureModels(p.id);
    });
  }

  start();
})();
