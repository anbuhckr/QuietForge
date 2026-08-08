let importError = null;
try {
  importScripts(
    '/public/marked.min.js',
    '/public/index.umd.js',
    '/public/highlight.min.js'
  );
} catch(e) {
  importError = e;
}

const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
}[c]));

if (self.marked && self.hljs && self.markedHighlight) {
  const { markedHighlight } = self.markedHighlight;
  self.marked.use(markedHighlight({
    langPrefix: 'hljs language-',
    highlight(code, lang) {
      if (lang && self.hljs.getLanguage(lang)) {
        try {
          return self.hljs.highlight(code, { language: lang }).value;
        } catch (e) {}
      }
      return esc(code);
    }
  }));
}

self.onmessage = function(e) {
  const { id, content } = e.data;
  
  if (importError) {
    self.postMessage({ id, html: '<div style="color:red;font-weight:bold;padding:10px;">Worker Import Error: ' + esc(importError.toString()) + '</div><br>' + esc(content) });
    return;
  }
  
  if (!self.marked) {
    self.postMessage({ id, html: '<div style="color:red;font-weight:bold;padding:10px;">Worker Error: self.marked is undefined!</div><br>' + esc(content) });
    return;
  }
  
  try {
    let html = self.marked.parse(String(content ?? ''));
    if (html instanceof Promise) {
      self.postMessage({ id, html: '<div style="color:red;font-weight:bold;padding:10px;">Worker Error: marked.parse returned a Promise! (async: true?)</div><br>' + esc(content) });
      return;
    }
    html = html.replace(/<pre>/gi, '<div class="code-wrapper"><button class="copy-btn" aria-label="Copy code" title="Copy code"><svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg></button><pre>').replace(/<\/pre>/gi, '</pre></div>');
    self.postMessage({ id, html });
  } catch(err) {
    self.postMessage({ id, html: '<div style="color:red;font-weight:bold;padding:10px;">Worker Parse Error: ' + esc(err.toString()) + '</div><br>' + esc(content) });
  }
};
