export const $ = id => document.getElementById(id);

export const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;'
}[c]));

export const md = s => {
  if (!window.marked || !window.DOMPurify) return esc(s);
  let html = DOMPurify.sanitize(window.marked.parse(String(s ?? '')));
  return html.replace(/<pre>/gi, '<div class="code-wrapper"><button class="copy-btn" aria-label="Copy code" title="Copy code"><svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg></button><pre>').replace(/<\/pre>/gi, '</pre></div>');
};

let mdWorker;
let mdJobId = 0;
const mdResolvers = new Map();

function initWorker() {
  if (!mdWorker) {
    mdWorker = new Worker('/public/worker.js');
    mdWorker.onmessage = function(e) {
      const { id, html } = e.data;
      if (mdResolvers.has(id)) {
        mdResolvers.get(id)(html);
        mdResolvers.delete(id);
      }
    };
  }
}

export const mdAsync = (s) => {
  initWorker();
  return new Promise(resolve => {
    const id = ++mdJobId;
    mdResolvers.set(id, resolve);
    mdWorker.postMessage({ id, content: s });
  });
};

export function setupMarked() {
  if (window.marked && window.hljs && window.markedHighlight) {
    const { markedHighlight } = window.markedHighlight;
    window.marked.use(markedHighlight({
      langPrefix: 'hljs language-',
      highlight(code, lang) {
        if (lang && window.hljs.getLanguage(lang)) {
          try {
            return window.hljs.highlight(code, { language: lang }).value;
          } catch (e) {}
        }
        return esc(code);
      }
    }));
  } else {
    console.warn("Missing syntax highlighting deps:", { marked: !!window.marked, hljs: !!window.hljs, markedHighlight: !!window.markedHighlight });
  }
}

let _isNearBottom = true;
let _scrollTrackerSetup = false;

export function chatIsNearBottom(threshold = 90) {
  const c = document.getElementById('chat');
  if (!c) return false;
  
  if (!_scrollTrackerSetup) {
    _scrollTrackerSetup = true;
    let _scrollRaf = null;
    c.addEventListener('scroll', () => {
      if (_scrollRaf) return;
      _scrollRaf = requestAnimationFrame(() => {
        _isNearBottom = c.scrollHeight - c.clientHeight <= c.scrollTop + 120;
        _scrollRaf = null;
      });
    }, { passive: true });
    _isNearBottom = c.scrollHeight - c.clientHeight <= c.scrollTop + 120;
  }
  return _isNearBottom;
}

export function scrollChatToBottom(instant = false) {
  return new Promise(resolve => {
    requestAnimationFrame(() => {
      const c = document.getElementById('chat');
      if (!c) return resolve();
      if (instant) {
        const orig = c.style.scrollBehavior;
        c.style.scrollBehavior = 'auto';
        c.scrollTop = c.scrollHeight;
        requestAnimationFrame(() => {
          c.scrollTop = c.scrollHeight;
          c.style.scrollBehavior = orig;
          resolve();
        });
      } else {
        c.scrollTop = c.scrollHeight;
        resolve();
      }
    });
  });
}
