(function () {
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const SKIP = new Set(['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'HEAD', 'META', 'LINK', 'TITLE', 'OPTION', 'OPTGROUP', 'DATALIST', 'SVG', 'PATH']);

  function visible(el) {
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none' || el.getClientRects().length === 0) return false;
    const b = el.getBoundingClientRect();
    return b.width > 0 && b.height > 0 && cs.opacity !== '0';
  }

  const BUTTON_TYPES = ['button', 'submit', 'reset', 'image'];
  function inputType(el) { return (el.getAttribute('type') || 'text').toLowerCase(); }

  function roleOf(el, cs) {
    const given = (el.getAttribute('role') || '').split(/\s+/)[0].toLowerCase();
    if (given && given !== 'presentation' && given !== 'none') return given;
    switch (el.tagName) {
      case 'A': return el.hasAttribute('href') ? 'link' : '';
      case 'BUTTON': case 'SUMMARY': return 'button';
      case 'SELECT': return 'combobox';
      case 'TEXTAREA': return 'textbox';
      case 'IMG': return el.getAttribute('alt') ? 'img' : '';
      case 'H1': case 'H2': case 'H3': case 'H4': case 'H5': case 'H6': return 'heading';
      case 'INPUT': {
        const t = inputType(el);
        if (t === 'hidden') return '';
        if (BUTTON_TYPES.includes(t)) return 'button';
        if (t === 'checkbox' || t === 'radio') return t;
        if (t === 'range') return 'slider';
        if (t === 'file') return 'file';
        return 'textbox';
      }
    }
    if (el.getAttribute('draggable') === 'true') return 'draggable';
    if (cs.cursor === 'pointer' && !(el.parentElement && getComputedStyle(el.parentElement).cursor === 'pointer')) return 'clickable';
    return '';
  }

  function isSecret(el) {
    return el.tagName === 'INPUT' && (inputType(el) === 'password' || /password/i.test(el.getAttribute('autocomplete') || ''));
  }

  function nameOf(el, role) {
    const label = el.getAttribute('aria-label');
    if (label && norm(label)) return norm(label);
    const by = el.getAttribute('aria-labelledby');
    if (by) {
      const t = norm(by.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean).map(e => e.textContent).join(' '));
      if (t) return t;
    }
    if (el.labels && el.labels.length) {
      const t = norm(Array.from(el.labels).map(l => l.textContent).join(' '));
      if (t) return t;
    }
    if (el.tagName === 'INPUT' && BUTTON_TYPES.includes(inputType(el))) return norm(el.value) || norm(el.getAttribute('alt'));
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT') {
      return norm(el.getAttribute('placeholder')) || norm(el.getAttribute('title')) || norm(el.getAttribute('name')) || norm(el.id);
    }
    if (el.tagName === 'IMG') return norm(el.getAttribute('alt'));
    return norm(el.innerText) || norm(el.getAttribute('title'));
  }

  function valueOf(el) {
    if (isSecret(el)) return { value: '', state: '' };
    if (el.tagName === 'INPUT') {
      const t = inputType(el);
      if (t === 'checkbox' || t === 'radio') return { value: '', state: el.checked ? 'checked' : 'unchecked' };
      if (BUTTON_TYPES.includes(t)) return { value: '', state: '' };
      return { value: el.value || '', state: '' };
    }
    if (el.tagName === 'TEXTAREA') return { value: el.value || '', state: '' };
    if (el.tagName === 'SELECT') return { value: Array.from(el.selectedOptions).map(o => o.text).join(', '), state: '' };
    const checked = el.getAttribute('aria-checked');
    if (checked) return { value: '', state: checked === 'true' ? 'checked' : 'unchecked' };
    return { value: '', state: '' };
  }

  function ownText(el) {
    let t = '';
    for (const n of el.childNodes) if (n.nodeType === 3) t += n.nodeValue;
    return norm(t);
  }

  // The content of these is their name, so a text inside one is not listed again.
  const LABELLED = new Set(['button', 'link', 'tab', 'menuitem', 'checkbox', 'radio', 'heading', 'switch']);

  const els = [];
  const items = [];
  const listed = new Map();
  for (const el of document.body ? document.body.querySelectorAll('*') : []) {
    if (SKIP.has(el.tagName.toUpperCase()) || el.closest('svg, datalist')) continue;
    if (!visible(el)) continue;
    const cs = getComputedStyle(el);
    const role = roleOf(el, cs);
    let name = '';
    let rest = { value: '', state: '' };
    if (role) {
      name = nameOf(el, role);
      rest = valueOf(el);
    } else {
      const own = ownText(el);
      if (!own || (el.tagName === 'LABEL' && el.control)) continue;
      name = norm(el.innerText);
      let skip = false;
      for (let up = el.parentElement; up; up = up.parentElement) {
        const l = listed.get(up);
        if (l && (l.name === name || (LABELLED.has(l.role) && l.name.includes(name)))) { skip = true; break; }
      }
      if (skip) continue;
    }
    if (!name && !rest.value && !rest.state) continue;
    const idx = els.length;
    els.push(el);
    listed.set(el, { name, role });
    items.push({
      index: idx, role: role || 'text', name, value: rest.value, state: rest.state,
      disabled: el.disabled === true || el.getAttribute('aria-disabled') === 'true',
      secret: isSecret(el),
      acts: !!role,
    });
  }
  window.__semarRefs = els;
  return JSON.stringify({ url: location.origin + location.pathname, title: document.title, items });
})()
