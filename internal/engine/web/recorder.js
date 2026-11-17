(function () {
  if (window.top !== window || window.__semarRec) return;
  window.__semarRec = true;

  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const isTrusted = e => e.isTrusted === true;

  function send(ev) {
    ev.page = { url: location.origin + location.pathname, title: document.title };
    const bind = window.__semarEvent;
    if (typeof bind === 'function') bind(JSON.stringify(ev));
  }

  const INTERACTIVE = 'button, a[href], input, select, textarea, summary, label, [role], [data-testid], [data-test], [data-qa], [draggable="true"]';
  function meaningful(el) {
    if (!el || el.nodeType !== 1) return document.body;
    const hit = el.closest(INTERACTIVE);
    if (hit && hit.tagName === 'LABEL' && hit.control) return hit.control;
    return hit || el;
  }

  // visible is the test of the engine of a replay: a person could see the element.
  function visible(el) {
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none' || el.getClientRects().length === 0) return false;
    const b = el.getBoundingClientRect();
    return b.width > 0 && b.height > 0 && cs.opacity !== '0';
  }
  function everyVisible() { return Array.from(document.querySelectorAll('*')).filter(e => !['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE'].includes(e.tagName) && visible(e)); }

  // The roles whose accessible name is certain here, and so can be matched the way the engine matches
  // the accessibility tree. Another role is found by its test ID, its text, or its path.
  const NAMED_ROLES = new Set(['button', 'link', 'textbox', 'searchbox', 'checkbox', 'radio', 'heading', 'tab', 'menuitem', 'switch', 'spinbutton']);
  const FROM_CONTENT = new Set(['button', 'link', 'heading', 'tab', 'menuitem', 'switch', 'checkbox', 'radio']);

  function roleOf(el) {
    const explicit = norm(el.getAttribute('role')).split(' ')[0].toLowerCase();
    if (explicit) return explicit;
    switch (el.tagName) {
      case 'BUTTON': return 'button';
      case 'A': return el.hasAttribute('href') ? 'link' : '';
      case 'TEXTAREA': return 'textbox';
      case 'H1': case 'H2': case 'H3': case 'H4': case 'H5': case 'H6': return 'heading';
      case 'INPUT': {
        const type = (el.getAttribute('type') || 'text').toLowerCase();
        if (['button', 'submit', 'reset', 'image'].includes(type)) return 'button';
        if (type === 'checkbox' || type === 'radio') return type;
        if (type === 'search') return 'searchbox';
        if (type === 'number') return 'spinbutton';
        if (['text', 'email', 'tel', 'url', 'password'].includes(type)) return 'textbox';
      }
    }
    return '';
  }

  // accName is the accessible name, as far as it is needed. fromContent says that the name is the
  // text that the element shows, which a check must not use to find the element that it reads.
  function accName(el, role) {
    const by = el.getAttribute('aria-labelledby');
    if (by) {
      const t = norm(by.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean).map(e => e.innerText || e.textContent).join(' '));
      if (t) return { name: t, fromContent: false };
    }
    const label = norm(el.getAttribute('aria-label'));
    if (label) return { name: label, fromContent: false };
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') {
      const type = (el.getAttribute('type') || 'text').toLowerCase();
      if (['button', 'submit', 'reset'].includes(type)) return { name: norm(el.value) || (type === 'submit' ? 'Submit' : type === 'reset' ? 'Reset' : ''), fromContent: true };
      const t = norm(Array.from(el.labels || []).map(l => l.innerText).join(' '));
      if (t) return { name: t, fromContent: false };
      const ph = norm(el.getAttribute('placeholder'));
      if (ph) return { name: ph, fromContent: false };
    } else if (el.tagName === 'IMG') {
      return { name: norm(el.getAttribute('alt')), fromContent: false };
    } else if (FROM_CONTENT.has(role)) {
      const t = norm(el.innerText);
      if (t) return { name: t, fromContent: true };
    }
    return { name: norm(el.getAttribute('title')), fromContent: false };
  }

  // The same search as the engine: the elements that show exactly this text, the innermost one only.
  function byText(text) {
    const out = [];
    for (const el of document.querySelectorAll('*')) {
      if (['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE'].includes(el.tagName) || !visible(el)) continue;
      if (norm(el.innerText) !== text) continue;
      if (Array.from(el.children).some(c => visible(c) && norm(c.innerText) === text)) continue;
      out.push(el);
    }
    return out;
  }

  const TEST_ID_ATTRS = ['data-testid', 'data-test', 'data-qa'];
  function byTestId(attr, value) { return Array.from(document.querySelectorAll('[' + attr + '=' + JSON.stringify(value) + ']')).filter(visible); }

  // cssPath is the shortest chain of tags that picks this element only, from an id when there is one.
  function cssPath(el) {
    const parts = [];
    for (let cur = el; cur && cur.nodeType === 1 && cur !== document.documentElement; cur = cur.parentElement) {
      if (cur.id && /^[A-Za-z][\w-]*$/.test(cur.id) && document.querySelectorAll('#' + cur.id).length === 1) {
        parts.unshift('#' + cur.id);
        break;
      }
      let seg = cur.tagName.toLowerCase();
      const same = cur.parentElement ? Array.from(cur.parentElement.children).filter(c => c.tagName === cur.tagName) : [];
      if (same.length > 1) seg += ':nth-of-type(' + (same.indexOf(cur) + 1) + ')';
      parts.unshift(seg);
      // One tag alone breaks as soon as the page gets another of it, so the path has two.
      if (parts.length > 1 && document.querySelectorAll(parts.join(' > ')).length === 1) break;
    }
    return parts.join(' > ');
  }

  // describe chooses the locator, from what a redesign changes least to what it changes most: a test
  // ID, the role with the name, the text, and a path. A role with a name, and a text, are used only
  // when this element is the one that they pick. A check reads the text of its element, so it never
  // finds the element by that text.
  function describe(el, purpose) {
    const reading = purpose === 'check';
    const role = roleOf(el);
    const named = NAMED_ROLES.has(role) ? accName(el, role) : { name: '', fromContent: false };
    const text = reading ? '' : norm(el.innerText);
    const attrs = TEST_ID_ATTRS.map(a => [a, el.getAttribute(a)]).filter(([a, v]) => v && byTestId(a, v).length === 1);
    const hint = (attrs[0] && attrs[0][1]) || (reading ? '' : named.name || (text.length <= 40 ? text : '')) ||
      (reading && !named.fromContent ? named.name : '') || el.id || el.getAttribute('name') || norm(el.getAttribute('aria-label')) || el.tagName.toLowerCase();

    if (attrs.length) {
      const [attr, value] = attrs[0];
      return { locator: attr === 'data-testid' ? { testId: value } : { css: '[' + attr + '=' + JSON.stringify(value) + ']' }, hint };
    }
    if (role && named.name && named.name.length <= 120 && !(reading && named.fromContent)) {
      const same = everyVisible().filter(e => { const r = roleOf(e); return r === role && accName(e, r).name === named.name; });
      if (same.length === 1 && same[0] === el) return { locator: { role, name: named.name }, hint };
    }
    if (text && text.length <= 80) {
      const found = byText(text);
      if (found.length === 1 && found[0] === el) return { locator: { text }, hint };
    }
    return { locator: { css: cssPath(el) }, hint };
  }

  const TEXT_TYPES = ['text', 'search', 'email', 'tel', 'url', 'password', 'number', ''];
  function isField(el) {
    if (!el || el.nodeType !== 1) return false;
    if (el.tagName === 'TEXTAREA') return true;
    return el.tagName === 'INPUT' && TEXT_TYPES.includes((el.getAttribute('type') || '').toLowerCase());
  }
  function isSecret(el) {
    return el.tagName === 'INPUT' && ((el.getAttribute('type') || '').toLowerCase() === 'password' || /password/i.test(el.getAttribute('autocomplete') || ''));
  }

  // A field is sent when it loses focus or when Enter commits it. Its text is read at that moment,
  // and a password is never read.
  let pending = null;
  function flush() {
    const el = pending;
    pending = null;
    if (!el) return;
    if (isSecret(el)) {
      send({ kind: 'fill', target: describe(el, 'act'), secret: true });
    } else {
      send({ kind: 'fill', target: describe(el, 'act'), value: el.value });
    }
  }
  window.__semarFlush = flush;

  function on(type, fn) { window.addEventListener(type, fn, true); }

  // What an element shows, as the engine of a replay reads it: a field gives its value.
  function textOf(el) {
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') return norm(el.value);
    if (el.tagName === 'SELECT') return norm(Array.from(el.selectedOptions).map(o => o.text).join(', '));
    return norm(visible(el) ? el.innerText : el.textContent);
  }

  function flash(el) {
    const was = [el.style.outline, el.style.outlineOffset];
    el.style.outline = '2px solid #e0245e';
    el.style.outlineOffset = '2px';
    setTimeout(() => { el.style.outline = was[0]; el.style.outlineOffset = was[1]; }, 700);
  }

  // A click with the Option key is a check and does nothing on the page: the page never sees any of
  // its events, and the element that it points at shows an outline for a moment.
  ['pointerdown', 'pointerup', 'mousedown', 'mouseup', 'dblclick', 'contextmenu'].forEach(type => on(type, e => {
    if (isTrusted(e) && e.altKey) { e.preventDefault(); e.stopImmediatePropagation(); }
  }));
  on('click', e => {
    if (!isTrusted(e) || !e.altKey) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    let el = e.target;
    while (el.parentElement && el.parentElement !== document.body && !textOf(el)) el = el.parentElement;
    if (el === document.body || el === document.documentElement || (el.tagName === 'INPUT' && isSecret(el))) return;
    flush();
    flash(el);
    send({ kind: 'check', target: describe(el, 'check'), value: textOf(el) });
  });

  on('input', e => { if (isTrusted(e) && isField(e.target)) pending = e.target; });
  on('focusout', e => { if (isTrusted(e) && pending === e.target) flush(); });
  on('change', e => { if (isTrusted(e) && pending === e.target) flush(); });
  on('pagehide', flush);

  // A drag is the button pressed on one element and let go on another, with the pointer moved in
  // between. The browser's own drag and drop sends dragstart and then dragover, and no mouseup.
  const MOVED = 6;
  let down = null;
  let moved = false;
  let nativeFrom = null;
  let nativeOver = null;
  let afterDrag = false;
  let nativeStarted = false;

  function sendDrag(from, to) {
    if (!from || !to || from === to || from.contains(to) || to.contains(from)) return;
    flush();
    send({ kind: 'drag', target: describe(from, 'act'), to: describe(to, 'act') });
  }

  on('mousedown', e => {
    if (!isTrusted(e) || e.button !== 0) return;
    down = { el: meaningful(e.target), x: e.clientX, y: e.clientY };
    moved = false;
    afterDrag = false;
    nativeStarted = false;
  });
  on('mousemove', e => {
    if (down && !moved && Math.hypot(e.clientX - down.x, e.clientY - down.y) > MOVED) moved = true;
  });
  on('mouseup', e => {
    if (!isTrusted(e) || !down) return;
    const from = down.el;
    down = null;
    if (!moved) return;
    afterDrag = true;
    if (!nativeStarted) sendDrag(from, meaningful(e.target));
  });
  on('dragstart', e => {
    if (!isTrusted(e)) return;
    nativeFrom = meaningful(e.target);
    nativeOver = null;
    nativeStarted = true;
    afterDrag = true;
  });
  on('dragover', e => { if (nativeFrom) nativeOver = meaningful(e.target); });
  function endNative() {
    const from = nativeFrom;
    const over = nativeOver;
    nativeFrom = nativeOver = null;
    if (from) sendDrag(from, over);
  }
  on('drop', endNative);
  on('dragend', endNative);

  let lastClick = { el: null, at: 0 };
  let lastEnter = 0;
  on('click', e => {
    if (!isTrusted(e)) return;
    if (afterDrag) { afterDrag = false; return; }
    const el = meaningful(e.target);
    const now = Date.now();
    if (lastClick.el === el && now - lastClick.at < 80) return;
    // The click that a form makes on its button when Enter is pressed is part of the Enter.
    if (e.detail === 0 && now - lastEnter < 100) return;
    lastClick = { el, at: now };
    flush();
    // A click that puts the cursor in a text field is part of typing: a fill focuses its field itself.
    if (isField(el)) return;
    send({ kind: 'click', target: describe(el, 'act') });
  });

  on('keydown', e => {
    if (!isTrusted(e) || e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === 'Enter' && e.target.tagName === 'INPUT' && isField(e.target)) {
      flush();
      lastEnter = Date.now();
      send({ kind: 'press', value: 'Enter', target: describe(e.target, 'act') });
    } else if (e.key === 'Escape') {
      flush();
      send({ kind: 'press', value: 'Escape' });
    }
  });
})();
