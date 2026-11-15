'use strict';

// The page builds every text with textContent, never with HTML, because provider IDs and
// the output of a sign-in are data.
(function () {
  const TOKEN_KEY = 'semar-dashboard-token';
  const KIND_LABELS = {
    claude: 'Claude Code',
    codex: 'Codex',
    anthropic: 'Anthropic API',
    openai: 'OpenAI format API',
  };
  const PRESETS = [
    { key: 'claude', label: 'Claude Code', kind: 'claude', id: 'claude' },
    { key: 'codex', label: 'Codex', kind: 'codex', id: 'codex' },
    { key: 'anthropic', label: 'Anthropic API', kind: 'anthropic', id: 'anthropic', modelHint: 'claude-opus-5-5', env: 'ANTHROPIC_API_KEY' },
    { key: 'openai', label: 'OpenAI API', kind: 'openai', id: 'openai', env: 'OPENAI_API_KEY' },
    { key: 'deepseek', label: 'DeepSeek', kind: 'openai', id: 'deepseek', baseUrl: 'https://api.deepseek.com', modelHint: 'deepseek-chat', env: 'DEEPSEEK_API_KEY' },
    { key: 'other', label: 'Other server (OpenAI format)', kind: 'openai', id: 'server', baseUrlHint: 'https://llm.example.com/v1', needsAddress: true },
  ];
  const PERIODS = [
    { days: 1, label: 'Today' },
    { days: 7, label: '7 days' },
    { days: 30, label: '30 days' },
  ];

  let token = null;
  let state = null;
  let usageDays = 30;
  let uid = 0;
  const logins = new Map(); // provider ID to its sign-in box
  const modelLists = new Map(); // provider ID to the last answer of its models route
  const modelPending = new Map(); // provider ID to the request in flight
  const modelControls = new Map(); // provider ID to the model control on its card now
  const OTHER = '__other__'; // no model name can start with an underscore

  const nodes = {};

  // --- small helpers ---

  function el(tag, props, children) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') e.className = v;
      else if (k === 'text') e.textContent = v;
      else if (k === 'on') for (const [ev, fn] of Object.entries(v)) e.addEventListener(ev, fn);
      else if (k === 'value' || k === 'checked' || k === 'disabled') e[k] = v;
      else e.setAttribute(k, v === true ? '' : String(v));
    }
    for (const c of [].concat(children === undefined ? [] : children)) {
      if (c === null || c === undefined || c === false) continue;
      e.append(c);
    }
    return e;
  }

  function nextId(prefix) {
    uid += 1;
    return prefix + '-' + uid;
  }

  function field(label, input, wide) {
    if (!input.id) input.id = nextId('f');
    return el('label', { class: wide ? 'field wide' : 'field', for: input.id }, [el('span', { text: label }), input]);
  }

  function textInput(props) {
    return el('input', Object.assign({ type: 'text', autocomplete: 'off', spellcheck: 'false' }, props));
  }

  function switchInput(label, checked) {
    const input = el('input', { type: 'checkbox', checked: checked });
    return { input, node: el('label', { class: 'switch' }, [input, label]) };
  }

  // A slot shows one message next to the control that caused it.
  function slot() {
    return el('div', { 'aria-live': 'polite' });
  }

  function say(target, text, bad) {
    target.replaceChildren();
    if (!text) {
      target.className = '';
      target.removeAttribute('role');
    } else {
      target.className = bad ? 'note bad' : 'note';
      if (bad) target.setAttribute('role', 'alert');
      else target.removeAttribute('role');
      String(text).split('\n').forEach((line, i) => {
        if (i > 0) target.append(el('br'));
        target.append(line);
      });
    }
    if (target === nodes.messages) placeMessages();
  }

  // The page-wide message sits under the masthead only while it has something to say, so an
  // empty one adds no gap to the grid.
  function placeMessages() {
    const m = nodes.messages;
    if (!m.childNodes.length) m.remove();
    else if (!m.isConnected) nodes.masthead.after(m);
  }

  async function busy(button, work) {
    button.disabled = true;
    try {
      await work();
    } finally {
      button.disabled = false;
    }
  }

  function isHarness(kind) {
    return kind === 'claude' || kind === 'codex';
  }

  function quoteArg(a) {
    return /^[A-Za-z0-9_./:=@%+-]+$/.test(a) ? a : "'" + a.replace(/'/g, "'\\''") + "'";
  }

  const numberFormat = new Intl.NumberFormat();
  const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

  function formatTime(s) {
    const d = new Date(s);
    return isNaN(d.getTime()) || d.getFullYear() < 2000 ? '-' : timeFormat.format(d);
  }

  function formatCost(v) {
    if (!v) return '-';
    return '$' + v.toFixed(v < 0.01 ? 4 : 2);
  }

  function formatDuration(ms) {
    if (!ms) return '-';
    const s = Math.round(ms / 1000);
    return s < 60 ? s + ' s' : Math.floor(s / 60) + ' min ' + (s % 60) + ' s';
  }

  // --- the token ---

  function readToken() {
    const m = /(?:^#|&)token=([^&]*)/.exec(location.hash);
    if (m) {
      const t = decodeURIComponent(m[1]);
      try {
        sessionStorage.setItem(TOKEN_KEY, t);
      } catch (e) {
        // The page still works for this visit without storage.
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

  function showStart(text) {
    for (const s of nodes.sections || []) s.remove();
    nodes.messages.className = 'note';
    nodes.messages.removeAttribute('role');
    nodes.messages.replaceChildren(
      text + ' Start it with ',
      el('code', { text: 'semar dashboard' }),
      ' in a terminal and open the address that it prints.',
    );
    placeMessages();
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
    if (res.status === 403) {
      forgetToken();
      showStart('This page has no valid token.');
      throw new TokenError((data && data.error) || 'The token is not valid.');
    }
    if (!res.ok) throw new Error((data && data.error) || 'The dashboard answered with status ' + res.status + '.');
    return data;
  }

  // Every handler reports its error in the slot next to the control.
  function report(target, err) {
    if (err instanceof TokenError) return;
    say(target, err.message, true);
  }

  // --- state and providers ---

  async function loadState() {
    try {
      state = await api('GET', '/api/state');
    } catch (err) {
      if (!(err instanceof TokenError)) say(nodes.messages, err.message, true);
      return;
    }
    nodes.path.textContent = state.file + (state.exists ? '' : ' (not written yet)');
    if (state.fileError) {
      say(nodes.messages, 'The providers file has an error. The page shows the built-in list and changes nothing until you fix the file by hand.\n' + state.fileError, true);
    } else {
      say(nodes.messages, '');
    }
    renderProviders();
    if (!nodes.add.childNodes.length) renderAdd();
  }

  function statusOf(p) {
    if (isHarness(p.kind)) {
      if (p.health && !p.health.installed) return ['bad', 'Not installed'];
      if (p.account && p.account.signedIn) return ['ok', p.account.plan ? 'Signed in, plan ' + p.account.plan : 'Signed in'];
      if (p.account) return ['warn', 'Not signed in'];
      if (p.error) return ['bad', 'Cannot be checked'];
      return ['warn', 'Not known'];
    }
    if (p.error && p.keyReady === undefined) return ['bad', 'Cannot be checked'];
    return p.keyReady ? ['ok', 'Key ready'] : ['bad', 'Key missing'];
  }

  function signedIn(p) {
    return Boolean(p.account && p.account.signedIn);
  }

  function details(p) {
    const out = [];
    if (isHarness(p.kind)) {
      if (p.health && p.health.version) out.push('Version ' + p.health.version + '.');
      if (p.account && p.account.detail) out.push(p.account.detail);
      else if (p.health && p.health.detail) out.push(p.health.detail);
    } else {
      if (p.apiKey && p.keyReady && p.health && p.health.detail) out.push(p.health.detail);
      if (!p.apiKey) out.push('No key is needed for a server on this computer.');
      else if (!p.keyReady && p.apiKey.startsWith('env:')) out.push('The environment variable ' + p.apiKey.slice(4) + ' is not set where semar dashboard runs.');
      else if (!p.keyReady) out.push('The keychain has no key for this provider. Set the key below.');
    }
    if (p.error) out.push(p.error);
    return out;
  }

  function renderProviders() {
    const list = nodes.providers;
    list.replaceChildren();
    for (const p of state.providers) list.append(providerCard(p));
  }

  function providerCard(p) {
    const isDefault = p.id === state.default;
    const nameId = nextId('name');
    const [cls, words] = statusOf(p);
    const card = el('article', { class: isDefault ? 'card is-default' : 'card', 'aria-labelledby': nameId }, [
      el('div', { class: 'row' }, [
        el('h3', { class: 'name', id: nameId, text: p.id }),
        el('span', { class: 'badge', text: KIND_LABELS[p.kind] || p.kind }),
        isDefault ? el('span', { class: 'badge accent', text: 'Default' }) : null,
        el('span', { class: 'grow' }),
        el('span', { class: 'status ' + cls, text: words }),
      ]),
    ]);
    for (const line of details(p)) card.append(el('p', { class: 'small muted wrap', text: line }));
    card.append(editForm(p));
    if (!isHarness(p.kind)) card.append(keyForm(p));
    card.append(actions(p, isDefault));
    const box = logins.get(p.id);
    if (box && signedIn(p)) logins.delete(p.id);
    else if (box) card.append(box.node);
    return card;
  }

  function editForm(p) {
    const note = slot();
    const harness = isHarness(p.kind);
    const model = modelControl(p);
    const form = el('form', { class: 'form', novalidate: true, 'aria-label': 'Settings of ' + p.id }, model.nodes);
    let mcp, skills, deny, address, keyRef;
    if (harness) {
      const setup = p.setup || { mcp: true, skills: true, deny: [] };
      mcp = switchInput('MCP servers', setup.mcp !== false);
      skills = switchInput('Skills', setup.skills !== false);
      const explain = [
        'With these on, the planner can call your MCP tools under your own permission rules while it reads a repository. ',
        el('code', { text: 'semar plan --no-setup' }),
        ' turns them off for one run.',
      ];
      if (p.kind === 'codex') explain.push(' Codex always reads your own instruction file, and a list of tools that never run is not supported.');
      form.append(el('div', { class: 'wide field' }, [
        el('div', { class: 'row' }, [mcp.node, skills.node]),
        el('p', { class: 'small muted' }, explain),
      ]));
      if (p.kind !== 'codex') {
        deny = textInput({ value: (setup.deny || []).join(', '), placeholder: 'For example Bash, mcp__orders__delete_order' });
        form.append(field('Tools that never run, separated by commas', deny, true));
      }
    } else {
      address = el('input', { type: 'url', value: p.baseUrl || '', placeholder: "Empty means the vendor's address", autocomplete: 'off', spellcheck: 'false' });
      keyRef = textInput({ value: p.apiKey || '', placeholder: 'env:NAME or keychain:NAME' });
      form.append(field('Address', address), field('Key reference', keyRef));
    }
    const save = el('button', { type: 'submit', class: 'primary', text: 'Save changes' });
    form.append(el('div', { class: 'actions wide' }, [save]), el('div', { class: 'wide' }, [note]));
    form.addEventListener('submit', (ev) => {
      ev.preventDefault();
      const body = { id: p.id, kind: p.kind, model: model.value() };
      if (harness) {
        const list = deny ? deny.value.split(',').map((s) => s.trim()).filter(Boolean) : (p.setup && p.setup.deny) || [];
        body.setup = { mcp: mcp.input.checked, skills: skills.input.checked, deny: list };
      } else {
        if (!body.model) {
          say(note, 'An API provider needs a model.', true);
          return;
        }
        body.baseUrl = address.value.trim();
        body.apiKey = keyRef.value.trim();
      }
      busy(save, async () => {
        try {
          await api('PUT', '/api/providers/' + encodeURIComponent(p.id), body);
          if (!harness) modelLists.delete(p.id);
          await loadState();
        } catch (err) {
          report(note, err);
        }
      });
    });
    return form;
  }

  // --- the model ---

  function modelText(m) {
    return m.label && m.label !== m.id ? m.label + ' (' + m.id + ')' : m.id;
  }

  // modelControl is a list to choose from and, behind "Another model...", a text field. Until
  // the list arrives it shows the saved value, so the page never waits for a harness.
  function modelControl(p) {
    const harness = isHarness(p.kind);
    const select = el('select', {});
    const custom = textInput({ placeholder: harness ? "Empty means the harness's default" : 'The name of the model' });
    const note = slot();
    const wrap = el('div', { class: 'wide', hidden: true }, [el('div', { class: 'section' }, [field('Model name', custom), note])]);
    let chosen = p.model || '';

    function value() {
      return (select.value === OTHER ? custom.value : select.value).trim();
    }

    function sync() {
      wrap.hidden = select.value !== OTHER;
    }

    function option(v, text, props) {
      return el('option', Object.assign({ value: v, text: text }, props));
    }

    // show fills the list from an answer of the models route, or, with null, shows the wait.
    function show(data) {
      if (select.options.length) chosen = value();
      const models = data ? data.models || [] : [];
      const used = data ? data.used || [] : [];
      const listed = new Set(models.concat(used.map((id) => ({ id }))).map((m) => m.id));
      select.replaceChildren(harness
        ? option('', "The harness's default")
        : option('', 'Choose a model', { disabled: true }));
      for (const m of models) select.append(option(m.id, modelText(m)));
      if (used.length) select.append(el('optgroup', { label: 'Used before' }, used.map((id) => option(id, id))));
      if (!data) {
        if (chosen) select.append(option(chosen, chosen));
        select.append(option('', 'Loading the models\u2026', { disabled: true }));
      }
      select.append(option(OTHER, 'Another model\u2026'));
      let target = chosen;
      if (data) {
        if (data.error || !listed.size) target = OTHER;
        else if (chosen && !listed.has(chosen)) target = OTHER;
      }
      select.value = target;
      if (target === OTHER) custom.value = chosen;
      say(note, data && data.error ? data.error : '');
      sync();
    }

    select.addEventListener('change', sync);
    modelControls.set(p.id, { show });
    const known = modelLists.get(p.id);
    show(known || null);
    if (!known) loadModels(p.id);
    return { nodes: [field('Model', select), wrap], value };
  }

  function loadModels(id) {
    if (modelPending.has(id)) return;
    const req = api('GET', '/api/providers/' + encodeURIComponent(id) + '/models').catch((err) => (
      err instanceof TokenError ? null : { models: [], used: [], error: err.message }
    ));
    modelPending.set(id, req);
    req.then((data) => {
      if (modelPending.get(id) !== req) return;
      modelPending.delete(id);
      if (!data) return;
      modelLists.set(id, data);
      const control = modelControls.get(id);
      if (control) control.show(data);
    });
  }

  // refreshModels asks again, for example after a sign-in, and drops an answer still on its way.
  function refreshModels(id) {
    modelLists.delete(id);
    modelPending.delete(id);
    const control = modelControls.get(id);
    if (control) control.show(null);
    loadModels(id);
  }

  function keyForm(p) {
    const note = slot();
    const value = el('input', { type: 'password', autocomplete: 'off', spellcheck: 'false', placeholder: 'Paste the key' });
    const submit = el('button', { type: 'submit', text: 'Set key' });
    const form = el('form', { class: 'form', novalidate: true, 'aria-label': 'Key of ' + p.id }, [
      field('New key, stored in the keychain', value),
      el('div', { class: 'actions wide' }, [submit]),
      el('div', { class: 'wide' }, [note]),
    ]);
    form.addEventListener('submit', (ev) => {
      ev.preventDefault();
      const v = value.value;
      value.value = '';
      if (!v.trim()) {
        say(note, 'Paste the key first.', true);
        return;
      }
      busy(submit, async () => {
        try {
          await api('PUT', '/api/providers/' + encodeURIComponent(p.id) + '/key', { value: v });
          await loadState();
        } catch (err) {
          report(note, err);
        }
      });
    });
    return form;
  }

  function actions(p, isDefault) {
    const note = slot();
    const row = el('div', { class: 'actions' });
    if (!isDefault) {
      const b = el('button', { type: 'button', text: 'Make default' });
      b.addEventListener('click', () => busy(b, async () => {
        try {
          await api('PUT', '/api/default', { id: p.id });
          await loadState();
        } catch (err) {
          report(note, err);
        }
      }));
      row.append(b);
    }
    if (isHarness(p.kind) && p.account && !p.account.signedIn && p.loginCommand && p.loginCommand.length) {
      const running = logins.get(p.id);
      const b = el('button', { type: 'button', text: 'Sign in', disabled: Boolean(running && running.running) });
      b.addEventListener('click', () => busy(b, async () => {
        say(note, '');
        try {
          const job = await api('POST', '/api/providers/' + encodeURIComponent(p.id) + '/login');
          showLogin(p, job);
        } catch (err) {
          report(note, err);
        }
      }));
      row.append(b);
    }
    const check = el('button', { type: 'button', text: 'Check again' });
    check.addEventListener('click', () => busy(check, () => {
      modelLists.clear();
      return loadState();
    }));
    row.append(check);
    const remove = el('button', {
      type: 'button',
      class: 'danger',
      text: 'Remove',
      disabled: isDefault,
      title: isDefault ? 'The default provider cannot be removed' : null,
    });
    remove.addEventListener('click', () => {
      if (!window.confirm('Remove the provider ' + p.id + '?')) return;
      busy(remove, async () => {
        try {
          await api('DELETE', '/api/providers/' + encodeURIComponent(p.id));
          logins.delete(p.id);
          modelLists.delete(p.id);
          modelPending.delete(p.id);
          modelControls.delete(p.id);
          await loadState();
        } catch (err) {
          report(note, err);
        }
      });
    });
    row.append(remove);
    return el('div', { class: 'section' }, [row, note]);
  }

  // --- sign-in ---

  function showLogin(p, job) {
    let box = logins.get(p.id);
    if (!box) {
      const log = el('pre', { class: 'log', tabindex: '0', 'aria-label': 'Output of the sign-in of ' + p.id });
      const status = el('p', { class: 'small' });
      const cancel = el('button', { type: 'button', text: 'Cancel' });
      const note = slot();
      const command = el('p', { class: 'small muted wrap' }, [
        'The sign-in may need a terminal. You can also run this command in one: ',
        el('code', { text: p.loginCommand.map(quoteArg).join(' ') }),
      ]);
      const node = el('div', { class: 'section' }, [el('h3', { text: 'Sign in' }), log, status, el('div', { class: 'actions' }, [cancel]), note, command]);
      box = { node, log, status, cancel, note, job: null, timer: null, running: false, finished: null };
      cancel.addEventListener('click', () => busy(cancel, async () => {
        try {
          updateLogin(p.id, await api('DELETE', '/api/logins/' + encodeURIComponent(box.job)));
        } catch (err) {
          report(note, err);
        }
      }));
      logins.set(p.id, box);
      renderProviders();
    }
    updateLogin(p.id, job);
  }

  function updateLogin(id, job) {
    const box = logins.get(id);
    if (!box) return;
    box.job = job.id;
    box.log.replaceChildren();
    job.lines.forEach((line, i) => {
      if (i > 0) box.log.append('\n');
      appendLine(box.log, line);
    });
    if (!job.lines.length) box.log.append('No output yet.');
    box.log.scrollTop = box.log.scrollHeight;
    const words = { running: 'Running.', done: 'Finished.', failed: 'Failed.', cancelled: 'Cancelled.' }[job.state] || job.state;
    box.status.textContent = job.detail ? job.detail : words;
    box.cancel.disabled = job.state !== 'running';
    box.running = job.state === 'running';
    clearTimeout(box.timer);
    if (box.running) say(box.note, '');
    if (job.state === 'running') {
      box.timer = setTimeout(async () => {
        try {
          updateLogin(id, await api('GET', '/api/logins/' + encodeURIComponent(job.id)));
        } catch (err) {
          report(box.note, err);
        }
      }, 1000);
    } else if (box.finished !== job.id) {
      box.finished = job.id;
      finishLogin(id, job);
    }
  }

  // finishLogin asks the state again. A provider that is signed in now drops the sign-in panel
  // when the card is drawn, and gets its model list again. After a failure or a cancel the
  // panel stays, and the card offers "Sign in" again, because the account is still signed out.
  async function finishLogin(id, job) {
    await loadState();
    if (!state) return;
    const p = state.providers.find((x) => x.id === id);
    if (p && signedIn(p)) {
      refreshModels(id);
      return;
    }
    const box = logins.get(id);
    if (box && job.state === 'done') {
      say(box.note, (KIND_LABELS[p ? p.kind : ''] || 'The provider') + ' still reports that it is not signed in.');
    }
  }

  // appendLine makes an https address of the harness's output a link, and keeps the rest text.
  function appendLine(target, line) {
    for (const part of line.split(/(https:\/\/[^\s<>"']+)/)) {
      if (!part) continue;
      let url = null;
      try {
        url = part.startsWith('https://') ? new URL(part) : null;
      } catch (e) {
        url = null;
      }
      if (url && url.protocol === 'https:') {
        target.append(el('a', { href: url.href, target: '_blank', rel: 'noopener noreferrer', text: part }));
      } else {
        target.append(part);
      }
    }
  }

  // --- add a provider ---

  function freeId(base) {
    const taken = new Set(state.providers.map((p) => p.id));
    if (!taken.has(base)) return base;
    for (let i = 2; ; i++) if (!taken.has(base + '-' + i)) return base + '-' + i;
  }

  function renderAdd() {
    const note = slot();
    const type = el('select', {}, PRESETS.map((p) => el('option', { value: p.key, text: p.label })));
    const id = textInput({});
    const model = textInput({});
    const address = el('input', { type: 'url', autocomplete: 'off', spellcheck: 'false' });
    const source = el('select', {}, [
      el('option', { value: 'paste', text: 'Paste a key' }),
      el('option', { value: 'env', text: 'An environment variable' }),
      el('option', { value: 'none', text: 'No key, for a server on this computer' }),
    ]);
    const key = el('input', { type: 'password', autocomplete: 'off', spellcheck: 'false', placeholder: 'Paste the key' });
    const envName = textInput({});
    const apiPart = el('div', { class: 'form wide' });
    const keyPart = el('div');
    const add = el('button', { type: 'submit', class: 'primary', text: 'Add provider' });
    const form = el('form', { class: 'form', novalidate: true, 'aria-label': 'Add a provider' }, [
      field('Type', type),
      field('ID', id),
      field('Model', model),
      apiPart,
      el('div', { class: 'actions wide' }, [add]),
      el('div', { class: 'wide' }, [note]),
    ]);

    function preset() {
      return PRESETS.find((p) => p.key === type.value);
    }
    function showKeyPart() {
      keyPart.replaceChildren();
      if (source.value === 'paste') keyPart.append(field('Key, stored in the keychain', key));
      if (source.value === 'env') keyPart.append(field('Name of the environment variable', envName));
    }
    function fill() {
      const p = preset();
      id.value = freeId(p.id);
      model.value = '';
      model.placeholder = isHarness(p.kind) ? "Empty means the harness's default" : 'Required' + (p.modelHint ? ', for example ' + p.modelHint : '');
      apiPart.replaceChildren();
      if (!isHarness(p.kind)) {
        address.value = p.baseUrl || '';
        address.placeholder = p.baseUrlHint || "Empty means the vendor's address";
        envName.value = p.env || '';
        envName.placeholder = 'For example ORDERS_LLM_KEY';
        key.value = '';
        source.value = 'paste';
        showKeyPart();
        apiPart.append(field(p.needsAddress ? 'Address, required' : 'Address', address), field('Key', source), keyPart);
      }
      say(note, '');
    }
    type.addEventListener('change', fill);
    source.addEventListener('change', showKeyPart);
    form.addEventListener('submit', (ev) => {
      ev.preventDefault();
      const p = preset();
      const body = { id: id.value.trim(), kind: p.kind, model: model.value.trim() };
      const pasted = key.value;
      if (!body.id) return say(note, 'Give the provider an ID.', true);
      if (isHarness(p.kind)) {
        body.setup = { mcp: true, skills: true, deny: [] };
      } else {
        if (!body.model) return say(note, 'An API provider needs a model.', true);
        body.baseUrl = address.value.trim();
        if (p.needsAddress && !body.baseUrl) return say(note, 'Give the address of the server.', true);
        if (source.value === 'paste') {
          if (!pasted.trim()) return say(note, 'Paste the key, or choose another way to give it.', true);
          body.apiKey = 'keychain:' + body.id;
        } else if (source.value === 'env') {
          if (!envName.value.trim()) return say(note, 'Give the name of the environment variable.', true);
          body.apiKey = 'env:' + envName.value.trim();
        }
      }
      key.value = '';
      busy(add, async () => {
        try {
          await api('PUT', '/api/providers/' + encodeURIComponent(body.id), body, { 'If-None-Match': '*' });
          let keyError = null;
          if (source.value === 'paste' && !isHarness(p.kind)) {
            try {
              await api('PUT', '/api/providers/' + encodeURIComponent(body.id) + '/key', { value: pasted });
            } catch (err) {
              keyError = err;
            }
          }
          await loadState();
          fill();
          if (keyError instanceof TokenError) return;
          if (keyError) say(note, 'Added ' + body.id + ', but the key was not stored. Set it on the card of the provider. ' + keyError.message, true);
          else say(note, 'Added ' + body.id + '.');
        } catch (err) {
          report(note, err);
        }
      });
    });
    fill();
    nodes.add.replaceChildren(el('div', { class: 'card' }, [form]));
  }

  // --- usage ---

  function windowLabel(w) {
    const known = { five_hour: '5 hours', seven_day: '7 days', seven_day_opus: '7 days, Opus', seven_day_sonnet: '7 days, Sonnet' };
    if (known[w]) return known[w];
    const s = String(w).replace(/_/g, ' ');
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  function outcomeWords(o) {
    if (o === 'ok') return 'Finished';
    if (o === 'rejected') return 'Rejected';
    if (o && o.startsWith('failed')) {
      const why = o.slice('failed'.length).replace(/^:/, '').replace(/_/g, ' ');
      return why ? 'Failed, ' + why : 'Failed';
    }
    return o || '-';
  }

  function table(headers, rows) {
    const head = el('tr', {}, headers.map(([label, num]) => el('th', { scope: 'col', class: num ? 'num' : null, text: label })));
    const body = rows.map((cells) => el('tr', {}, cells.map(([text, num]) => el('td', { class: num ? 'num' : null, text: String(text) }))));
    return el('div', { class: 'table-wrap', tabindex: '0' }, [el('table', {}, [el('thead', {}, [head]), el('tbody', {}, body)])]);
  }

  function meters(id, limits) {
    const list = el('div', { class: 'meters' });
    for (const w of limits.windows || []) {
      const u = Math.max(0, Math.min(1, Number(w.utilization) || 0));
      const pct = Math.round(u * 100);
      const bar = el('i');
      bar.style.width = pct + '%';
      const label = windowLabel(w.window);
      list.append(el('div', { class: u >= 0.9 ? 'meter bad' : u >= 0.75 ? 'meter warn' : 'meter' }, [
        el('div', { class: 'row' }, [el('span', { class: 'grow', text: label }), el('span', { class: 'mono', text: pct + '%' })]),
        el('div', { class: 'bar', role: 'progressbar', 'aria-label': label + ' used', 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(pct) }, [bar]),
        el('p', { class: 'small muted', text: 'Resets ' + formatTime(w.resetsAt) }),
      ]));
    }
    return el('div', { class: 'section' }, [
      el('div', { class: 'row' }, [el('h3', { class: 'name grow', text: id }), el('span', { class: 'small muted', text: 'As of ' + formatTime(limits.at) })]),
      list,
    ]);
  }

  async function loadUsage() {
    for (const b of nodes.tabs.querySelectorAll('button')) b.setAttribute('aria-pressed', String(Number(b.dataset.days) === usageDays));
    let u;
    try {
      u = await api('GET', '/api/usage?days=' + usageDays);
    } catch (err) {
      report(nodes.usageNote, err);
      return;
    }
    say(nodes.usageNote, '');
    const out = [];
    for (const id of Object.keys(u.limits).sort()) out.push(meters(id, u.limits[id]));
    if (!u.rows.length) {
      out.push(el('div', { class: 'empty' }, ['Usage appears after the first ', el('code', { text: 'semar plan' }), '.']));
    } else {
      out.push(table(
        [['Provider'], ['Model'], ['Runs', 1], ['Failed', 1], ['Turns', 1], ['Input tokens', 1], ['Output tokens', 1], ['Cost', 1]],
        u.rows.map((r) => [
          [r.provider], [r.model || 'Default model'], [r.runs, 1], [r.failed, 1], [numberFormat.format(r.turns), 1],
          [numberFormat.format(r.inputTokens), 1], [numberFormat.format(r.outputTokens), 1], [formatCost(r.costUsd), 1],
        ]),
      ));
      out.push(el('h3', { text: 'Latest runs' }));
      out.push(table(
        [['When'], ['Provider'], ['Model'], ['Outcome'], ['Turns', 1], ['Input tokens', 1], ['Output tokens', 1], ['Cost', 1], ['Time', 1]],
        u.recent.map((r) => [
          [formatTime(r.at)], [r.provider], [r.model || 'Default model'], [outcomeWords(r.outcome)], [numberFormat.format(r.turns), 1],
          [numberFormat.format(r.inputTokens), 1], [numberFormat.format(r.outputTokens), 1], [formatCost(r.costUsd), 1], [formatDuration(r.durationMs), 1],
        ]),
      ));
    }
    nodes.usage.replaceChildren(...out);
  }

  // --- the page ---

  function section(title, head, body) {
    const titleId = nextId('title');
    return el('section', { class: 'section', 'aria-labelledby': titleId }, [
      el('div', { class: 'section-head' }, [el('h2', { id: titleId, text: title })].concat(head || [])),
    ].concat(body));
  }

  function start() {
    nodes.main = document.querySelector('main');
    nodes.masthead = document.querySelector('.masthead');
    nodes.messages = el('div', { 'aria-live': 'polite' });
    nodes.path = document.getElementById('file-path');
    token = readToken();
    if (!token) {
      showStart('This page needs the address with its token.');
      return;
    }
    nodes.providers = el('div', { class: 'list' });
    nodes.add = el('div');
    nodes.usage = el('div', { class: 'section' });
    nodes.usageNote = slot();
    nodes.tabs = el('div', { class: 'tabs', role: 'group', 'aria-label': 'Period' }, PERIODS.map((p) => {
      const b = el('button', { type: 'button', text: p.label, 'data-days': String(p.days), 'aria-pressed': String(p.days === usageDays) });
      b.addEventListener('click', () => {
        usageDays = p.days;
        loadUsage();
      });
      return b;
    }));
    nodes.sections = [
      section('Providers', [], [nodes.providers]),
      section('Add a provider', [], [nodes.add]),
      section('Usage', [nodes.tabs], [nodes.usageNote, nodes.usage]),
    ];
    nodes.main.append(...nodes.sections);
    loadState();
    loadUsage();
  }

  start();
})();
