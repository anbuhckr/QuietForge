import { $, esc } from '../../utils/dom.js?v=1785573007908';
import { setEditorText, textOfEditor } from './InputBox.js?v=1785573007908';

export function mentions(text = textOfEditor()) {
  return [...new Set((text.match(/(^|\s)@([^\s]+)/g) || []).map(x => x.trim().slice(1)))]
}

export function chipType(m) {
  if (m.startsWith('uploads/')) return 'upload';
  if (['recent', 'diff', 'profile', 'skills', '*.py'].includes(m)) return 'special';
  return ''
}

export function renderChips(text = textOfEditor()) {
  const box = $('chips');
  const ms = mentions(text);
  box.innerHTML = '';
  ms.forEach(m => {
    const c = document.createElement('span');
    c.className = 'chip ' + chipType(m);
    c.innerHTML = `@${esc(m)} <button>×</button>`;
    c.querySelector('button').onclick = () => {
      const safe = m.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      setEditorText(textOfEditor().replace(new RegExp('(^|\\s)@' + safe + '(?=\\s|$)', 'g'), ' ').replace(/\s+/g, ' ').trim())
    };
    box.appendChild(c)
  })
}

export function updateBackdropHighlights() {
  const editor = $('editor');
  const highlights = $('editorHighlights');
  if (!editor || !highlights) return;

  let text = editor.value;
  // Handle trailing newline so scroll height matches perfectly
  if (text.endsWith('\n')) text += ' ';

  // Escape HTML
  let html = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

  // Highlight /commands
  html = html.replace(/(^|\s)(\/[a-zA-Z0-9_-]+)/g, '$1<span class="hl-slash">$2</span>');

  // Highlight @mentions (including trailing slash for folders)
  html = html.replace(/(^|\s)(@[a-zA-Z0-9_.-]+\/?)/g, (match, space, tag) => {
    return space + `<span class="${tag.endsWith('/') ? 'hl-folder' : 'hl-mention'}">${tag}</span>`;
  });

  highlights.innerHTML = html;
}

export function highlightMentions() {
  renderChips(textOfEditor());
  resizeEditor();
  updateBackdropHighlights();
}

export function currentMentionQuery() {
  const text = textOfEditor();
  const m = text.match(/(?:^|\s)([@/])([^\s@/]*)$/);
  return m ? { prefix: m[1], query: m[2] } : null
}
export async function fetchSuggestions(qObj) {
  if (!qObj) return [];
  const endpoint = qObj.prefix === '/' ? '/api/tools' : '/api/files';
  const r = await fetch(endpoint + '?q=' + encodeURIComponent(qObj.query || ''));
  if (!r.ok) return [];
  const items = await r.json();
  return (items || []).map(item => ({ ...item, prefix: qObj.prefix }));
}

export function renderSuggestions() {
  const box = $('suggestions');
  if (!window.suggestions.length) {
    box.classList.remove('open');
    return
  }
  box.innerHTML = '';
  suggestions.forEach((s, i) => {
    const d = document.createElement('div');
    d.className = 'suggestion' + (i === activeSuggestion ? ' active' : '');
    d.innerHTML = `<span class="path">${s.prefix}${esc(s.value)}</span><span class="type">${esc(s.type)} · ${esc(s.label)}</span>`;
    d.onmousedown = e => {
      e.preventDefault();
      chooseSuggestion(i)
    };
    box.appendChild(d)
  });
  box.classList.add('open')
}

export function chooseSuggestion(i) {
  const s = window.suggestions[i];
  if (!s) return;
  let t = textOfEditor();
  const safePrefix = s.prefix === '/' ? '\\/' : '@';
  t = t.replace(new RegExp('(^|\\s)' + safePrefix + '([^\\s@/]*)$'), (_, sp) => sp + s.prefix + s.value + ' ');
  setEditorText(t);
  suggestions = [];
  renderSuggestions()

  setTimeout(() => {
    const el = $('editor');
    el.focus();
    el.selectionStart = el.value.length;
    el.selectionEnd = el.value.length;
    el.scrollTop = el.scrollHeight;
  }, 10);
}
