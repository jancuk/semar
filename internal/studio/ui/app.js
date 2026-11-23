'use strict';

// The page draws everything from what the server sends and puts every text in with textContent,
// because element texts, step texts, and file previews are data. It keeps no secret: a secret is sent
// once, and the field that held it is emptied.
(function () {
  const $ = (id) => document.getElementById(id);

  let token = null;
  let state = null; // GET /api/state
  let devices = []; // GET /api/devices
  let foreground = null;
  let session = null; // the last answer that held a recording: frame, steps, kind, id
  let frame = null;
  let mode = 'act';
  let busy = false;
  let shotURL = '';
  let finished = null; // the answer of the last save
  let editing = -1;

  // --- small helpers ---

  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v == null) continue;
      if (k === 'text') el.textContent = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k in el && k !== 'list') el[k] = v;
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids) if (kid != null) el.append(kid);
    return el;
  }

  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }

  function banner(text, bad) {
    const b = $('banner');
    if (!text) { b.hidden = true; return; }
    b.textContent = text;
    b.className = 'banner' + (bad ? ' bad' : '');
    b.hidden = false;
  }

  async function api(method, path, body) {
    const opts = { method, headers: { 'X-Semar-Token': token } };
    // Every change is JSON, even one that says nothing.
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

  function setBusy(on) {
    busy = on;
    $('stage').classList.toggle('busy', on);
    syncControls();
  }

  // --- drawing ---

  function syncControls() {
    const live = !!session && !!frame;
    for (const id of ['refresh', 'reset', 'finish', 'flow-run', 'delete-last']) $(id).disabled = !live || busy;
    $('delete-last').disabled = !live || busy || !(session && session.steps && session.steps.length);
    $('screen-name').disabled = !live || busy;
    $('setup-open').hidden = !(state && !state.hasConfig || state && state.problem);
    $('mode-act').classList.toggle('seg-on', mode === 'act');
    $('mode-assert').classList.toggle('seg-on', mode === 'assert');
    $('mode-act').setAttribute('aria-pressed', String(mode === 'act'));
    $('mode-assert').setAttribute('aria-pressed', String(mode === 'assert'));
    document.body.classList.toggle('assert-mode', mode === 'assert');
  }

  function drawChips() {
    const chips = $('chips');
    clear(chips);
    if (!state) return;
    const add = (t) => chips.append(h('span', { class: 'chip', text: t }));
    add(state.repo);
    if (state.env) add('env ' + state.env);
    if (state.service) add('service ' + state.service);
    if (state.problem) add(state.problem);
    else if (!state.hasConfig) add('no configuration yet');
  }

  function drawDevices() {
    const sel = $('device');
    const keep = sel.value;
    clear(sel);
    sel.append(h('option', { value: '', text: devices.length ? 'Default device' : 'No device found' }));
    for (const d of devices) {
      const label = (d.model ? d.model + ' ' : '') + d.serial + (d.physical ? ' (physical)' : ' (emulator)') + (d.state !== 'device' ? ' ' + d.state : '');
      sel.append(h('option', { value: d.serial, text: label, disabled: d.state !== 'device' }));
    }
    if (keep && devices.some((d) => d.serial === keep)) sel.value = keep;
    drawPhysical();
  }

  function selectedDevice() {
    return devices.find((d) => d.serial === $('device').value) || (devices.length === 1 ? devices[0] : null);
  }

  function drawPhysical() {
    const d = selectedDevice();
    $('physical').hidden = !(d && d.physical) && !(session && session.physicalDevice);
  }

  function drawServices() {
    const services = (state && state.services) || [];
    const row = $('start-service-row');
    const sel = $('start-service');
    const keep = sel.value || (state && state.service) || '';
    clear(sel);
    for (const s of services) sel.append(h('option', { value: s, text: s }));
    if (keep && services.includes(keep)) sel.value = keep;
    row.hidden = services.length <= 1;
  }

  function drawFlows() {
    const flows = (state && state.flows) || [];
    for (const id of ['flow-pick', 'start-flow']) {
      const sel = $(id);
      const keep = sel.value;
      clear(sel);
      sel.append(h('option', { value: '', text: id === 'start-flow' ? 'No flow' : flows.length ? 'Run an existing flow (use:)' : 'No flows saved yet' }));
      for (const f of flows) sel.append(h('option', { value: f, text: f }));
      if (flows.includes(keep)) sel.value = keep;
    }
  }

  function drawMirror() {
    const live = !!session && !!frame;
    $('no-token').hidden = !!token;
    $('start-panel').hidden = !token || live;
    $('stage-wrap').hidden = !live;
    if (!live) {
      const ready = state && state.hasConfig && !state.problem;
      $('start').disabled = !ready || busy;
      $('start-note').textContent = ready
        ? 'The app is reset first, so a replay starts where the recording does.'
        : (state && state.problem) || 'Use the guided setup to add the app first.';
      return;
    }
    const stage = $('stage');
    stage.style.aspectRatio = frame.width + ' / ' + frame.height;
    const img = $('shot');
    if (frame.image) { img.src = frame.image; shotURL = frame.image; }
    else if (frame.imageHidden) shotURL = '';
    if (!frame.imageHidden && shotURL) img.src = shotURL;
    img.hidden = frame.imageHidden || !shotURL;
    $('shot-hidden').hidden = !frame.imageHidden;
    drawOverlay();
    drawTools();
    const name = $('screen-name');
    if (document.activeElement !== name) name.value = frame.screen || '';
  }

  const SVGNS = 'http://www.w3.org/2000/svg';

  function drawOverlay() {
    const svg = $('overlay');
    clear(svg);
    svg.setAttribute('viewBox', '0 0 ' + frame.width + ' ' + frame.height);
    svg.setAttribute('preserveAspectRatio', 'none');
    // A click on no element is a click on a point of the screen.
    svg.onclick = (ev) => {
      if (ev.target !== svg) return;
      const r = svg.getBoundingClientRect();
      const x = Math.round((ev.clientX - r.left) / r.width * frame.width);
      const y = Math.round((ev.clientY - r.top) / r.height * frame.height);
      onPick({ point: { x, y } }, ev);
    };
    const els = (frame.elements || []).slice();
    // The smallest is drawn last, so that it is on top and is the one that is under the pointer.
    els.sort((a, b) => area(b) - area(a));
    for (const e of els) {
      const b = e.bounds;
      const r = document.createElementNS(SVGNS, 'rect');
      r.setAttribute('x', b.Left);
      r.setAttribute('y', b.Top);
      r.setAttribute('width', Math.max(b.Right - b.Left, 1));
      r.setAttribute('height', Math.max(b.Bottom - b.Top, 1));
      r.addEventListener('mouseenter', () => showTip(e, r));
      r.addEventListener('mouseleave', () => { $('tip').hidden = true; r.classList.remove('on'); });
      r.addEventListener('click', (ev) => { ev.stopPropagation(); onPick({ element: e }, ev); });
      svg.append(r);
    }
  }

  function area(e) { return Math.max(e.bounds.Right - e.bounds.Left, 0) * Math.max(e.bounds.Bottom - e.bounds.Top, 0); }

  function describeLocator(l) {
    if (!l) return 'no locator finds it alone';
    const parts = [];
    for (const k of ['id', 'desc', 'role', 'name', 'text', 'xpath', 'class', 'within']) if (l[k]) parts.push(k + ': ' + l[k]);
    return parts.join(', ') || 'a locator';
  }

  function showTip(e, rect) {
    rect.classList.add('on');
    const tip = $('tip');
    clear(tip);
    const label = e.text || e.desc || e.hint || e.id || e.class;
    tip.append(h('b', { text: label }), document.createTextNode(' '));
    if (e.locator) {
      tip.append(h('span', { class: 'badge ' + e.stability, text: e.stability }), document.createTextNode(' 1 match'));
    } else {
      tip.append(h('span', { class: 'badge weak', text: 'no locator' }));
    }
    tip.append(h('div', { text: describeLocator(e.locator) }));
    tip.hidden = false;
  }

  function drawTools() {
    const tools = $('tools');
    if (tools.childElementCount) return;
    const press = (label, key) => h('button', { type: 'button', class: 'btn', text: label, onclick: () => act({ do: 'press', value: key }) });
    const swipe = (label, dir) => h('button', { type: 'button', class: 'btn', text: label, onclick: () => act({ do: 'swipe', value: dir }) });
    tools.append(
      press('Back', 'back'), press('Enter', 'enter'), press('Hide keyboard', 'hide-keyboard'),
      swipe('Swipe up', 'up'), swipe('Swipe down', 'down'), swipe('Swipe left', 'left'), swipe('Swipe right', 'right'),
      h('button', { type: 'button', class: 'btn', text: 'Open link', onclick: () => $('dlg-link').showModal() }),
      h('button', { type: 'button', class: 'btn', text: 'Wait', onclick: () => act({ do: 'wait' }) }),
    );
  }

  function drawSteps() {
    const ol = $('steps');
    clear(ol);
    const steps = (session && session.steps) || [];
    $('steps-empty').hidden = steps.length > 0;
    steps.forEach((st, i) => {
      const li = h('li', { class: 'step' });
      li.append(h('div', { class: 'step-head' },
        h('span', { class: 'step-n', text: String(st.n) }),
        h('span', { class: 'step-kind', text: st.kind }),
        h('span', { class: 'step-text', text: st.text }),
        h('button', { type: 'button', class: 'btn', text: editing === i ? 'Close' : 'Edit', onclick: () => { editing = editing === i ? -1 : i; drawSteps(); } }),
      ));
      if (editing === i) li.append(stepEditor(i, st));
      ol.append(li);
    });
  }

  function stepEditor(i, st) {
    const id = h('input', { type: 'text', value: st.id, 'aria-label': 'Step ID' });
    const screen = h('input', { type: 'text', value: st.screen || '', 'aria-label': 'Screen name' });
    const element = h('input', { type: 'text', placeholder: 'Element name', 'aria-label': 'Element name' });
    const secret = h('input', { type: 'text', placeholder: 'NAME: make the typed text a secret', 'aria-label': 'Secret name' });
    const form = h('div', { class: 'step-edit' },
      h('label', { class: 'field', text: 'Step ID' }), h('label', { class: 'field', text: 'Screen' }), id, screen,
      h('label', { class: 'field wide', text: 'Element name' }), h('div', { class: 'wide' }, element),
    );
    if (st.kind === 'type') form.append(h('div', { class: 'wide' }, secret), h('p', { class: 'hint wide', text: 'A literal text becomes a secret: the text is erased from the recording and kept in memory for this session.' }));
    form.append(h('button', { type: 'button', class: 'btn primary wide', text: 'Save changes', onclick: async () => {
      const body = {};
      if (id.value && id.value !== st.id) body.id = id.value;
      if (screen.value && screen.value !== (st.screen || '')) body.screen = screen.value;
      if (element.value) body.element = element.value;
      if (secret.value) body.secretEnv = secret.value;
      if (!Object.keys(body).length) { editing = -1; drawSteps(); return; }
      const r = await api('PATCH', '/api/timeline/' + i, body);
      if (!r.ok) { banner(r.data.error, true); return; }
      banner('');
      editing = -1;
      take(r.data);
    } }));
    return form;
  }

  function render() {
    drawChips();
    drawServices();
    drawFlows();
    drawPhysical();
    drawMirror();
    drawSteps();
    syncControls();
  }

  // take adopts an answer that holds the recording.
  function take(data) {
    session = Object.assign({}, session, data);
    if (data.frame) frame = data.frame;
    if (data.flow) banner('The flow ' + data.flow + '.');
    render();
  }

  // --- calls ---

  async function load() {
    const r = await api('GET', '/api/state');
    if (!r.ok) { banner(r.data.error, true); return; }
    state = r.data;
    if (state.session && !session) {
      // A page that was reloaded shows the recording that the server holds.
      session = { steps: state.session.steps, kind: state.session.kind, id: state.session.id, physicalDevice: state.physicalDevice };
      const f = await api('POST', '/api/session/refresh', {});
      if (f.ok) take(f.data);
    }
    render();
  }

  async function loadDevices() {
    const r = await api('GET', '/api/devices');
    if (!r.ok) return;
    devices = r.data.devices || [];
    foreground = null;
    for (const d of devices) if (d.foreground && d.foreground.package) { foreground = d.foreground; break; }
    if (r.data.error) banner('Devices: ' + r.data.error, true);
    drawDevices();
  }

  // run sends a call that changes the recording, and handles the answers that every one can get.
  async function run(method, path, body, retry) {
    setBusy(true);
    banner('');
    const r = await api(method, path, body);
    setBusy(false);
    if (r.ok) { take(r.data); return r; }
    if (r.status === 409 && r.data.stale) {
      take(r.data.session);
      banner('The screen changed, so nothing was done. This is the screen now.');
    } else if (r.status === 428 && r.data.needsConfirm) {
      confirm('Physical device', r.data.error, 'Confirm', () => retry && retry(true));
    } else {
      banner(r.data.error || 'The call failed.', true);
    }
    return r;
  }

  function act(a) {
    if (!frame) return Promise.resolve();
    return run('POST', '/api/session/act', Object.assign({ frameId: frame.frameId }, a));
  }

  function onPick(pick, ev) {
    if (busy || !frame) return;
    const t = pick.element ? { index: pick.element.index } : pick.point;
    if (mode === 'assert' || ev.altKey) {
      if (pick.element) openAssert(pick.element, t); else banner('Pick an element to check.');
      return;
    }
    const e = pick.element;
    if (e && (e.editable || e.password)) { openType(e, t); return; }
    act(Object.assign({ do: ev.shiftKey ? 'longPress' : 'tap' }, t));
  }

  // --- dialogs ---

  function closeOn(id) { const d = $(id); if (d.open) d.close(); }

  function confirm(title, text, ok, fn) {
    $('confirm-title').textContent = title;
    $('confirm-text').textContent = text;
    $('confirm-ok').textContent = ok;
    $('form-confirm').onsubmit = (ev) => { ev.preventDefault(); closeOn('dlg-confirm'); fn(); };
    $('dlg-confirm').showModal();
  }

  function secretName(e) {
    const base = (e.hint || e.id || e.desc || 'FIELD').split(/[:/]/).pop().toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '');
    return 'APP_' + (base || 'FIELD');
  }

  function openType(e, t) {
    const value = $('type-value');
    const secret = $('type-secret');
    const env = $('type-env');
    value.value = '';
    value.type = e.password ? 'password' : 'text';
    secret.checked = !!e.password;
    secret.disabled = !!e.password;
    env.value = secretName(e);
    $('type-title').textContent = 'Type in ' + (e.hint || e.desc || e.id || 'the field');
    const syncHint = () => {
      $('type-hint').textContent = e.password ? 'A password field takes a secret. The text is never saved: the files hold env:' + (env.value || 'NAME') + '.' : '';
    };
    env.oninput = syncHint;
    syncHint();
    const sync = () => { $('type-env-row').hidden = !secret.checked; value.type = secret.checked ? 'password' : 'text'; };
    secret.onchange = sync;
    sync();
    $('form-type').onsubmit = async (ev) => {
      ev.preventDefault();
      const body = Object.assign({ do: 'type' }, t);
      if (secret.checked) { body.secretEnv = env.value; body.ephemeralSecret = value.value; } else body.value = value.value;
      value.value = '';
      closeOn('dlg-type');
      await act(body);
    };
    $('dlg-type').showModal();
    value.focus();
  }

  function openAssert(e, t) {
    $('assert-what').textContent = 'On: ' + (e.text || e.desc || e.hint || e.id || e.class);
    $('assert-value').value = e.text || '';
    $('assert-attr').value = '';
    $('assert-error').hidden = true;
    const kind = $('assert-kind');
    kind.value = e.text ? 'equals' : 'visible';
    const sync = () => {
      const k = kind.value;
      $('assert-value-row').hidden = !['equals', 'contains', 'count', 'attribute'].includes(k);
      $('assert-attr-row').hidden = k !== 'attribute';
    };
    kind.onchange = sync;
    sync();
    $('form-assert').onsubmit = async (ev) => {
      ev.preventDefault();
      const body = Object.assign({ frameId: frame.frameId, kind: kind.value, value: $('assert-value-row').hidden ? '' : $('assert-value').value, attribute: $('assert-attr-row').hidden ? '' : $('assert-attr').value }, t);
      setBusy(true);
      const r = await api('POST', '/api/session/assert', body);
      setBusy(false);
      if (r.ok) { closeOn('dlg-assert'); take(r.data); banner('Check added.'); return; }
      if (r.status === 409 && r.data.stale) { closeOn('dlg-assert'); take(r.data.session); banner('The screen changed, so nothing was done. This is the screen now.'); return; }
      $('assert-error').textContent = r.data.error || 'The check was not added.';
      $('assert-error').hidden = false;
    };
    $('dlg-assert').showModal();
  }

  async function openSetup() {
    $('su-error').hidden = true;
    if (foreground && foreground.package) {
      $('su-package').value = foreground.package;
      if (!$('su-name').value) $('su-name').value = foreground.package.split('.').pop();
    }
    const list = $('su-packages');
    clear(list);
    for (const p of (foreground && foreground.candidates) || []) list.append(h('option', { value: p }));
    $('dlg-setup').showModal();
  }

  // --- finish ---

  function finishKind() {
    return document.querySelector('input[name="fin-kind"]:checked').value;
  }

  function syncFinishForm() {
    const flow = finishKind() === 'flow';
    $('fin-ticket-row').hidden = flow;
    $('fin-title-row').hidden = flow;
  }

  function openFinish() {
    finished = null;
    $('fin-error').hidden = true;
    $('fin-result').hidden = true;
    $('fin-save').hidden = false;
    for (const id of ['fin-undo', 'fin-verify-btn', 'fin-next']) $(id).hidden = true;
    const kind = (session && session.kind) || 'scenario';
    document.querySelector('input[name="fin-kind"][value="' + kind + '"]').checked = true;
    if (session && session.id && session.id !== 'recording') $('fin-id').value = session.id;
    syncFinishForm();
    $('dlg-finish').showModal();
  }

  async function save(confirmClear) {
    const body = {
      kind: finishKind(), id: $('fin-id').value, ticket: $('fin-ticket').value, title: $('fin-title').value,
      replace: $('fin-replace').checked, confirmClear: !!confirmClear || $('fin-clear').checked,
    };
    setBusy(true);
    const r = await api('POST', '/api/finish', body);
    setBusy(false);
    const err = $('fin-error');
    if (!r.ok) {
      err.textContent = r.data.error || 'Nothing was saved.';
      err.hidden = false;
      $('fin-clear-row').hidden = !r.data.needsConfirm;
      if (r.data.exists) $('fin-replace').checked = false;
      return;
    }
    err.hidden = true;
    finished = r.data;
    session = Object.assign({}, session, r.data);
    showSaved();
    drawSteps();
  }

  function showSaved() {
    $('fin-result').hidden = false;
    $('fin-save').hidden = true;
    for (const id of ['fin-undo', 'fin-verify-btn', 'fin-next']) $(id).hidden = false;
    const tabs = $('fin-tabs');
    clear(tabs);
    const files = finished.files || [];
    const show = (f) => {
      $('fin-preview').textContent = (finished.preview && finished.preview[f]) || '';
      for (const b of tabs.children) b.setAttribute('aria-pressed', String(b.dataset.file === f));
    };
    for (const f of files) tabs.append(h('button', { type: 'button', class: 'btn', text: f, 'data-file': f, onclick: () => show(f) }));
    if (files.length) show(files.find((f) => f.includes('/flows/') || f.endsWith('.feature')) || files[0]);
    const notes = $('fin-verify');
    clear(notes);
    for (const n of finished.notes || []) notes.append(h('p', { class: 'hint', text: 'Note: ' + n + '.' }));
  }

  async function verify() {
    setBusy(true);
    const box = $('fin-verify');
    box.append(h('p', { class: 'hint', text: 'Verifying on a clean device session...' }));
    const r = await api('POST', '/api/verify', {});
    setBusy(false);
    if (!r.ok) { box.append(h('p', { class: 'error', text: r.data.error || 'The verify failed.' })); return; }
    take(r.data);
    const v = r.data.verify;
    const good = v.status === 'completed' || v.status === 'passed';
    const out = h('div', { class: 'verify' }, h('b', { class: good ? 'ok' : 'bad', text: v.status }), document.createTextNode(' ' + v.message));
    if (v.steps && v.steps.length) {
      const ul = h('ul');
      for (const s of v.steps) ul.append(h('li', { text: s.name + ': ' + s.status + (s.detail ? ' (' + s.detail + ')' : '') }));
      out.append(ul);
    }
    if (v.report) out.append(h('div', { class: 'hint', text: 'Report: ' + v.report }));
    box.append(out);
  }

  async function undo() {
    const r = await api('POST', '/api/undo', {});
    const box = $('fin-verify');
    if (!r.ok) { box.append(h('p', { class: 'error', text: r.data.error })); return; }
    take(r.data);
    clear(box);
    box.append(h('p', { class: 'hint', text: 'Undone: the files are as they were before the save.' }));
    $('fin-result').hidden = true;
    $('fin-save').hidden = false;
    for (const id of ['fin-undo', 'fin-verify-btn', 'fin-next']) $(id).hidden = true;
  }

  async function nextActivity() {
    closeOn('dlg-finish');
    const use = $('start-flow').value || (finishKind() === 'flow' ? $('fin-id').value : '');
    await next(use);
  }

  async function next(use) {
    editing = -1;
    const body = { kind: 'scenario', useFlows: use ? [use] : [] };
    const r = await run('POST', '/api/session/reset', body);
    if (r.ok) { $('fin-id').value = ''; await refreshState(); }
  }

  async function refreshState() {
    const r = await api('GET', '/api/state');
    if (r.ok) { state = r.data; render(); }
  }

  // --- wiring ---

  function boot() {
    const m = /(?:^|[#&])token=([0-9a-f]+)/.exec(location.hash);
    if (m) {
      token = m[1];
      history.replaceState(null, '', location.pathname + location.search);
    }
    for (const b of document.querySelectorAll('[data-close]')) b.addEventListener('click', () => closeOn(b.dataset.close));
    $('mode-act').onclick = () => { mode = 'act'; syncControls(); };
    $('mode-assert').onclick = () => { mode = 'assert'; syncControls(); };
    $('device').onchange = drawPhysical;
    $('setup-open').onclick = openSetup;
    $('refresh').onclick = () => run('POST', '/api/session/refresh', {});
    $('finish').onclick = openFinish;
    $('reset').onclick = () => {
      const steps = (session && session.steps) || [];
      if (!steps.length) { next($('flow-pick').value); return; }
      confirm('Reset the recording', 'The ' + steps.length + ' recorded steps are dropped and the app is reset. Save first to keep them.', 'Reset', () => next($('flow-pick').value));
    };
    $('delete-last').onclick = () => run('DELETE', '/api/timeline/last');
    $('flow-run').onclick = () => {
      const flow = $('flow-pick').value;
      if (!flow) { banner('Pick a flow to run.'); return; }
      const with_ = {};
      for (const part of $('flow-with').value.split(',')) {
        const [k, ...v] = part.split('=');
        if (k.trim()) with_[k.trim()] = v.join('=').trim();
      }
      run('POST', '/api/session/use-flow', { flow, with: with_ });
    };
    $('screen-name').onchange = () => {
      const name = $('screen-name').value.trim();
      if (name) run('POST', '/api/session/screen', { name });
    };
    $('start').onclick = function start(confirmClear) {
      const service = $('start-service').value || (state && state.service) || '';
      const body = { kind: 'scenario', service, deviceSerial: $('device').value, useFlows: $('start-flow').value ? [$('start-flow').value] : [], confirmClear: confirmClear === true };
      run('POST', '/api/session/start', body, (yes) => start(yes)).then(refreshState);
    };
    $('form-link').onsubmit = (ev) => {
      ev.preventDefault();
      const link = $('link-value').value.trim();
      closeOn('dlg-link');
      if (link) act({ do: 'open', value: link });
    };
    $('form-setup').onsubmit = async (ev) => {
      ev.preventDefault();
      const r = await api('POST', '/api/scaffold', {
        name: $('su-name').value, package: $('su-package').value, baseUrl: $('su-base').value, apk: $('su-apk').value, reset: $('su-reset').value, environment: '',
      });
      if (!r.ok) { $('su-error').textContent = r.data.error; $('su-error').hidden = false; return; }
      closeOn('dlg-setup');
      banner('Added the app to ' + r.data.path + '.');
      await refreshState();
    };
    $('form-finish').onsubmit = (ev) => { ev.preventDefault(); save(false); };
    for (const r of document.querySelectorAll('input[name="fin-kind"]')) r.addEventListener('change', syncFinishForm);
    $('fin-undo').onclick = undo;
    $('fin-verify-btn').onclick = verify;
    $('fin-next').onclick = nextActivity;
    $('form-confirm').onsubmit = (ev) => ev.preventDefault();
    if (!token) { render(); return; }
    load().then(loadDevices);
  }

  boot();
})();
