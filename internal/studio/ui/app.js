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
  let captures = []; // what the capture mode took in this page, for a look: {name, value}
  let chosen = null; // the locator that the person picked in the panel: {k, frameId, index}, for the next call on that element
  let repicking = -1; // the step whose element the next click on the screen replaces

  // --- small helpers ---

  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || (v === false && k !== 'spellcheck')) continue;
      if (k === 'text') el.textContent = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (k in el && k !== 'list') el[k] = v;
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids) if (kid != null) el.append(kid);
    return el;
  }

  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }

  function plural(n, word) { return n + ' ' + word + (n === 1 ? '' : 's'); }

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

  function actLabel(a) {
    if (!a || !a.do) return 'Working…';
    switch (a.do) {
      case 'tap': return 'Tapping…';
      case 'longPress': return 'Long pressing…';
      case 'doubleTap': return 'Double-tapping…';
      case 'type': return 'Typing…';
      case 'swipe': return 'Scrolling…';
      case 'press': return 'Pressing key…';
      case 'open': return 'Opening link…';
      case 'wait': return 'Waiting…';
      default: return 'Working…';
    }
  }

  let slowTimer = 0;

  function setBusy(on, label) {
    busy = on;
    const stage = $('stage');
    stage.classList.toggle('busy', on);
    // The picture dims and the pill shows only when the device takes long: a quick action must not flash.
    clearTimeout(slowTimer);
    const pill = $('busy-pill');
    const slow = (yes) => {
      stage.classList.toggle('slow', yes);
      if (pill) pill.hidden = !yes;
    };
    if (pill && on) $('busy-text').textContent = label || 'Working…';
    // A check reads the screen once and is back in a moment: it waits like a quick action does.
    if (on && !quick && label !== 'Adding check…') slow(true);
    else if (on) slowTimer = setTimeout(() => slow(true), 900);
    else slow(false);
    const startBtn = $('start');
    if (startBtn) startBtn.textContent = on && label === 'Starting app…' ? label : 'Start recording';
    const openBtn = $('open-existing');
    if (openBtn) openBtn.textContent = on && label === 'Reopening recording…' ? label : 'Open and replay';
    syncControls();
  }

  function syncStartKind(kind) {
    const k = kind === 'flow' ? 'flow' : 'scenario';
    $('start-kind').value = k;
    for (const btn of document.querySelectorAll('.kind-card')) {
      const on = btn.dataset.kind === k;
      btn.classList.toggle('kind-card-on', on);
      btn.setAttribute('aria-pressed', String(on));
    }
  }

  // --- drawing ---

  function drawModeHelper() {
    const el = $('mode-helper');
    if (!el) return;
    if (mode === 'assert') {
      el.className = 'mode-helper mode-assert';
      el.textContent = 'Click any text or element on screen to add a verification check (required for scenarios).';
    } else if (mode === 'capture') {
      el.className = 'mode-helper mode-capture';
      el.textContent = 'Click text on screen (like an order ID) to save it as {{name}} for later steps.';
    } else {
      el.className = 'mode-helper mode-act';
      el.textContent = 'Click any button or field on the phone to record a tap or typing. Shift+click = long press · Right-click = pick locator · Mouse wheel = scroll.';
    }
  }

  function drawReadiness() {
    const box = $('readiness');
    if (!box) return;
    const live = !!session && !!frame;
    box.hidden = !live;
    if (!live) return;
    clear(box);
    const steps = (session && session.steps) || [];
    const checks = steps.filter((s) => s.kind === 'assert').length;
    const actions = steps.length - checks;
    const isFlow = session && session.kind === 'flow';
    if (isFlow) {
      if (steps.length === 0) {
        box.className = 'readiness neutral';
        box.append(h('span', { class: 'readiness-msg', text: 'Click the phone to record your first step' }));
      } else {
        box.className = 'readiness ok';
        box.append(h('span', { class: 'readiness-msg', text: plural(steps.length, 'step') + ' · Ready to save as a reusable flow' }));
      }
      return;
    }
    if (checks === 0) {
      box.className = 'readiness warn';
      const msg = actions > 0
        ? plural(actions, 'action') + ' · Add at least 1 Check (Assert) before saving'
        : 'Click the phone to record, then add at least 1 Check (Assert) before saving';
      box.append(h('span', { class: 'readiness-msg', text: msg }));
      if (mode !== 'assert') {
        box.append(h('button', { type: 'button', class: 'btn readiness-btn', text: 'Switch to Check mode', onclick: () => { mode = 'assert'; syncControls(); } }));
      }
      return;
    }
    box.className = 'readiness ok';
    box.append(h('span', { class: 'readiness-msg', text: 'Ready to save · ' + plural(actions, 'action') + ', ' + plural(checks, 'check') }));
  }

  function syncControls() {
    const live = !!session && !!frame;
    $('rec-controls').hidden = !live;
    $('side').hidden = !live;
    $('work').classList.toggle('is-idle', !live);
    for (const id of ['refresh', 'reset', 'finish', 'flow-run', 'delete-last']) $(id).disabled = !live || busy;
    $('delete-last').disabled = !live || busy || !(session && session.steps && session.steps.length);
    for (const btn of $('tools').querySelectorAll('button')) btn.disabled = !live || (busy && !(quick && btn.dataset.quick));
    $('screen-name').disabled = !live || busy;
    $('setup-open').hidden = !(state && !state.hasConfig || state && state.problem);
    $('mode-act').classList.toggle('seg-on', mode === 'act');
    $('mode-assert').classList.toggle('seg-on', mode === 'assert');
    $('mode-capture').classList.toggle('seg-on', mode === 'capture');
    $('mode-act').setAttribute('aria-pressed', String(mode === 'act'));
    $('mode-assert').setAttribute('aria-pressed', String(mode === 'assert'));
    $('mode-capture').setAttribute('aria-pressed', String(mode === 'capture'));
    document.body.classList.toggle('assert-mode', mode === 'assert');
    document.body.classList.toggle('capture-mode', mode === 'capture');
    $('screen-rename').disabled = !live || busy;
    $('dirty-note').hidden = !(session && session.dirty);
    $('repick-note').hidden = repicking < 0;
    $('open-existing').disabled = !$('open-pick').value || busy;
    const ready = state && state.hasConfig && !state.problem;
    if (!live) $('start').disabled = !ready || busy;
    drawModeHelper();
    drawReadiness();
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
    $('start-flow-row').hidden = flows.length === 0;
    for (const id of ['flow-pick', 'start-flow']) {
      const sel = $(id);
      const keep = sel.value;
      clear(sel);
      sel.append(h('option', { value: '', text: id === 'start-flow' ? 'Start fresh (no flow)' : flows.length ? 'Run an existing flow (use:)' : 'No flows saved yet' }));
      for (const f of flows) sel.append(h('option', { value: f, text: f }));
      if (flows.includes(keep)) sel.value = keep;
    }
  }

  function drawRecordings() {
    const list = (state && state.recordings) || [];
    $('open-details').hidden = list.length === 0;
    const sel = $('open-pick');
    const keep = sel.value;
    clear(sel);
    sel.append(h('option', { value: '', text: list.length ? 'Choose a saved flow or scenario…' : 'Nothing saved to open yet' }));
    for (const r of list) sel.append(h('option', { value: r.kind + ':' + r.id, text: r.kind + ' ' + r.id + (r.title ? ' (' + r.title + ')' : '') }));
    if (list.some((r) => r.kind + ':' + r.id === keep)) sel.value = keep;
    $('open-existing').disabled = !sel.value || busy;
    $('open-at').disabled = !sel.value;
  }

  function drawScreenNames() {
    const dl = $('screen-names');
    clear(dl);
    for (const n of (session && session.screenNames) || []) dl.append(h('option', { value: n }));
  }

  function drawCaptured() {
    const box = $('captured');
    const ul = $('captured-list');
    clear(ul);
    box.hidden = captures.length === 0;
    for (const c of captures) ul.append(h('li', {}, h('code', { text: '{{' + c.name + '}}' }), document.createTextNode(' = ' + c.value)));
  }

  function drawMirror() {
    const live = !!session && !!frame;
    $('no-token').hidden = !!token;
    $('start-panel').hidden = !token || live;
    $('stage-wrap').hidden = !live;
    if (!live) {
      drawRecordings();
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
    drawScreenNames();
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
      r.addEventListener('contextmenu', (ev) => { ev.preventDefault(); ev.stopPropagation(); openCandidates(e); });
      svg.append(r);
    }
  }

  function area(e) { return Math.max(e.bounds.Right - e.bounds.Left, 0) * Math.max(e.bounds.Bottom - e.bounds.Top, 0); }

  function describeLocator(l) {
    if (!l) return 'no locator finds it alone';
    const parts = [];
    for (const k of ['id', 'desc', 'role', 'name', 'text', 'hasText', 'index', 'xpath', 'class', 'within']) if (l[k]) parts.push(k + ': ' + l[k]);
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
    if (e.warning) tip.append(h('div', { class: 'warning', text: e.warning }));
    tip.append(h('div', { class: 'hint', text: 'Right-click to see every locator and pick one.' }));
    tip.hidden = false;
  }

  function drawTools() {
    const tools = $('tools');
    if (tools.childElementCount) return;
    const press = (label, key) => h('button', { type: 'button', class: 'btn tool-btn', text: label, 'data-quick': '1', onclick: () => quickAct({ do: 'press', value: key }) });
    const swipe = (label, dir) => h('button', { type: 'button', class: 'btn tool-btn', text: label, 'data-quick': '1', onclick: () => quickAct({ do: 'swipe', value: dir }) });
    const group = (title, ...btns) => h('div', { class: 'tool-group' },
      h('span', { class: 'tool-label', text: title }),
      h('div', { class: 'tool-row' }, ...btns),
    );
    tools.append(
      group('Keys', press('← Back', 'back'), press('↵ Enter', 'enter'), press('Hide keyboard', 'hide-keyboard')),
      // The buttons say where the screen goes, as a scroll bar does. The finger moves the other way.
      group('Scroll', swipe('↑ Up', 'down'), swipe('↓ Down', 'up'), swipe('← Left', 'right'), swipe('→ Right', 'left')),
      group('More',
        h('button', { type: 'button', class: 'btn tool-btn', text: 'Deep link', onclick: () => $('dlg-link').showModal() }),
        h('button', { type: 'button', class: 'btn tool-btn', text: 'Wait', onclick: () => act({ do: 'wait' }) }),
      ),
    );
  }

  function drawSteps() {
    const ol = $('steps');
    clear(ol);
    const steps = (session && session.steps) || [];
    $('steps-empty').hidden = steps.length > 0;
    const cursor = session && typeof session.cursor === 'number' ? session.cursor : steps.length;
    const marker = () => h('li', { class: 'cursor-mark', text: 'recording here' });
    if (steps.length && cursor === 0) ol.append(marker());
    steps.forEach((st, i) => {
      const li = h('li', { class: 'step' + (st.pending ? ' pending' : '') });
      li.append(h('div', { class: 'step-head' },
        h('span', { class: 'step-n', text: String(st.n) }),
        h('span', { class: 'step-kind', text: st.kind }),
        h('span', { class: 'step-text', text: st.text }),
      ));
      if (st.locator) {
        li.append(h('div', { class: 'step-loc' },
          h('code', { text: st.locator }),
          st.stability ? h('span', { class: 'badge ' + st.stability, text: st.stability }) : null,
          st.shared ? h('span', { class: 'badge strong', text: 'shared' }) : null,
        ));
      }
      li.append(h('div', { class: 'step-actions' },
        h('button', { type: 'button', class: 'btn', text: 'Go here', title: 'Reset the app and take the device through the steps up to this one', disabled: busy, onclick: () => gotoStep(st.n) }),
        st.locator ? h('button', { type: 'button', class: 'btn', text: 'Locator', disabled: busy, onclick: () => openStepLocators(i) }) : null,
        h('button', { type: 'button', class: 'btn', text: editing === i ? 'Close' : 'Edit', onclick: () => { editing = editing === i ? -1 : i; drawSteps(); } }),
        h('button', { type: 'button', class: 'btn danger', text: 'Delete', disabled: busy, onclick: () => deleteStep(i) }),
      ));
      if (editing === i) li.append(stepEditor(i, st));
      ol.append(li);
      if (cursor === st.n && st.n < steps.length) ol.append(marker());
    });
  }

  function deleteStep(i) {
    if (busy) return Promise.resolve();
    // The steps shift, so no edit or repick that points at a number is still the same one.
    repicking = -1;
    editing = -1;
    closeCandidates();
    return run('DELETE', '/api/timeline/' + i, undefined, undefined, 'Deleting step…');
  }

  // gotoStep resets the app and takes the device through the first n steps. A step that needs a secret or a
  // param that no file holds asks for it: the value is sent once and kept in memory by the server only.
  async function gotoStep(n, values, confirmClear) {
    if (busy) return;
    setBusy(true, 'Replaying steps…');
    banner('');
    const r = await api('POST', '/api/timeline/goto', Object.assign({ n }, values || {}, confirmClear ? { confirmClear: true } : {}));
    setBusy(false);
    if (r.ok) { take(r.data); banner('The device is at step ' + n + '. New steps go after it.'); return r; }
    if (r.status === 428 && r.data.needsConfirm) {
      // Any value that was sent is kept by the server, so the call goes again without it.
      confirm('Physical device', r.data.error, 'Confirm', () => gotoStep(n, null, true));
    } else if (r.status === 428 && (r.data.needsSecrets || r.data.needsParams)) {
      askValues(r.data, (v) => gotoStep(n, v, confirmClear));
    } else if (r.data.session) {
      take(r.data.session);
      banner(r.data.error || 'A step did not go through.', true);
    } else {
      banner(r.data.error || 'The call failed.', true);
    }
    return r;
  }

  // askValues asks for the secrets and the params that a replay lacks. A secret is a password field that is
  // emptied as soon as it is sent.
  function askValues(need, fn) {
    const box = $('values-fields');
    clear(box);
    const inputs = [];
    for (const n of need.needsSecrets || []) {
      const input = h('input', { type: 'password', autocomplete: 'off', spellcheck: false, 'aria-label': 'Secret ' + n });
      inputs.push({ kind: 'secrets', name: n, input });
      box.append(h('label', { class: 'field', text: 'Secret ' + n + ' (memory only, never saved)' }), input);
    }
    for (const n of need.needsParams || []) {
      const input = h('input', { type: 'text', autocomplete: 'off', 'aria-label': 'Param ' + n });
      inputs.push({ kind: 'params', name: n, input });
      box.append(h('label', { class: 'field', text: 'Param ' + n }), input);
    }
    $('form-values').onsubmit = (ev) => {
      ev.preventDefault();
      const v = { secrets: {}, params: {} };
      for (const i of inputs) { v[i.kind][i.name] = i.input.value; i.input.value = ''; }
      clear(box);
      closeOn('dlg-values');
      fn(v);
    };
    $('dlg-values').showModal();
    if (inputs.length) inputs[0].input.focus();
  }

  function stepEditor(i, st) {
    const id = h('input', { type: 'text', value: st.id, 'aria-label': 'Step ID' });
    const screen = h('input', { type: 'text', value: st.screen || '', 'aria-label': 'Screen name' });
    const element = h('input', { type: 'text', placeholder: 'Element name', 'aria-label': 'Element name' });
    const secret = h('input', { type: 'text', placeholder: 'NAME: make the typed text a secret', 'aria-label': 'Secret name' });
    const common = h('input', { type: 'checkbox', checked: !!st.shared, 'aria-label': 'Shared element' });
    const param = h('input', { type: 'text', placeholder: 'name: make the typed text a param', 'aria-label': 'Param name' });
    const required = h('input', { type: 'checkbox', 'aria-label': 'Required param' });
    const form = h('div', { class: 'step-edit' },
      h('label', { class: 'field', text: 'Step ID' }), h('label', { class: 'field', text: 'Screen' }), id, screen,
      h('label', { class: 'field wide', text: 'Element name' }), h('div', { class: 'wide' }, element),
    );
    if (st.locator) {
      form.append(h('label', { class: 'check wide' }, common, document.createTextNode(' Shared element (common): every screen can find it, as a back button or a tab bar')));
    }
    if (st.kind === 'type') form.append(h('div', { class: 'wide' }, secret), h('p', { class: 'hint wide', text: 'A literal text becomes a secret: the text is erased from the recording and kept in memory for this session.' }));
    if (st.kind === 'type' && session && session.kind === 'flow') {
      form.append(
        h('div', { class: 'wide' }, param),
        h('label', { class: 'check wide' }, required, document.createTextNode(' Required: no default, a scenario that uses the flow must give a value')),
        h('p', { class: 'hint wide', text: 'Make parameter: the step types {{name}}, and the param gets the typed text as its default. A secret is never a param.' }),
      );
    }
    form.append(h('button', { type: 'button', class: 'btn primary wide', text: 'Save changes', onclick: async () => {
      const body = {};
      if (id.value && id.value !== st.id) body.id = id.value;
      if (screen.value && screen.value !== (st.screen || '')) body.screen = screen.value;
      if (element.value) body.element = element.value;
      if (secret.value) body.secretEnv = secret.value;
      if (st.locator && common.checked !== !!st.shared) body.common = common.checked;
      if (param.value) { body.param = param.value; body.required = required.checked; }
      if (!Object.keys(body).length) { editing = -1; drawSteps(); return; }
      const r = await api('PATCH', '/api/timeline/' + i, body);
      if (!r.ok) { banner(r.data.error, true); return; }
      banner('');
      editing = -1;
      take(r.data);
    } }));
    return form;
  }

  // --- locators ---

  function closeCandidates() { $('cand-panel').hidden = true; chosen = null; }

  function candRow(info, pick, label) {
    const row = h('li', { class: 'cand' + (info.current ? ' current' : '') + (!info.k ? ' refused' : '') });
    row.append(h('code', { text: info.locator }));
    if (info.stability) row.append(h('span', { class: 'badge ' + info.stability, text: info.stability }));
    // A refused row says so once: the server's note for it is that same text.
    const note = info.k ? (info.matches > 1 ? info.matches + ' matches' : '1 match') : (info.matches || '?') + ' matches (refused)';
    const extra = info.k && info.note ? ', ' + info.note : '';
    row.append(h('span', { class: 'hint', text: note + (info.explicit ? ', by position: use it only on purpose' : '') + extra }));
    if (info.k && pick) row.append(h('button', { type: 'button', class: 'btn', text: info.current ? 'In use' : label, disabled: !!info.current || busy, onclick: () => pick(info.k) }));
    return row;
  }

  // openCandidates lists every locator that finds an element of the frame, with how many elements each one
  // finds, and lets the person pick one for the next action, check, or capture. It is the way out of an
  // element that has no locator of its own, or that has one that is scoped.
  async function openCandidates(e, forDo) {
    if (busy || !frame) return;
    // The mode decides what the locator is for: a field is typed in only when the person acts.
    const op = forDo || (mode === 'assert' || mode === 'capture' ? 'assert' : e.editable || e.password ? 'type' : 'tap');
    chosen = null;
    const r = await api('GET', '/api/session/candidates?frameId=' + frame.frameId + '&index=' + e.index + '&do=' + encodeURIComponent(op));
    if (!r.ok) {
      if (r.status === 409) { await run('POST', '/api/session/refresh', {}, undefined, 'Refreshing screen…'); banner('The screen changed. This is the screen now.'); return; }
      banner(r.data.error || 'The locators could not be listed.', true);
      return;
    }
    const panel = $('cand-panel');
    const list = $('cand-list');
    clear(list);
    $('cand-title').textContent = 'Locators of ' + (e.text || e.desc || e.hint || e.id || e.class);
    const target = { index: e.index };
    const frameId = frame.frameId;
    const pick = (k) => {
      closeCandidates();
      // The choice is for this element of this frame, and no other call takes it.
      chosen = { k, frameId, index: e.index };
      if (mode === 'assert') { openAssert(e, target); return; }
      if (mode === 'capture') { openCapture(e, target); return; }
      if (op === 'type') { openType(e, target); return; }
      act(Object.assign({ do: 'tap' }, target));
    };
    const usable = r.data.usable || [];
    for (const u of usable) list.append(candRow(u, pick, mode === 'act' && op === 'tap' ? 'Tap with this' : 'Use this'));
    for (const u of r.data.refused || []) list.append(candRow(u, null, ''));
    $('cand-note').textContent = usable.length
      ? 'Pick the one to use. A locator that finds several elements is refused by a replay.'
      : 'No locator finds this element alone. A card or a position may be offered above, or the app needs an id or a description.';
    panel.hidden = false;
  }

  // takeCandidate gives the locator that the person picked, once, and only for the element that it was
  // picked for on the frame that was shown then.
  function takeCandidate(t) {
    const c = chosen;
    chosen = null;
    return c && frame && t && c.frameId === frame.frameId && c.index === t.index ? c.k : 0;
  }

  async function openStepLocators(i) {
    if (busy) return;
    chosen = null;
    const r = await api('GET', '/api/timeline/' + i + '/locators');
    if (!r.ok) { banner(r.data.error || 'The locators could not be listed.', true); return; }
    const list = $('cand-list');
    clear(list);
    $('cand-title').textContent = 'Locators of step ' + (i + 1);
    for (const info of r.data.locators || []) {
      list.append(candRow(info, async (k) => {
        const s = await run('POST', '/api/timeline/' + i + '/locator', { choice: k }, undefined, 'Updating locator…');
        if (s.ok) { banner('Step ' + (i + 1) + ' uses that locator. The screens file is not rewritten: the element gets a new name when you save.'); closeCandidates(); }
      }, 'Use this'));
    }
    const sel = h('li', { class: 'cand' }, h('button', { type: 'button', class: 'btn', text: 'Pick another element', onclick: () => {
      repicking = i;
      closeCandidates();
      banner('Click the element on the screen that step ' + (i + 1) + ' is on. ' + (i === 0
        ? 'The device must be before the first step, where the cursor is at the top of the list.'
        : 'The device must be at step ' + i + ': use Go here on step ' + i + ' first.'));
      syncControls();
    } }));
    list.append(sel);
    $('cand-note').textContent = 'The locators that were found when the step was recorded. A change never rewrites an entry of the screens file.';
    $('cand-panel').hidden = false;
  }

  async function repickTo(pick) {
    const i = repicking;
    repicking = -1;
    const body = pick.element ? { frameId: frame.frameId, element: pick.element.index } : Object.assign({ frameId: frame.frameId }, pick.point);
    const r = await run('POST', '/api/timeline/' + i + '/locator', body, undefined, 'Updating locator…');
    if (r.ok) banner('Step ' + (i + 1) + ' now points at that element.');
    syncControls();
  }

  function render() {
    drawChips();
    drawServices();
    drawFlows();
    drawRecordings();
    drawCaptured();
    drawPhysical();
    drawMirror();
    drawSteps();
    syncControls();
  }

  // take adopts an answer that holds the recording.
  function take(data) {
    // The locators of the panel belong to the frame they were listed on: a new frame closes the panel.
    if (data.frame && frame && data.frame.frameId !== frame.frameId && !$('cand-panel').hidden) closeCandidates();
    session = Object.assign({}, session, data);
    // The answer always says whether the device is where the steps say: an old yes must not stay.
    if (Array.isArray(data.steps)) session.dirty = !!data.dirty;
    if (data.frame) frame = data.frame;
    if (data.frame && data.frame.notice) banner(data.frame.notice);
    if (data.dirty) banner('A step that the device had done was deleted, so the device is not where the steps say. Use Go here on a step to take it through the steps that are left.');
    if (data.flow) banner('The flow ' + data.flow + '.');
    if (data.notice) banner(data.notice);
    render();
  }

  // --- calls ---

  async function load() {
    const r = await api('GET', '/api/state');
    if (!r.ok) { banner(r.data.error, true); return; }
    state = r.data;
    if (state.session && !session) {
      // A page that was reloaded shows the recording that the server holds.
      const ss = state.session;
      session = { steps: ss.steps, kind: ss.kind, id: ss.id, cursor: ss.cursor, dirty: ss.dirty, common: ss.common, screenNames: ss.screenNames, physicalDevice: state.physicalDevice };
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
  async function run(method, path, body, retry, label) {
    setBusy(true, label || 'Working…');
    banner('');
    const began = Date.now();
    const r = await api(method, path, body);
    setBusy(false);
    const took = Date.now() - began;
    if (r.ok) {
      take(r.data);
      // The time is the device's: it reads the whole screen at least twice for every action.
      if (path === '/api/session/act' && took > 4000) banner('The device took ' + Math.round(took / 1000) + ' s to read this screen. A screen with a web view or very many elements is slow to read, so every action on it is slow.');
      // Any call that left the device on a screen may have left it on one that is still loading.
      if (r.data && r.data.frame && path !== '/api/session/refresh') watchScreen(took);
      return r;
    }
    if (r.status === 409 && r.data.stale) {
      take(r.data.session);
      banner('The screen changed, so nothing was done. This is the screen now.');
    } else if (r.status === 428 && r.data.needsConfirm) {
      confirm('Physical device', r.data.error, 'Confirm', () => retry && retry(true));
    } else if (r.status === 428 && (r.data.needsSecrets || r.data.needsParams)) {
      askValues(r.data, (v) => retry && retry(v));
    } else {
      banner(r.data.error || 'The call failed.', true);
    }
    return r;
  }

  function act(a) {
    if (!frame) return Promise.resolve();
    const k = takeCandidate(a);
    return run('POST', '/api/session/act', Object.assign({ frameId: frame.frameId }, k ? { candidate: k } : {}, a), undefined, actLabel(a));
  }

  // --- a screen that changes on its own ---
  // An app may go on after the action is done: a page loads, a list fills. The page asks the device
  // twice, a moment after each action, whether the screen is still the one in the picture, and shows the
  // new one when it is not. The server cuts a question short after 1.5 s, because it holds the device
  // and the person may be about to click: on a screen that is slower to read than that, the page stops
  // asking until the next action.
  let peekTimers = [];

  function stopPeeking() {
    for (const t of peekTimers) clearTimeout(t);
    peekTimers = [];
  }

  function watchScreen(took) {
    stopPeeking();
    if (took > 8000) return;
    for (const wait of [700, 2200]) peekTimers.push(setTimeout(peek, wait));
  }

  async function peek() {
    if (busy || !frame || !session || document.querySelector('dialog[open]') || !$('cand-panel').hidden || repicking >= 0) return;
    const id = frame.frameId;
    const r = await api('POST', '/api/session/peek', { frameId: id });
    if (!r.ok || r.data.slow) { stopPeeking(); return; }
    if (!r.data.changed || busy || !frame || frame.frameId !== id || document.querySelector('dialog[open]') || !$('cand-panel').hidden || repicking >= 0) return;
    take(r.data);
    banner('The screen changed on its own, so the picture was updated.');
  }

  // --- quick actions ---
  // A swipe, a key, and a tap show at once that they were taken: the picture moves with the finger, or a
  // ring marks the tap, while the device does it. A swipe or a key that is clicked while another runs
  // waits its turn, so that three clicks are three swipes.
  let quick = false;
  const waiting = [];

  function motion(a) {
    const img = $('shot');
    if (a.do === 'swipe' && !('index' in a) && !('x' in a)) {
      const by = { up: 'translateY(-28%)', down: 'translateY(28%)', left: 'translateX(-40%)', right: 'translateX(40%)' }[a.value];
      if (by) { img.classList.add('moving'); img.style.transform = by; }
    }
  }

  function still() {
    const img = $('shot');
    img.classList.remove('moving');
    img.style.transform = '';
  }

  function ring(ev) {
    const stage = $('stage');
    const r = stage.getBoundingClientRect();
    const dot = h('span', { class: 'tap-ring' });
    dot.style.left = (ev.clientX - r.left) + 'px';
    dot.style.top = (ev.clientY - r.top) + 'px';
    stage.append(dot);
    setTimeout(() => dot.remove(), 600);
  }

  async function quickAct(a) {
    if (!frame) return undefined;
    if (busy) {
      if (quick && waiting.length < 4) waiting.push(a);
      return undefined;
    }
    quick = true;
    stopPeeking();
    motion(a);
    let r;
    try {
      r = await act(a);
      // A swipe or a key acts on no element, so a screen that changed on its own just before does not
      // make it wrong: it is sent once more, on the screen as it is now. A tap is not: it was aimed.
      const plain = !('index' in a) && !('x' in a);
      if (r && r.status === 409 && r.data && r.data.stale && plain && frame) r = await act(a);
    } finally {
      quick = false;
      still();
      syncControls();
    }
    const next = r && r.ok ? waiting.shift() : (waiting.length = 0, undefined);
    if (next) return quickAct(next);
    return r;
  }

  function onPick(pick, ev) {
    if (busy || !frame) return;
    if (repicking >= 0) { repickTo(pick); return; }
    const t = pick.element ? { index: pick.element.index } : pick.point;
    if (mode === 'capture') {
      if (pick.element) openCapture(pick.element, t); else banner('Pick an element to capture.');
      return;
    }
    if (mode === 'assert' || ev.altKey) {
      if (pick.element) openAssert(pick.element, t); else banner('Pick an element to check.');
      return;
    }
    const e = pick.element;
    if (e && (e.editable || e.password)) { openType(e, t); return; }
    const doing = ev.shiftKey ? 'longPress' : 'tap';
    ring(ev);
    quickAct(Object.assign({ do: doing }, t)).then((r) => {
      // No locator finds the element alone: show the ways to find it, so that the person can pick one.
      if (r && r.status === 422 && !r.data.needsSecret && e) openCandidates(e, 'tap');
    });
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
      const keep = chosen;
      const k = takeCandidate(t);
      if (k) body.candidate = k;
      setBusy(true, 'Adding check…');
      const began = Date.now();
      const r = await api('POST', '/api/session/assert', body);
      setBusy(false);
      const took = Date.now() - began;
      if (r.ok) {
        closeOn('dlg-assert');
        take(r.data);
        // A check reads the screen once, to be sure that it is still the one in the picture.
        banner(took > 4000 ? 'Check added. The device took ' + Math.round(took / 1000) + ' s to read this screen: a screen with a web view or very many elements is slow to read.' : 'Check added.');
        return;
      }
      if (r.status === 409 && r.data.stale) { closeOn('dlg-assert'); take(r.data.session); banner('The screen changed, so nothing was done. This is the screen now.'); return; }
      chosen = keep; // the dialog stays open, so a second try keeps the locator that was picked
      $('assert-error').textContent = r.data.error || 'The check was not added.';
      $('assert-error').hidden = false;
    };
    $('dlg-assert').showModal();
  }

  // openCapture asks for the name and the optional regular expression of a value to keep from an element.
  // The preview is what a browser regular expression finds in the text that the element shows: Semar reads
  // the real value, and refuses a pattern that does not match.
  function openCapture(e, t) {
    const text = e.text || e.desc || '';
    const name = $('cap-name');
    const pattern = $('cap-pattern');
    $('cap-what').textContent = 'On: ' + (e.text || e.desc || e.hint || e.id || e.class);
    $('cap-text').textContent = text;
    name.value = '';
    pattern.value = '';
    $('cap-error').hidden = true;
    const preview = () => {
      const out = $('cap-preview');
      if (!pattern.value) { out.textContent = text ? 'Value: ' + text : 'The element shows no text.'; return; }
      try {
        const m = new RegExp(pattern.value).exec(text);
        out.textContent = !m ? 'The pattern does not match this text.' : 'Value: ' + (m.length > 1 ? (m[1] === undefined ? '' : m[1]) : m[0]);
      } catch (err) {
        out.textContent = 'This pattern cannot be previewed here. Semar checks it when you capture.';
      }
    };
    pattern.oninput = preview;
    preview();
    $('form-capture').onsubmit = async (ev) => {
      ev.preventDefault();
      const body = Object.assign({ frameId: frame.frameId, name: name.value, pattern: pattern.value }, t);
      const keep = chosen;
      const k = takeCandidate(t);
      if (k) body.candidate = k;
      setBusy(true, 'Capturing value…');
      const r = await api('POST', '/api/session/capture', body);
      setBusy(false);
      if (r.ok) {
        closeOn('dlg-capture');
        take(r.data);
        if (r.data.captured) {
          captures = captures.filter((c) => c.name !== r.data.captured.name).concat([r.data.captured]);
          drawCaptured();
          banner('Captured {{' + r.data.captured.name + '}} = ' + r.data.captured.value + '. Later steps may type it or check it as {{' + r.data.captured.name + '}}.');
        }
        return;
      }
      if (r.status === 409 && r.data.stale) { closeOn('dlg-capture'); take(r.data.session); banner('The screen changed, so nothing was done. This is the screen now.'); return; }
      chosen = keep; // the dialog stays open, so a second try keeps the locator that was picked
      $('cap-error').textContent = r.data.error || 'The value was not captured.';
      $('cap-error').hidden = false;
    };
    $('dlg-capture').showModal();
    name.focus();
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
    const isFlow = finishKind() === 'flow';
    $('fin-ticket-row').hidden = isFlow;
    $('fin-title-row').hidden = isFlow;
    $('fin-id').placeholder = isFlow ? 'login' : 'SHOP-12-01';
    $('fin-explain').textContent = isFlow
      ? 'Saves a reusable .semar/flows/<name>.yaml building block you can run at the start of any scenario.'
      : 'Saves a runnable test (.feature + script) that checks your app end-to-end.';
  }

  // The finish dialog has two views: the form, and what was saved with the verify under it.
  function finishView(saved) {
    $('fin-form').hidden = saved;
    $('fin-result').hidden = !saved;
    $('fin-save').hidden = saved;
    $('fin-heading').textContent = saved ? 'Saved' : 'Finish and save';
    for (const id of ['fin-undo', 'fin-verify-btn', 'fin-next']) $(id).hidden = !saved;
    finishPrimary(saved ? 'fin-verify-btn' : 'fin-save');
    $('fin-verify-btn').textContent = 'Verify replay';
  }

  // finishPrimary marks the one button that is the next thing to do.
  function finishPrimary(id) {
    for (const b of ['fin-verify-btn', 'fin-next', 'fin-save']) $(b).classList.toggle('primary', b === id);
  }

  function openFinish() {
    finished = null;
    $('fin-error').hidden = true;
    finishView(false);
    const kind = (session && session.kind) || 'scenario';
    document.querySelector('input[name="fin-kind"][value="' + kind + '"]').checked = true;
    if (session && session.id && session.id !== 'recording') $('fin-id').value = session.id;
    syncFinishForm();
    $('dlg-finish').showModal();
  }

  async function save(confirmClear) {
    if (busy) return;
    const body = {
      kind: finishKind(), id: $('fin-id').value, ticket: $('fin-ticket').value, title: $('fin-title').value,
      replace: $('fin-replace').checked, confirmClear: !!confirmClear || $('fin-clear').checked,
    };
    setBusy(true, 'Saving…');
    $('fin-save').disabled = true;
    const r = await api('POST', '/api/finish', body);
    $('fin-save').disabled = false;
    setBusy(false);
    const err = $('fin-error');
    err.className = 'error';
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

  // fileLabel says what a saved file is. The script of a scenario is stored under the hash of what the
  // scenario expects, so its name means nothing to a person: the label does.
  function fileLabel(f) {
    if (f.includes('/flows/')) return 'Flow';
    if (f.endsWith('.feature')) return 'Test steps';
    if (f.includes('/screens/')) return 'Screen elements';
    if (f.includes('/store/scripts/')) return 'Replay script';
    return f.split('/').pop();
  }

  function showSaved() {
    finishView(true);
    const isFlow = finishKind() === 'flow';
    $('fin-done-title').textContent = (isFlow ? 'Flow ' : 'Test ') + $('fin-id').value + ' is saved';
    $('fin-done-sub').textContent = isFlow
      ? 'Next: verify that it replays on a fresh app, or record the next activity.'
      : 'Next: verify that it replays and passes on a fresh app.';
    const tabs = $('fin-tabs');
    clear(tabs);
    const files = finished.files || [];
    $('fin-files').open = false;
    $('fin-files-summary').textContent = 'Files written (' + files.length + ')';
    const show = (f) => {
      $('fin-preview').textContent = (finished.preview && finished.preview[f]) || '';
      $('fin-path').textContent = f;
      for (const b of tabs.children) b.setAttribute('aria-pressed', String(b.dataset.file === f));
    };
    for (const f of files) tabs.append(h('button', { type: 'button', class: 'btn', text: fileLabel(f), title: f, 'data-file': f, onclick: () => show(f) }));
    if (files.length) show(files.find((f) => f.includes('/flows/') || f.endsWith('.feature')) || files[0]);
    const notes = $('fin-verify');
    clear(notes);
    for (const n of finished.notes || []) notes.append(h('p', { class: 'hint', text: 'Note: ' + n + '.' }));
  }

  // Nothing in the dialog can be clicked while the device replays: a second verify would be refused.
  function finishLocked(on) {
    for (const id of ['fin-undo', 'fin-close', 'fin-verify-btn', 'fin-next']) $(id).disabled = on;
  }

  async function verify() {
    if (busy) return;
    const box = $('fin-verify');
    const btn = $('fin-verify-btn');
    for (const old of box.querySelectorAll('.verify, .verify-run, .error')) old.remove();
    const note = h('div', { class: 'verify-run' },
      h('span', { class: 'spinner', 'aria-hidden': 'true' }),
      h('div', {},
        h('div', { class: 'verify-run-title', text: 'Replaying on the device…' }),
        h('div', { class: 'hint', text: 'The app restarts and every step runs again on its own. Leave the device alone until this ends.' })));
    box.append(note);
    finishLocked(true);
    btn.textContent = 'Verifying…';
    setBusy(true, 'Replaying the saved test…');
    const r = await api('POST', '/api/verify', {});
    setBusy(false);
    finishLocked(false);
    btn.textContent = 'Verify again';
    note.remove();
    if (!r.ok) { box.append(h('p', { class: 'error', text: r.data.error || 'The verify failed.' })); return; }
    take(r.data);
    const v = r.data.verify;
    const good = v.status === 'completed' || v.status === 'passed';
    finishPrimary(good ? 'fin-next' : 'fin-verify-btn');
    const out = h('div', { class: 'verify ' + (good ? 'is-ok' : 'is-bad') },
      h('div', { class: 'verify-head' }, h('b', { class: good ? 'ok' : 'bad', text: good ? '✓ Passed' : '✕ ' + v.status })),
      h('div', { class: 'verify-msg', text: v.message }));
    if (v.steps && v.steps.length) {
      const ul = h('ol', { class: 'verify-steps' });
      for (const s of v.steps) {
        const fine = ['done', 'held'].includes(s.status);
        ul.append(h('li', { class: fine ? '' : 'bad' },
          h('span', { class: 'verify-step-mark', text: fine ? '✓' : '✕' }),
          h('span', { text: s.name + (fine && s.status === 'done' ? '' : ': ' + s.status) + (s.detail ? ' (' + s.detail + ')' : '') })));
      }
      out.append(ul);
    }
    if (v.diagnosis && v.diagnosis.kind === 'stale-start') {
      out.append(h('div', { class: 'verify-fix' },
        h('button', { type: 'button', class: 'btn primary', text: 'Reset with clear, save and verify again', onclick: () => fixStart(false) })));
    }
    if (v.report) out.append(h('div', { class: 'hint fin-path', text: 'Full report: ' + v.report }));
    box.append(out);
    out.scrollIntoView({ block: 'nearest' });
  }

  // fixStart is the fix that a verify offers when the replay began on another screen than the recording: it
  // saves again over the files of the last save with the first launch resetting with clear, and verifies.
  async function fixStart(confirmClear) {
    if (busy) return;
    const body = {
      kind: finishKind(), id: $('fin-id').value, ticket: $('fin-ticket').value, title: $('fin-title').value,
      replace: true, confirmClear: !!confirmClear, clearFirstLaunch: true,
    };
    setBusy(true, 'Saving…');
    finishLocked(true);
    const r = await api('POST', '/api/finish', body);
    setBusy(false);
    finishLocked(false);
    if (r.status === 428 && r.data.needsConfirm) {
      confirm('Physical device', r.data.error, 'Confirm', () => fixStart(true));
      return;
    }
    if (!r.ok) { $('fin-verify').append(h('p', { class: 'error', text: r.data.error || 'Nothing was saved.' })); return; }
    finished = r.data;
    take(r.data);
    showSaved();
    drawSteps();
    await verify();
  }

  async function undo() {
    const r = await api('POST', '/api/undo', {});
    const box = $('fin-verify');
    if (!r.ok) { box.append(h('p', { class: 'error', text: r.data.error })); return; }
    take(r.data);
    clear(box);
    finishView(false);
    const err = $('fin-error');
    err.className = 'hint';
    err.textContent = 'Undone: the files are as they were before the save.';
    err.hidden = false;
  }

  async function nextActivity() {
    closeOn('dlg-finish');
    const use = $('start-flow').value || (finishKind() === 'flow' ? $('fin-id').value : '');
    await next(use);
  }

  async function next(use) {
    editing = -1;
    const body = { kind: 'scenario', useFlows: use ? [use] : [] };
    const r = await run('POST', '/api/session/reset', body, undefined, 'Resetting app…');
    if (r.ok) { $('fin-id').value = ''; await refreshState(); }
  }

  async function refreshState() {
    const r = await api('GET', '/api/state');
    if (r.ok) { state = r.data; render(); }
  }

  // --- wiring ---

  // The mouse wheel over the phone swipes it, as a finger does: one recorded swipe for a burst of notches.
  // Wheel down shows what is below, which is a finger that moves up.
  let wheelDX = 0;
  let wheelDY = 0;
  let wheelTimer = 0;

  function wireWheel() {
    const stage = $('stage');
    if (!stage) return;
    stage.addEventListener('wheel', (ev) => {
      if (mode !== 'act' || !frame || !session) return;
      ev.preventDefault();
      if (busy && !quick) return;
      wheelDX += ev.deltaX;
      wheelDY += ev.deltaY;
      clearTimeout(wheelTimer);
      wheelTimer = setTimeout(() => {
        const dx = wheelDX;
        const dy = wheelDY;
        wheelDX = wheelDY = 0;
        if (!frame || Math.max(Math.abs(dx), Math.abs(dy)) < 8) return;
        const dir = Math.abs(dy) >= Math.abs(dx) ? (dy > 0 ? 'up' : 'down') : (dx > 0 ? 'left' : 'right');
        quickAct({ do: 'swipe', value: dir });
      }, 60);
    }, { passive: false });
  }

  function boot() {
    const m = /(?:^|[#&])token=([0-9a-f]+)/.exec(location.hash);
    if (m) {
      token = m[1];
      history.replaceState(null, '', location.pathname + location.search);
    }
    for (const b of document.querySelectorAll('[data-close]')) b.addEventListener('click', () => closeOn(b.dataset.close));
    // Any way that a dialog ends, Escape too, drops the locator that was picked for it and what was typed in it.
    for (const d of document.querySelectorAll('dialog')) {
      d.addEventListener('close', () => {
        chosen = null;
        if (d.id === 'dlg-values') clear($('values-fields'));
        if (d.id === 'dlg-type') $('type-value').value = '';
      });
    }
    for (const btn of document.querySelectorAll('.kind-card')) {
      btn.addEventListener('click', () => syncStartKind(btn.dataset.kind));
    }
    syncStartKind($('start-kind').value);
    $('mode-act').onclick = () => { mode = 'act'; syncControls(); };
    $('mode-assert').onclick = () => { mode = 'assert'; syncControls(); };
    $('mode-capture').onclick = () => { mode = 'capture'; syncControls(); };
    $('cand-close').onclick = closeCandidates;
    $('open-pick').onchange = drawRecordings;
    $('open-existing').onclick = function reopen(confirmClear) {
      const [kind, ...rest] = $('open-pick').value.split(':');
      const id = rest.join(':');
      if (!id) return;
      const at = parseInt($('open-at').value, 10);
      const service = $('start-service').value || (state && state.service) || '';
      const body = { service, deviceSerial: $('device').value, confirmClear: confirmClear === true, resume: { kind, id, at: at > 0 ? at : 0 } };
      captures = [];
      // The server asks for the secrets and the params that no file holds before it starts, and for a
      // confirmation on a physical device: the call goes again with what was asked.
      const again = (x) => x === true
        ? reopen(true)
        : run('POST', '/api/session/start', Object.assign({}, body, x), (y) => y === true ? reopen(true) : undefined, 'Reopening recording…').then(refreshState);
      run('POST', '/api/session/start', body, again, 'Reopening recording…').then(refreshState);
    };
    $('device').onchange = drawPhysical;
    $('setup-open').onclick = openSetup;
    $('refresh').onclick = () => run('POST', '/api/session/refresh', {}, undefined, 'Refreshing screen…');
    $('finish').onclick = openFinish;
    $('reset').onclick = () => {
      const steps = (session && session.steps) || [];
      if (!steps.length) { next($('flow-pick').value); return; }
      confirm('Reset the recording', 'The ' + steps.length + ' recorded steps are dropped and the app is reset. Save first to keep them.', 'Reset', () => next($('flow-pick').value));
    };
    $('delete-last').onclick = () => {
      if (busy) return;
      repicking = -1;
      editing = -1;
      closeCandidates();
      run('DELETE', '/api/timeline/last', undefined, undefined, 'Deleting step…');
    };
    $('flow-run').onclick = () => {
      const flow = $('flow-pick').value;
      if (!flow) { banner('Pick a flow to run.'); return; }
      const with_ = {};
      for (const part of $('flow-with').value.split(',')) {
        const [k, ...v] = part.split('=');
        if (k.trim()) with_[k.trim()] = v.join('=').trim();
      }
      run('POST', '/api/session/use-flow', { flow, with: with_ }, undefined, 'Running flow…');
    };
    $('screen-name').onchange = () => {
      const name = $('screen-name').value.trim();
      if (!name) return;
      const rename = $('screen-rename').checked && !!frame && !!frame.screen && frame.screen !== name;
      $('screen-rename').checked = false;
      run('POST', '/api/session/screen', rename ? { name, rename: true, from: frame.screen } : { name }, undefined, 'Updating screen…');
    };
    $('start').onclick = function start(confirmClear) {
      const service = $('start-service').value || (state && state.service) || '';
      const body = { kind: $('start-kind').value || 'scenario', service, deviceSerial: $('device').value, useFlows: $('start-flow').value ? [$('start-flow').value] : [], confirmClear: confirmClear === true };
      captures = [];
      run('POST', '/api/session/start', body, (yes) => start(yes), 'Starting app…').then(refreshState);
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
    // Escape must not close the dialog while the device replays.
    $('dlg-finish').addEventListener('cancel', (ev) => { if (busy) ev.preventDefault(); });
    $('dlg-finish').addEventListener('keydown', (ev) => { if (busy && ev.key === 'Escape') ev.preventDefault(); });
    $('fin-next').onclick = nextActivity;
    wireWheel();
    $('form-confirm').onsubmit = (ev) => ev.preventDefault();
    if (!token) { render(); return; }
    load().then(loadDevices);
  }

  boot();
})();
