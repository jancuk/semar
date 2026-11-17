(function () {
  if (window.top !== window || window.__semarRec) return;
  window.__semarRec = true;

  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const isTrusted = e => e.isTrusted === true;

  function send(ev) {
    ev.page = { url: location.href, title: document.title };
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
      if (document.querySelectorAll(parts.join(' > ')).length === 1) break;
    }
    return parts.join(' > ');
  }

  function describe(el) {
    const hint = el.id || norm(el.innerText).slice(0, 40) || el.tagName.toLowerCase();
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
      send({ kind: 'fill', target: describe(el), secret: true });
    } else {
      send({ kind: 'fill', target: describe(el), value: el.value });
    }
  }
  window.__semarFlush = flush;

  function on(type, fn) { window.addEventListener(type, fn, true); }

  on('input', e => { if (isTrusted(e) && isField(e.target)) pending = e.target; });
  on('focusout', e => { if (isTrusted(e) && pending === e.target) flush(); });
  on('change', e => { if (isTrusted(e) && pending === e.target) flush(); });
  on('pagehide', flush);

  let lastClick = { el: null, at: 0 };
  let lastEnter = 0;
  on('click', e => {
    if (!isTrusted(e)) return;
    const el = meaningful(e.target);
    const now = Date.now();
    if (lastClick.el === el && now - lastClick.at < 80) return;
    // The click that a form makes on its button when Enter is pressed is part of the Enter.
    if (e.detail === 0 && now - lastEnter < 100) return;
    lastClick = { el, at: now };
    flush();
    // A click that puts the cursor in a text field is part of typing: a fill focuses its field itself.
    if (isField(el)) return;
    send({ kind: 'click', target: describe(el) });
  });

  on('keydown', e => {
    if (!isTrusted(e) || e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === 'Enter' && e.target.tagName === 'INPUT' && isField(e.target)) {
      flush();
      lastEnter = Date.now();
      send({ kind: 'press', value: 'Enter', target: describe(e.target) });
    } else if (e.key === 'Escape') {
      flush();
      send({ kind: 'press', value: 'Escape' });
    }
  });
})();
