window.viewArtifactByTitle = function (title) {
  const art = typeof currentArtifacts !== 'undefined' ? currentArtifacts.find(a => a.title === title) : null;
  if (art) viewArtifact(art);
};

const $ = id => document.getElementById(id);
const esc = s => String(s ?? '').replace(/[&<>]/g, c => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;'
} [c]));
const md = s => {
  if (!window.marked || !window.DOMPurify) return esc(s);
  let html = DOMPurify.sanitize(marked.parse(String(s ?? '')));
  return html.replace(/<pre>/gi, '<div class="code-wrapper"><button class="copy-btn" aria-label="Copy code" title="Copy code"><svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg></button><pre>').replace(/<\/pre>/gi, '</pre></div>');
};
let running = false,
  stopping = false,
  poll = null,
  suggestions = [],
  _notifQueued = false,
  activeSuggestion = 0,
  lastUserMsg = null,
  inlineLiveEl = null,
  pendingFolders = [],
  projectSelected = false,
  sseSource = null;
const featureLabels = {
  implementation_guard: 'Implementation guard',
  test_recovery: 'Test failure recovery',
  memory_context: 'Memory context',
  skills_context: 'Skills context',
  git_context: 'Git context',
  activity_telemetry: 'Activity telemetry',
  auto_profile: 'Auto project profile',
  persistent_toolset: 'Persistent toolset',
  use_native_tools: 'Use Native Tool Calling'
};
let features = JSON.parse(localStorage.getItem('qf_features') || '{}');
let intentMode = localStorage.getItem('qf_intent_mode');
if (intentMode === 'auto' || !intentMode) intentMode = 'build';
let totalTokens = {prompt: 0, completion: 0};
let inputPricePerM = 2.50;
let outputPricePerM = 10.00;

function _fmtTokens(n) {
  return n.toLocaleString();
}
function _fmtCost(cents) {
  return '$' + cents.toLocaleString(undefined, {minimumFractionDigits: 2, maximumFractionDigits: 2});
}
function _updateTokenDisplay() {
  const p = totalTokens.prompt || 0;
  const c = totalTokens.completion || 0;
  const costIn = (p / 1_000_000) * inputPricePerM;
  const costOut = (c / 1_000_000) * outputPricePerM;
  const el = document.getElementById('tokenDisplay');
  if (el) el.textContent = '\u25B2 ' + _fmtTokens(p) + ' (' + _fmtCost(costIn) + ') \u25BC ' + _fmtTokens(c) + ' (' + _fmtCost(costOut) + ')';
}

function mergedFeatures(defaults = {}) {
  return {
    ...(window.SERVER_FEATURES || {}),
    ...defaults,
    ...features
  }
}

// Notification sound: "You Would Be Glad To Know" by notificationsounds.com (CC BY)
function _playNotificationSound() {
  try {
    const a = new Audio('/public/notification.mp3');
    a.volume = 0.5;
    a.play().catch(() => {});
  } catch(e) {}
}

function textOfEditor() {
  return $('editor').value;
}

function setEditorText(t) {
  $('editor').value = t;
  renderChips(t);
  resizeEditor();
  if (typeof updateBackdropHighlights === 'function') updateBackdropHighlights();
}

function resizeEditor() {
  const e = $('editor');
  e.style.height = 'auto';
  e.style.height = Math.min(e.scrollHeight, 200) + 'px';
}

function mentions(text = textOfEditor()) {
  return [...new Set((text.match(/(^|\s)@([^\s]+)/g) || []).map(x => x.trim().slice(1)))]
}

function chipType(m) {
  if (m.startsWith('uploads/')) return 'upload';
  if (['recent', 'diff', 'profile', 'skills', '*.py'].includes(m)) return 'special';
  return ''
}

function renderChips(text = textOfEditor()) {
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

function updateBackdropHighlights() {
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

function highlightMentions() {
  renderChips(textOfEditor());
  resizeEditor();
  updateBackdropHighlights();
}

function currentMentionQuery() {
  const text = textOfEditor();
  const m = text.match(/(?:^|\s)([@/])([^\s@/]*)$/);
  return m ? { prefix: m[1], query: m[2] } : null
}
async function fetchSuggestions(qObj) {
  if (!qObj) return [];
  const endpoint = qObj.prefix === '/' ? '/api/tools' : '/api/files';
  const r = await fetch(endpoint + '?q=' + encodeURIComponent(qObj.query || ''));
  if (!r.ok) return [];
  const items = await r.json();
  return items.map(item => ({ ...item, prefix: qObj.prefix }));
}

function renderSuggestions() {
  const box = $('suggestions');
  if (!suggestions.length) {
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

function chooseSuggestion(i) {
  const s = suggestions[i];
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

$('editor').addEventListener('scroll', () => {
  const backdrop = $('editorBackdrop');
  if (backdrop) backdrop.scrollTop = $('editor').scrollTop;
});

$('editor').addEventListener('input', async () => {
  highlightMentions();
  updateSendStopButtons();
  const q = currentMentionQuery();
  if (q !== null) {
    suggestions = await fetchSuggestions(q);
    activeSuggestion = 0;
    renderSuggestions()
  } else {
    suggestions = [];
    renderSuggestions()
  }
});
$('editor').addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey && !suggestions.length) {
    e.preventDefault();
    send()
  } else if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
    e.preventDefault();
    send()
  }
  if (suggestions.length) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      activeSuggestion = (activeSuggestion + 1) % suggestions.length;
      renderSuggestions()
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      activeSuggestion = (activeSuggestion - 1 + suggestions.length) % suggestions.length;
      renderSuggestions()
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      chooseSuggestion(activeSuggestion)
    }
  }
});
if (window.marked && window.hljs && window.markedHighlight) {
} else {
  console.warn("Missing syntax highlighting deps:", { marked: !!window.marked, hljs: !!window.hljs, markedHighlight: !!window.markedHighlight });
}
if (window.marked && window.hljs && window.markedHighlight) {
  const {
    markedHighlight
  } = window.markedHighlight;
  marked.use(markedHighlight({
    langPrefix: 'hljs language-',
    highlight(code, lang) {
      if (lang && hljs.getLanguage(lang)) {
        return hljs.highlight(code, { language: lang }).value;
      }
      return hljs.highlightAuto(code).value;
    }
  }));
}

function addMsg(role, txt, opts) {
  const isUser = role.toLowerCase() === 'user';
  let chat = document.getElementById('chat');
  let turnGroup = chat.lastElementChild;
  if (isUser || !turnGroup || !turnGroup.classList.contains('chat-turn')) {
    turnGroup = document.createElement('div');
    turnGroup.className = 'chat-turn';
    chat.appendChild(turnGroup);
  }

  const d = document.createElement('div');
  d.className = 'msg ' + (isUser ? 'user' : 'agent');
  if (opts && opts.message_id) d.dataset.messageId = opts.message_id;
  let content = esc(txt);
  if (role.toLowerCase() === 'user') {
    content = content.replace(/(^|\s)(\/[a-zA-Z0-9_-]+)/g, '$1<span class="hl-slash">$2</span>');
    content = content.replace(/(^|\s)(@[a-zA-Z0-9_.-]+\/?)/g, (match, space, tag) => {
      return space + `<span class="${tag.endsWith('/') ? 'hl-folder' : 'hl-mention'}">${tag}</span>`;
    });
  } else if (role.toLowerCase() === 'agent' && window.marked) {
    let cleaned = txt.replace(/\[(?:Compat )?Tool Call\]\nTool: (.*)\nTool Input: ([\s\S]*?)\n\[\/Tool Call\]/g, (m, t, j) => {
      try {
        let a = JSON.parse(j);
        let s = [];
        for (let k in a) {
          let v = a[k];
          if (typeof v === 'string' && (v.includes('/') || v.includes('\\')) && !v.includes('\n')) {
            v = v.split(/[\\/]/).pop()
          }
          if (typeof v === 'object' && v !== null) {
            v = JSON.stringify(v);
            if (v.length > 50) v = v.substring(0, 50) + '...';
          }
          s.push(`${k}="${v}"`)
        }
        return `\n<div class="inline-live flat-live" style="margin: 8px 0;"><div class="live-log"><div class="live-entry markdown-body action-entry" style="animation: none;"><p>⚙️ ${t} ${s.join(' ')}</p></div></div></div>\n`
      } catch (e) {
        return m
      }
    });
    // Catch LLM hallucinations where it imitates the prompt's "> ⚙ ..." format
    cleaned = cleaned.replace(/^>\s*⚙\s*(.*)$/gm, (m, rest) => {
      return `\n<div class="inline-live flat-live"><div class="live-log"><div class="live-entry markdown-body action-entry" style="animation: none;"><p>⚙️ ${rest}</p></div></div></div>\n`
    });
    // Parse <think> tags from DeepSeek R1 and similar models
    cleaned = cleaned.replace(/<think>([\s\S]*?)<\/think>/g, (m, thought) => {
      return `\n<details class="llm-thought"><summary>🧠 Thought Process</summary><div class="llm-thought-content">\n\n${thought}\n\n</div></details>\n`;
    });
    content = md(cleaned)
  }
  d.innerHTML = `<div class="label">${esc(role)}</div><div class="bubble markdown-body">${content}</div>`;
  if (isUser && opts && opts.snapshot) {
    const btn = document.createElement('button');
    btn.className = 'revert-btn';
    btn.title = 'Revert workspace to this point';
    btn.textContent = '\u21B6';
    btn.dataset.messageId = opts.message_id;
    btn.onclick = async () => {
      const r = await fetch('/api/chat/revert', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({message_id: opts.message_id})
      });
      if (r.ok) {
        const data = await r.json();
        renderSession(data.session_log || []); // Render the log immediately
        refresh(); // Call refresh in the background just to update tokens and artifacts
      }
    };
    d.querySelector('.label').after(btn);
  }
  turnGroup.appendChild(d);
  if (isUser) {
    lastUserMsg = d;
    inlineLiveEl = null;
    currentLiveContainer = null
  }
  updateStickyPrompts();
  $('chat').scrollTop = $('chat').scrollHeight;
  return d
}

function createLiveTextRow(text) {
  const row = document.createElement('details');
  row.className = 'inline-live';
  row.open = false;
  row.dataset.isToolBlock = 'false';
  row.dataset.structuredLive = 'true';
  row.innerHTML = `<summary style="cursor:pointer; display:flex; align-items:center;"><span style="margin-right:8px; font-size:10px; opacity:0.7;">▼</span><span class="livetext" style="font-weight:600;"></span><span class="timer" style="margin-left:8px; opacity:0.6; font-variant-numeric: tabular-nums;"></span></summary><div class="live-log" style="margin-top:8px; padding-left:16px; font-size:13px; color:var(--text-muted); display:flex; flex-direction:column; gap:4px; font-family:var(--font-mono); max-height:200px; overflow-y:auto; overflow-x:hidden; scrollbar-width:thin; scrollbar-color: #3f424b transparent;"></div>`;
  row.querySelector('.livetext').textContent = String(text || '').substring(0, 220);
  row.addEventListener('toggle', () => {
    const arrow = row.querySelector('summary span');
    if (arrow) arrow.style.transform = row.open ? 'rotate(0deg)' : 'rotate(-90deg)';
  });
  return row;
}

function renderPersistedRunMeta(meta) {
  const events = Array.isArray(meta && meta.live_events) ? meta.live_events : [];
  if (!events.length) return;
  const container = document.createElement('div');
  container.className = 'live-container compacted';
  container.dataset.compacted = 'true';
  container.style.display = 'flex';
  container.style.flexDirection = 'column';
  container.style.gap = '6px';
  container.style.marginBottom = '10px';
  container.style.marginTop = '2px';

  const eventsToCompact = [];
  const eventsToKeep = [];

  events.forEach(evt => {
    const rawText = typeof evt === 'string' ? evt : (evt.text || evt.event || evt.message || evt.error);
    if (!rawText) return;
    let kind = (typeof evt === 'string' ? 'activity' : evt.type) || 'activity';
    const lower = rawText.toLowerCase();
    if (kind === 'error' || lower.startsWith('created artifact:') || lower.startsWith('artifact ')) {
      eventsToKeep.push(evt);
    } else {
      eventsToCompact.push(evt);
    }
  });

  if (eventsToCompact.length > 0) {
    const details = document.createElement('details');
    details.className = 'inline-live live-compact';
    details.open = false;
    details.innerHTML = `<summary><span class="compact-label">Worked for ${esc(formatRunDuration(Number(meta.duration_ms || 0)))}</span><span class="compact-arrow">›</span></summary><div class="compact-log"></div>`;
    const log = details.querySelector('.compact-log');
    let currentInlineEl = null;

    eventsToCompact.forEach(evt => {
      let rawText = typeof evt === 'string' ? evt : (evt.text || evt.event || evt.message || evt.error);
      let kind = (typeof evt === 'string' ? 'activity' : evt.type) || 'activity';
      if (kind === 'done' || (!['think', 'action'].includes(kind) && isNoisyLiveText(rawText))) return;
      if (kind !== 'think' && kind !== 'action') kind = 'action';

      let skipAppend = false;
      if (kind === 'action' && rawText) {
        if (rawText.startsWith('Executing: ')) {
          rawText = rawText.replace(/^Executing:\s*/, '⚙️ ');
        } else if (rawText.startsWith('✓ ') || rawText.startsWith('✗ ')) {
          const match = rawText.match(/^[✓✗]\s+([a-zA-Z0-9_]+)/);
          if (match && log) {
            const toolName = match[1];
            const detailsNodes = [...log.querySelectorAll('details.inline-live')];
            for (let i = detailsNodes.length - 1; i >= 0; i--) {
              const d = detailsNodes[i];
              const summarySpan = d.querySelector('summary .livetext');
              if (summarySpan && summarySpan.textContent.startsWith('⚙️ ' + toolName + ' ')) {
                const isSuccess = rawText.startsWith('✓');
                let updatedText = summarySpan.textContent.replace('⚙️ ', isSuccess ? '✓ ' : '✗ ');
                if (!isSuccess) {
                  const errStr = rawText.substring(`✗ ${toolName}: `.length);
                  updatedText += ': ' + errStr;
                }
                summarySpan.textContent = updatedText;
                const entry = d.querySelector('.live-entry');
                if (entry) {
                  entry.textContent = updatedText;
                }
                skipAppend = true;
                break;
              }
            }
          }
        }
      }

      if (skipAppend) return;

      let isStructuredLive = true;
      let title = '';

      const liveLabels = {
        think: 'Thinking',
        action: 'Action',
        response: 'Finalizing'
      };

      if (kind === 'action') {
        title = rawText.split('\n')[0].substring(0, 100);
      } else {
        title = (liveLabels[kind] || 'Thinking') + '...';
      }

      let forceNew = false;
      if (kind === 'think' || kind === 'action') {
        forceNew = true;
      }

      if (forceNew || !currentInlineEl) {
        currentInlineEl = document.createElement('details');
        currentInlineEl.className = 'inline-live flat-live';
        currentInlineEl.open = true;
        currentInlineEl.dataset.structuredLive = 'true';
        if (kind !== 'action') {
          currentInlineEl.dataset.liveKind = kind;
        }
        currentInlineEl.dataset.hasAction = (kind === 'action') ? 'true' : 'false';
        currentInlineEl.innerHTML = `<summary><span class="livetext">${esc(title)}</span><span class="timer"></span></summary><div class="live-log"></div>`;
        log.appendChild(currentInlineEl);
      } else {
        if (kind === 'action') currentInlineEl.dataset.hasAction = 'true';
      }

      const logContainer = currentInlineEl.querySelector('.live-log');
      if (logContainer) {
        const entry = document.createElement('div');
        entry.className = 'live-entry markdown-body';
        if (kind === 'action') entry.classList.add('action-entry');
        if (kind === 'think') {
          entry.textContent = rawText;
          entry.style.cssText = 'white-space: nowrap !important; overflow: hidden !important; text-overflow: ellipsis !important;';
        } else {
          entry.innerHTML = typeof DOMPurify !== 'undefined' ? DOMPurify.sanitize(marked.parse(rawText)) : esc(rawText);
        }
        logContainer.appendChild(entry);
      }
    });

    if (log.children.length > 0) {
      container.appendChild(details);
    }
  }

  eventsToKeep.forEach(evt => {
    const rawText = typeof evt === 'string' ? evt : (evt.text || evt.event || evt.message || evt.error);
    let kind = (typeof evt === 'string' ? 'activity' : evt.type) || 'activity';
    const row = document.createElement('details');
    row.className = 'inline-live flat-live';
    row.open = true;
    row.dataset.structuredLive = 'true';
    if (kind === 'error') {
      row.classList.add('error');
      row.innerHTML = `<summary><span class="livetext" style="color: #ff4d4d;">❌ ${esc(rawText)}</span><span class="timer"></span></summary><div class="live-log"></div>`;
    } else {
      row.innerHTML = `<summary><span class="livetext">${esc(rawText)}</span><span class="timer"></span></summary><div class="live-log"></div>`;
    }
    container.appendChild(row);
  });

  if (container.children.length > 0) {
    lastUserMsg ? lastUserMsg.insertAdjacentElement('afterend', container) : $('chat').appendChild(container);
  }
}

function renderSession(log = []) {
  $('chat').innerHTML = '';
  lastUserMsg = null;
  inlineLiveEl = null;
  currentLiveContainer = null;
  if (!log.length) {
    addMsg('System', 'Ready. Mention workspace context with @todo.py, @recent, @diff.');
    return
  }
  log.forEach(m => {
    if (String(m.role || 'Agent').toLowerCase() !== 'user' && m.run_meta) {
      renderPersistedRunMeta(m.run_meta);
    }
    const msgEl = addMsg(m.role || 'Agent', m.content || '', (m.role === 'User' && m.snapshot) ? { snapshot: m.snapshot, message_id: m.id } : undefined);
    if (String(m.role || 'Agent').toLowerCase() !== 'user' && m.run_meta && m.run_meta.workspace_changes) {
      let changedFiles = [];
      const wc = m.run_meta.workspace_changes;
      if (wc.created) changedFiles.push(...wc.created);
      if (wc.modified) changedFiles.push(...wc.modified);
      
      if (changedFiles.length > 0 && window.latestArtifacts) {
        let relevantDiffs = matchingDiffArtifactsForChangedFiles(changedFiles);
        if (relevantDiffs.length > 0) {
           renderDiffReviewWidget(msgEl, relevantDiffs);
        }
      }
    }
  });
  updateStickyPrompts();
}
let runStartTime = 0,
  runTimerInterval = null;
let inlineLiveLog = null;

function chatIsNearBottom(threshold = 90) {
  const c = $('chat');
  return c.scrollHeight - c.clientHeight <= c.scrollTop + threshold
}

function scrollChatToBottom() {
  const c = $('chat');
  c.scrollTop = c.scrollHeight
}
let currentLiveContainer = null;

function ensureInlineLive(forceNew = false, title = "Processing...") {
  if (!forceNew && inlineLiveEl && document.body.contains(inlineLiveEl)) return inlineLiveEl;
  if (forceNew && inlineLiveEl) {
    // Keep it expanded during the run. compactLiveTranscript will collapse them at the end.
    inlineLiveEl.classList.remove('running');
  }

  if (!currentLiveContainer || !document.body.contains(currentLiveContainer)) {
    currentLiveContainer = document.createElement('div');
    currentLiveContainer.className = 'live-container';
    currentLiveContainer.style.display = 'flex';
    currentLiveContainer.style.flexDirection = 'column';
    currentLiveContainer.style.gap = '0';
    currentLiveContainer.style.marginBottom = '16px';
    currentLiveContainer.style.marginTop = '-8px';
    lastUserMsg ? lastUserMsg.insertAdjacentElement('afterend', currentLiveContainer) : $('chat').appendChild(currentLiveContainer);
  }

  const d = document.createElement('details');
  d.className = 'inline-live';
  d.style.marginTop = '0';
  d.open = true;
  d.innerHTML = `<summary style="cursor:pointer; display:flex; align-items:center;"><span style="margin-right:8px; font-size:10px; opacity:0.7;">▼</span><span class="livetext" style="font-weight:600;"></span><span class="timer" style="margin-left:8px; opacity:0.6; font-variant-numeric: tabular-nums;"></span></summary><div class="live-log" style="margin-top:2px; padding-left:0; font-size:13px; color:var(--text-muted); display:flex; flex-direction:column; gap:2px; font-family:var(--font-mono); max-height:200px; overflow-y:auto; overflow-x:hidden; scrollbar-width:thin; scrollbar-color: #3f424b transparent;"></div>`;

  // Add rotation listener for the custom arrow
  d.addEventListener('toggle', (e) => {
    const arrow = d.querySelector('summary span');
    if (arrow) arrow.style.transform = d.open ? 'rotate(0deg)' : 'rotate(-90deg)';
  });

  currentLiveContainer.appendChild(d);
  inlineLiveEl = d;
  inlineLiveLog = d.querySelector('.live-log');
  return d;
}

function updateTimer() {
  if (!running || !inlineLiveEl) return;
  const s = Math.floor((Date.now() - runStartTime) / 1000);
  const tEl = inlineLiveEl.querySelector('.timer');
  if (tEl) tEl.textContent = `[${Math.floor(s/60).toString().padStart(2,'0')}:${(s%60).toString().padStart(2,'0')}]`;
}

function formatRunDuration(ms) {
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return rest ? `${minutes}m ${rest}s` : `${minutes}m`;
}

function compactLiveTranscript(durationMs) {
  if (!currentLiveContainer || currentLiveContainer.dataset.compacted === 'true') return;
  const rowsToCompact = [];
  const rowsToKeep = [];
  
  [...currentLiveContainer.children].forEach(el => {
    if (!el.classList || !el.classList.contains('inline-live')) return;
    
    // Check if it's "Created artifact:"
    const lt = el.querySelector('.livetext');
    const text = (lt ? lt.textContent : '').trim().toLowerCase();
    if (text.startsWith('created artifact:') || text.startsWith('artifact ')) {
      rowsToKeep.push(el);
      return;
    }
    
    // Skip generic "Thinking..."/"Processing..." spinners that have no real timeline content
    if (!el.dataset.structuredLive && !el.dataset.hasAction) {
      if (!text || text === 'processing...' || text.startsWith('thinking')) {
        if (el.parentNode) el.parentNode.removeChild(el);
        return;
      }
    }
    rowsToCompact.push(el);
  });

  if (!rowsToCompact.length && !rowsToKeep.length) {
    if (currentLiveContainer.parentNode) currentLiveContainer.parentNode.removeChild(currentLiveContainer);
    currentLiveContainer = null;
    return;
  }

  currentLiveContainer.innerHTML = '';

  if (rowsToCompact.length > 0) {
    const details = document.createElement('details');
    details.className = 'inline-live live-compact';
    details.open = false;
    details.innerHTML = `<summary><span class="compact-label">Worked for ${esc(formatRunDuration(durationMs))}</span><span class="compact-arrow">›</span></summary><div class="compact-log"></div>`;
    const log = details.querySelector('.compact-log');
    rowsToCompact.forEach(row => {
      row.classList.remove('running');
      row.classList.add('flat-live');
      row.open = true;
      log.appendChild(row);
    });
    currentLiveContainer.appendChild(details);
    inlineLiveEl = details;
    inlineLiveLog = log;
  }

  rowsToKeep.forEach(row => {
    row.classList.remove('running');
    currentLiveContainer.appendChild(row);
  });

  currentLiveContainer.dataset.compacted = 'true';
  currentLiveContainer.classList.add('compacted');
  inlineLiveEl = null;
  inlineLiveLog = null;
}

function isNoisyLiveText(text) {
  const t = String(text || '').trim().toLowerCase();
  return !t ||
    t.startsWith('auto-summarizing') ||
    t.startsWith('thinking') ||
    t.startsWith('building prompt') ||
    t.startsWith('tool cache hit') ||
    t.startsWith('phase:') ||
    t.startsWith('reading file:') ||
    t.startsWith('reading ') ||
    t.startsWith('scanning workspace') ||
    t.startsWith('searching codebase:') ||
    t.startsWith('semantic search:') ||
    t.startsWith('smart context search:') ||
    t.startsWith('deterministic verification') ||
    t.startsWith('verification strategy') ||
    t.startsWith('code reviewer checking') ||
    t.startsWith('workspace created:') ||
    t.startsWith('workspace modified:') ||
    t.startsWith('continuation attempt') ||
    t.startsWith('sub-agent') ||
    t.includes('tool budget exceeded') ||
    t.includes('continuation budget exceeded') ||
    t.includes('loop guard blocked') ||
    t.includes('you already called');
}

function pruneNoisyLiveEntries() {
  if (!inlineLiveLog) return;
  [...inlineLiveLog.children].forEach(entry => {
    if (isNoisyLiveText(entry.dataset.rawText || entry.textContent)) entry.remove();
  });
}

function updateInlineLive(text, state = 'running', opts = {}) {
  const shouldFollow = chatIsNearBottom();
  const rawText = String(text || '');
  const liveKind = opts.kind || '';
  const liveLabels = {
    think: 'Thinking',
    action: 'Action',
    response: 'Response'
  };
  const isStructuredLive = !!liveLabels[liveKind];
  const isToolResult = /^\[[0-9.]+s\]/.test(rawText);

  const pathRegex = /(?:[A-Za-z]:[\\/][^\s"']+|[\\/][^\s"']+|\.env\b|[a-zA-Z0-9_.-]+\.(?:py|js|ts|jsx|tsx|css|html|json|md|txt|csv|yaml|yml|sh|bash|cpp|c|h|hpp|rs|go|java|xml|ini|cfg|conf)\b)/gi;
  let compactText = (isNoisyLiveText(rawText) && !isStructuredLive ? 'Thinking' : rawText).replace(pathRegex, m => (m.includes('/') || m.includes('\\')) ? m.split(/[\\/]/).pop() : m);

  let title = "Processing...";
  if (isStructuredLive) {
    if (liveKind === 'action') {
      title = compactText.split('\n')[0].substring(0, 100);
    } else {
      title = liveLabels[liveKind] + '...';
    }
  } else if (isToolResult) {
    title = rawText.split('\n')[0].replace(/\*\*/g, '').replace(/^\[[0-9.]+s\]\s*/, '').substring(0, 80);
  } else if (!isNoisyLiveText(rawText)) {
    title = rawText.substring(0, 50);
  }

  let forceNew = false;
  if (isStructuredLive) {
    if (liveKind === 'think') {
      // Reuse existing placeholder from processStatus instead of force-new
      if (inlineLiveEl) {
        const lt = inlineLiveEl.querySelector('.livetext');
        const t = lt ? lt.textContent.trim().toLowerCase() : '';
        forceNew = !(t === 'thinking' || t === 'processing...');
      } else {
        forceNew = true;
      }
    }
    // Actions stay inside the current think block (no separate dot)
  } else if (isToolResult) {
    forceNew = false;
    if (inlineLiveEl) inlineLiveEl.dataset.hasToolResult = 'true';
  } else {
    if (inlineLiveEl && inlineLiveEl.dataset.hasToolResult === 'true') {
      forceNew = true;
    }
  }

  const d = ensureInlineLive(forceNew, title);
  if (forceNew) {
    d.dataset.hasToolResult = 'false';
    d.dataset.liveKind = liveKind || '';
    d.dataset.hasAction = 'false';
  }
  
  if (isStructuredLive) {
    d.dataset.structuredLive = 'true';
    if (forceNew || liveKind === 'think') d.dataset.liveKind = liveKind;
    if (liveKind === 'action') d.dataset.hasAction = 'true';
    d.classList.add('flat-live');
  }

  if (state === 'running') {
    d.classList.add('running')
  } else {
    d.classList.remove('running')
  }
  if (rawText.includes('❌')) {
    d.classList.add('error');
    d.querySelector('.livetext').style.color = '#ff4d4d';
  }

  if (isStructuredLive) {
    d.querySelector('.livetext').textContent = title;
    d.open = true;
  } else if (!isToolResult) {
    d.querySelector('.livetext').textContent = compactText;
    d.open = true;
  }

  pruneNoisyLiveEntries();
  
  // Custom logic to handle action deduplication and formatting
  let finalRawText = rawText;
  let skipAppend = false;
  if (liveKind === 'action' && finalRawText) {
    if (finalRawText.startsWith('Executing: ')) {
      finalRawText = finalRawText.replace(/^Executing:\s*/, '⚙️ ');
    } else if (finalRawText.startsWith('✓ ') || finalRawText.startsWith('✗ ')) {
      const match = finalRawText.match(/^[✓✗]\s+([a-zA-Z0-9_]+)/);
      if (match && inlineLiveLog) {
        const toolName = match[1];
        const actionNodes = [...inlineLiveLog.querySelectorAll('.action-entry')];
        for (let i = actionNodes.length - 1; i >= 0; i--) {
          const node = actionNodes[i];
          if (node.textContent.startsWith('⚙️ ' + toolName + ' ')) {
            const isSuccess = finalRawText.startsWith('✓');
            let updatedText = node.textContent.replace('⚙️ ', isSuccess ? '✓ ' : '✗ ');
            if (!isSuccess) {
              const errStr = finalRawText.substring(`✗ ${toolName}: `.length);
              updatedText += ': ' + errStr;
            }
            // Update node
            node.textContent = updatedText;
            skipAppend = true;
            break;
          }
        }
      }
    }
  }

  if (!skipAppend && finalRawText && (!isNoisyLiveText(finalRawText) || isStructuredLive) && opts.log !== false) {
    const entry = document.createElement('div');
    entry.className = 'live-entry markdown-body';
    if (liveKind === 'action') entry.classList.add('action-entry');
    if (liveKind === 'think') {
      entry.textContent = finalRawText;
      entry.style.cssText = 'white-space: nowrap !important; overflow: hidden !important; text-overflow: ellipsis !important;';
    } else {
      try {
        entry.innerHTML = DOMPurify.sanitize(marked.parse(finalRawText));
      } catch (e) {
        entry.textContent = finalRawText;
      }
    }
    
    if (isStructuredLive) {
      if (inlineLiveLog) {
        if (liveKind === 'think') {
          inlineLiveLog.innerHTML = '';
          inlineLiveLog.scrollTop = 0;
        }
        inlineLiveLog.appendChild(entry);
        // Auto-scroll to bottom for actions so latest is visible (thought stays sticky at top via CSS)
        if (liveKind !== 'think' && inlineLiveLog.scrollHeight - inlineLiveLog.clientHeight <= inlineLiveLog.scrollTop + 100) {
          inlineLiveLog.scrollTop = inlineLiveLog.scrollHeight;
        }
      }
    } else if (isToolResult) {
      // Do nothing - user explicitly requested to hide massive tool results from the UI to reduce clutter
    } else if (inlineLiveLog) {
      // Activity log entries: scroll to bottom if user was near bottom
      if (inlineLiveLog.scrollHeight - inlineLiveLog.clientHeight <= inlineLiveLog.scrollTop + 50) {
        inlineLiveLog.scrollTop = inlineLiveLog.scrollHeight;
      }
    }
  }
  if (shouldFollow) scrollChatToBottom();
}

function updateSendStopButtons() {
  const editorHasText = textOfEditor().trim() !== '';
  if (running) {
      $('send').style.display = editorHasText ? 'flex' : 'none';
      $('stopBtn').style.display = editorHasText ? 'none' : 'flex';
      $('send').disabled = !projectSelected || stopping;
      $('stopBtn').disabled = stopping;
  } else {
      $('send').style.display = 'flex';
      $('stopBtn').style.display = 'none';
      $('send').disabled = !projectSelected;
  }
}

function setRunningState(isRunning, isStopping = false) {
  if (isRunning && !running) { window._newDiffArtifacts = []; _notifQueued = false; }
  running = isRunning;
  stopping = isStopping;
  document.body.classList.toggle('running', isRunning);
  $('editor').disabled = !projectSelected;
  $('attachBtn').disabled = isRunning || !projectSelected;
  updateSendStopButtons();
  if (isStopping) updateInlineLive('Stopping', 'stopping');
  if (isRunning && !isStopping) {
    if (!runStartTime) {
      runStartTime = Date.now()
    }
    if (!runTimerInterval) {
      runTimerInterval = setInterval(updateTimer, 1000);
      updateTimer()
    }
  } else if (!isRunning) {
    const elapsedMs = runStartTime ? Date.now() - runStartTime : 0;
    clearInterval(runTimerInterval);
    runTimerInterval = null;
    if (inlineLiveEl) {
      inlineLiveEl.open = false;
      inlineLiveEl.classList.remove('running');
      const tEl = inlineLiveEl.querySelector('.timer');
      if (tEl) tEl.textContent = '';
    }
    compactLiveTranscript(elapsedMs);
    runStartTime = 0;
  }
}
async function readJsonOrText(r) {
  const t = await r.text();
  try {
    return JSON.parse(t)
  } catch {
    return {
      error: t.includes('<html') || t.includes('Traceback') ? 'Internal Server Error' : t || r.statusText
    }
  }
}

function renderProjects(projects = []) {
  const box = $('projectList');
  box.innerHTML = '';
  if (!projects.length) {
    const empty = document.createElement('div');
    empty.className = 'empty-projects';
    empty.innerHTML = '<div class="empty-title">No Projects</div><div class="empty-text">Select a directory to begin.</div>';
    box.appendChild(empty);
    return
  }
  projects.forEach(p => {
    const d = document.createElement('div');
    d.className = 'project ' + (p.active ? 'active' : '');
    d.innerHTML = `<div class="project-title"><span class="project-name">${esc(p.name)}</span><button class="project-delete" title="Remove workspace">×</button></div><div class="project-path">${esc(p.path)}</div><div class="chats"></div>`;
    d.querySelector('.project-title').onclick = () => selectProject(p.path);
    const delBtn = d.querySelector('.project-delete');
    if (delBtn) delBtn.onclick = (e) => removeProject(p.path, e);
    const chats = d.querySelector('.chats');
    const convs = Array.isArray(p.conversations) ? p.conversations : [];
    if (!convs.length) {
      const row = document.createElement('div');
      row.className = 'chatitem muted';
      row.innerHTML = '<span>↳</span><span>No conversations</span>';
      chats.appendChild(row)
    } else {
      convs.forEach(c => {
        const row = document.createElement('div');
        row.className = 'chatitem';
        row.innerHTML = `<span class="chat-title" style="flex:1; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; cursor:pointer;">↳ ${esc(c.title)}</span><button class="chat-delete" title="Delete conversation" style="background:transparent; border:none; color:var(--text-muted); cursor:pointer; padding:0 4px; border-radius:4px;">×</button>`;
        row.querySelector('.chat-title').onclick = async (e) => {
          e.stopPropagation();
          const r = await fetch('/api/chat/switch', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json'
            },
            body: JSON.stringify({
              id: c.id
            })
          });
          const d = await r.json();
          if (d.ok) renderSession(d.session_log);
          await refresh();
          document.querySelector('.col.left').classList.remove('open');
          document.body.classList.remove('drawer-open');
        };
        const chatDelBtn = row.querySelector('.chat-delete');
        if (chatDelBtn) chatDelBtn.onclick = async (e) => {
          e.stopPropagation();
          const r = await fetch('/api/chat/delete', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json'
            },
            body: JSON.stringify({
              id: c.id
            })
          });
          const d = await r.json();
          if (d.ok) renderSession(d.session_log);
          await refresh()
        };
        chats.appendChild(row)
      })
    }
    box.appendChild(d)
  })
}

// SVG Icons mapping
const icons = {
  user: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"></path><circle cx="12" cy="7" r="4"></circle></svg>',
  code: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"></circle><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"></path></svg>',
  folder: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>',
  db: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><ellipse cx="12" cy="5" rx="9" ry="3"></ellipse><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"></path><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"></path></svg>',
  error: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>',
  brain: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9.5 2A2.5 2.5 0 0 1 12 4.5v15a2.5 2.5 0 0 1-4.96.44 2.5 2.5 0 0 1-2.96-3.08 3 3 0 0 1-.34-5.58 2.5 2.5 0 0 1 1.32-4.24 2.5 2.5 0 0 1 1.98-3A2.5 2.5 0 0 1 9.5 2Z"></path><path d="M14.5 2A2.5 2.5 0 0 0 12 4.5v15a2.5 2.5 0 0 0 4.96.44 2.5 2.5 0 0 0 2.96-3.08 3 3 0 0 0 .34-5.58 2.5 2.5 0 0 0-1.32-4.24 2.5 2.5 0 0 0-1.98-3A2.5 2.5 0 0 0 14.5 2Z"></path></svg>',
  search: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line></svg>',
  default: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle></svg>'
};

function determineIcon(text) {
  text = text.toLowerCase();
  if (text.includes("error") || text.includes("fail") || text.includes("traceback")) return {
    type: 'error',
    svg: icons.error,
    title: "Error"
  };
  if (text.includes("user") || text.includes("request")) return {
    type: 'user',
    svg: icons.user,
    title: "User"
  };
  if (text.includes("sub-agent") || text.includes("orchestrating")) return {
    type: 'brain',
    svg: icons.brain,
    title: "Sub-Agent Active"
  };
  if (text.includes("semantic") || text.includes("search")) return {
    type: 'search',
    svg: icons.search,
    title: "Knowledge Retrieval"
  };
  if (text.includes("project") || text.includes("workspace") || text.includes("file")) return {
    type: 'folder',
    svg: icons.folder,
    title: "Project Activity"
  };
  if (text.includes("dependenc") || text.includes("install") || text.includes("database")) return {
    type: 'db',
    svg: icons.db,
    title: "System Update"
  };
  if (text.includes("refactor") || text.includes("code") || text.includes("fix") || text.includes("patch") || text.includes("checkpoint")) return {
    type: 'code',
    svg: icons.code,
    title: "Code Modification"
  };
  return {
    type: 'default',
    svg: icons.default,
    title: "System Event"
  };
}

function timeAgo(dateString) {
  if (!dateString) return "just now";
  let dStr = dateString;
  if (/^\d{2}:\d{2}:\d{2}$/.test(dateString)) {
    const today = new Date().toISOString().split('T')[0];
    dStr = `${today}T${dateString}`;
  }
  const date = new Date(dStr);
  const seconds = Math.floor((new Date() - date) / 1000);
  if (isNaN(seconds) || seconds < 0 || seconds > 86400) return dateString;
  if (seconds < 60) return `just now`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
}

function renderBackendDiagnostics(diag) {
  diag = diag || {};
  if ($('diagBackend')) $('diagBackend').textContent = diag.selected_backend || '-';
  if ($('diagReason')) $('diagReason').textContent = diag.reason || 'No backend diagnostic reason available.';
}

function renderModelTestResult(d) {
  if (!$('diagResults')) return;
  $('diagResults').innerHTML = '';
  const c = document.createElement('div');
  c.className = 'diag-check' + (d.ok ? ' ok' : ' bad');
  c.innerHTML = `<span>${d.ok?'OK':'ERR'}</span><div><strong>model connection (${esc(d.provider)})</strong><small>${esc(d.detail||'')}</small></div>`;
  $('diagResults').appendChild(c);
  const r = document.createElement('div');
  r.className = 'diag-elapsed';
  r.textContent = `Elapsed: ${d.elapsed_seconds}s`;
  $('diagResults').appendChild(r);
  if (d.response_preview && !d.ok) {
    const p = document.createElement('pre');
    p.textContent = d.response_preview;
    p.style.marginTop = '0.5rem';
    p.style.fontSize = '0.8rem';
    p.style.whiteSpace = 'pre-wrap';
    $('diagResults').appendChild(p)
  }
}

async function openFile(path) {
  try {
    await fetch('/api/open-file', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        path
      })
    });
  } catch (e) {
    console.error(e);
  }
}


let firstStatus = true;
let statusAbort = null;
async function refresh() {
  const wasFirstStatus = firstStatus;
  try {
    if (statusAbort) statusAbort.abort();
    statusAbort = new AbortController();
    const endpoint = firstStatus ? '/api/status?full=true' : '/api/status';
    const r = await fetch(endpoint, {
      signal: statusAbort.signal
    });
    statusAbort = null;
    const a = await r.json();
    window.SERVER_FEATURES = a.features || {};
    projectSelected = !!a.workspace;
    $('workTitle').textContent = projectSelected ? ((a.project && a.project.workspace) ? a.project.workspace.split(/[\\/]/).pop() : 'Workspace') : 'Select a project';
    $('workSub').textContent = projectSelected ? (a.workspace || '') : 'No workspace active.';
    _updateTokenDisplay();
    renderBackendDiagnostics(a.backend_diagnostics || {});
    setRunningState(!!a.running, !!a.stop_requested);
    if (a.artifacts !== undefined) window.latestArtifacts = a.artifacts;
    
    // Auth logic for Logout button
    const logoutBtn = $('logoutBtn');
    if (logoutBtn) {
      logoutBtn.style.display = a.auth_enabled ? 'inline-flex' : 'none';
      logoutBtn.onclick = async () => {
        await fetch('/api/logout', { method: 'POST' });
        window.location.href = '/login';
      };
    }
    
    // Sync mode from server (prefer session agent, fall back to config mode)
    const effectiveMode = a.agent || a.mode || 'build';
    if (effectiveMode) {
      localStorage.setItem('qf_intent_mode', effectiveMode);
      intentMode = effectiveMode;
      modeToggle.textContent = effectiveMode.charAt(0).toUpperCase() + effectiveMode.slice(1);
      modeMenu.querySelectorAll('.mode-dropdown-option').forEach(o => {
        o.classList.toggle('active', o.dataset.value === effectiveMode);
      });
    }
    
    // Sync token totals from server
    totalTokens = {
      prompt: a.total_prompt_tokens || 0,
      completion: a.total_completion_tokens || 0
    };
    if (a.input_token_price != null) inputPricePerM = a.input_token_price;
    if (a.output_token_price != null) outputPricePerM = a.output_token_price;
    updateModelTag(a.model, a.workspace);
    _updateTokenDisplay();



    if (a.projects !== undefined) renderProjects(a.projects);
    if (firstStatus) {
      renderSession(a.session_log || []);
      firstStatus = false;
      if (a.running) {
        try {
          const act = await fetch('/api/activity').then(res => res.json());
          const events = act.events || a.live_events || [];
          if (events.length > 0) {
            events.forEach(evt => {
              const rawText = typeof evt === 'string' ? evt : (evt.text || evt.event || evt.message || evt.error);
              const kind = (typeof evt === 'string' ? 'activity' : evt.type) || 'activity';
              if (!rawText) return;
              if (kind === 'done' || kind === 'think' || (!['think', 'action'].includes(kind) && isNoisyLiveText(rawText))) return;
              updateInlineLive(rawText, 'running', { log: true, countRepeats: false, kind: (kind !== 'think' && kind !== 'action') ? 'action' : kind });
            });
          }
        } catch (e) {
          console.error("Failed to restore live activity timeline", e);
        }
      }
    }
    if (a.artifacts) {
      const oldLen = (typeof currentArtifacts !== 'undefined' && currentArtifacts) ? currentArtifacts.length : 0;
      if (typeof renderArtifacts === 'function') renderArtifacts(a.artifacts, a.workspace || 'global');
      const newLen = (typeof currentArtifacts !== 'undefined' && currentArtifacts) ? currentArtifacts.length : 0;
      if (newLen > oldLen) {
        window._newDiffArtifacts = window._newDiffArtifacts || [];
        for (let i = oldLen; i < newLen; i++) {
          let art = currentArtifacts[i];
          if (typeof isDiffArtifact === 'function' && isDiffArtifact(art)) window._newDiffArtifacts.push(art);
        }
        if (!wasFirstStatus) {
          const newArt = currentArtifacts[newLen - 1];
          updateInlineLive(`Created artifact: ${newArt.title}`, running ? 'running' : 'done', {
            log: true
          });
        }
      }
    }
    if (a.running && !sseSource) {
      updateInlineLive(a.activity || 'Thinking', a.stop_requested ? 'stopping' : 'running', {
        log: false
      })
    } else if (!a.running && running) {
      setRunningState(false, false);
      if (!_notifQueued) {
        _notifQueued = true;
        _playNotificationSound();
      }
    }
  } catch (e) {
    if (e && e.name === 'AbortError') {
      statusAbort = null;
      return;
    }
    console.warn('Status refresh skipped:', e);
  }
}
function triggerIndex() {
  fetch('/api/workspace/index', { method: 'POST' })
    .then(r => r.json())
    .then(d => { if (!d.ok) console.warn('cbm index:', d.detail || d.error); })
    .catch(() => {});
}

async function selectProject(path) {
  if (running) return;
  const r = await fetch('/api/projects/select', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({
      path
    })
  });
  const d = await readJsonOrText(r);
  if (!r.ok || d.ok === false) {
    alert(d.error || 'Project select failed');
    return
  }
  firstStatus = true;
  document.querySelector('.col.left').classList.remove('open');
  document.body.classList.remove('drawer-open');
  await refresh();
  triggerIndex()
}

function showConfirm(title, text, onConfirm) {
  $('confirmTitle').textContent = title;
  $('confirmText').textContent = text;
  $('confirmCancel').textContent = 'Cancel';
  $('confirmOk').textContent = 'Confirm';
  $('confirmModal').classList.add('open');
  $('confirmCancel').onclick = () => $('confirmModal').classList.remove('open');
  $('confirmOk').onclick = () => {
    $('confirmModal').classList.remove('open');
    if (onConfirm) onConfirm();
  };
}
async function removeProject(path, e) {
  if (e) e.stopPropagation();
  if (running) return;
  showConfirm('Remove Workspace', 'Remove this workspace from the UI? The actual folder will NOT be deleted.', async () => {
    const r = await fetch('/api/projects/remove', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        path
      })
    });
    const d = await readJsonOrText(r);
    if (!r.ok || d.ok === false) {
      alert(d.error || 'Remove failed');
      return
    }
    renderSession([]);
    firstStatus = true;
    await refresh();
  });
}
async function uploadFiles(files) {
  if (!projectSelected) {
    alert('Create or select a project folder first.');
    return
  }
  if (!files || !files.length) return;
  const fd = new FormData();
  [...files].forEach(f => fd.append('files', f, f.name));
  const r = await fetch('/api/upload', {
    method: 'POST',
    body: fd
  });
  const d = await readJsonOrText(r);
  if (!r.ok) throw new Error(d.error || 'Upload failed');
  let t = textOfEditor();
  (d.files || []).forEach(f => {
    t += (t ? ' ' : '') + '@' + f.mention
  });
  setEditorText(t + ' ')
}
let isSending = false;
async function send() {
  if (isSending) return;
  const text = textOfEditor().trim();
  if (!projectSelected) {
    alert('Create or select a project folder first.');
    return
  }
  if (!text) return;
  
  if (running) {
    isSending = true;
    try {
      const r = await fetch('/api/chat/followup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt: text })
      });
      const data = await r.json();
      if (r.ok && data.ok) {
        setEditorText('');
        addMsg('User', text);
        updateSendStopButtons();
        scrollChatToBottom();
      } else {
        alert(data.error || 'Failed to queue follow-up');
      }
    } catch (e) {
      alert('Error sending follow-up: ' + e);
    } finally {
      isSending = false;
    }
    return;
  }
  
  isSending = true;

  addMsg('User', text);
  updateInlineLive('Thinking...', 'running');
  setRunningState(true, false);
  setEditorText('');
  // Enhancement: SSE streaming for live activity updates
  let streamingBubble = null;

  function sseOnMessage(e) {
    try {
      const d = JSON.parse(e.data);
      if (['think', 'action', 'response'].includes(d.type)) {
        updateInlineLive(d.event || d.message || 'Working', 'running', {
          log: true,
          countRepeats: false,
          kind: d.type
        });
      } else if (d.type === 'activity') {
        updateInlineLive(d.event || 'Working', 'running', {
          log: true,
          countRepeats: false
        });
      } else if (d.type === 'token_usage') {
        totalTokens.prompt = d.total_prompt || totalTokens.prompt;
        totalTokens.completion = d.total_completion || totalTokens.completion;
        _updateTokenDisplay();
      } else if (d.type === 'token') {
        if (inlineLiveEl && inlineLiveEl.open) {
          inlineLiveEl.open = false;
          inlineLiveEl.classList.remove('running');
          inlineLiveEl.querySelector('.livetext').textContent = 'Completed';
        }
        if (!streamingBubble) {
          const el = document.createElement('div');
          el.className = 'msg agent';
          el.innerHTML = '<div class="label">Agent</div><div class="bubble markdown-body" id="streamingMsg"></div>';
          $('chat').appendChild(el);
          streamingBubble = el.querySelector('.bubble');
        }
        const shouldFollow = chatIsNearBottom();
        streamingBubble.dataset.raw = (streamingBubble.dataset.raw || '') + d.event;
        if (!streamingBubble.renderQueued) {
          streamingBubble.renderQueued = true;
          requestAnimationFrame(() => {
            if (streamingBubble) {
              streamingBubble.innerHTML = md(streamingBubble.dataset.raw);
              if (shouldFollow) scrollChatToBottom();
              streamingBubble.renderQueued = false;
            }
          });
        }
      } else if (d.type === 'error') {
        const errMsg = d.error || d.message || 'An error occurred';
        addMsg('System', '⚠️ ' + errMsg);
        if (sseSource) { sseSource.close(); sseSource = null; }
        clearInterval(poll); poll = null;
        setRunningState(false, false);
        compactLiveTranscript();
        firstStatus = true;
        return;
      } else if (d.type === 'complete') {
        _notifQueued = true;
        _playNotificationSound();
        if (d.response) {
          const sm = document.getElementById('streamingMsg');
          if (sm) {
            sm.innerHTML = md(d.response);
            sm.removeAttribute('id');
          } else {
            addMsg('Agent', d.response);
          }
          streamingBubble = null;
        }
        if (sseSource) {
          sseSource.close();
          sseSource = null
        }
        clearInterval(poll);
        poll = null;
        setRunningState(false, false);
        firstStatus = true;
        refresh().then(() => {
          if (d.workspace_changes) {
            let changedFiles = [];
            if (d.workspace_changes.created) changedFiles.push(...d.workspace_changes.created);
            if (d.workspace_changes.modified) changedFiles.push(...d.workspace_changes.modified);
            if (changedFiles.length > 0) {
              let relevantDiffs = matchingDiffArtifactsForChangedFiles(changedFiles);
              if (relevantDiffs.length > 0) {
                const msgs = document.querySelectorAll('#chat .msg.agent');
                if (msgs.length > 0) {
                  const lastMsgEl = msgs[msgs.length - 1];
                  let oldWidget = lastMsgEl.querySelector('.diff-widget');
                  if (oldWidget) oldWidget.remove();
                  renderDiffReviewWidget(lastMsgEl, relevantDiffs);
                }
              }
            }
          }
        });
      } else if (d.type === 'status') {
        if (d.stopping) updateInlineLive('Stopping...', 'stopping', {
          log: false
        });
        else if (d.running) updateInlineLive(d.activity || 'Thinking', 'running', {
          log: false
        });
      } else if (d.type === 'done') {
        if (sseSource) {
          sseSource.close();
          sseSource = null
        }
        if (streamingBubble) {
          setTimeout(() => streamingBubble.removeAttribute('id'), 1000);
          streamingBubble = null;
        }
        refresh();
      }
    } catch (ex) {}
  }

  function sseOnError() {
    if (sseSource) {
      sseSource.close();
      sseSource = null
    }
    // High fix #5: start polling fallback immediately so live activity doesn't freeze
    if (!poll) poll = setInterval(refresh, 900);
    // Attempt SSE reconnect after 2s (only while agent is still running)
    setTimeout(() => {
      if (running && !sseSource) {
        try {
          sseSource = new EventSource('/api/stream/activity');
          sseSource.onmessage = sseOnMessage;
          sseSource.onerror = sseOnError;
          sseSource.onopen = function () {
            if (poll) {
              clearInterval(poll);
              poll = null;
            }
            refresh();
          };
        } catch (ex) {
          /* stay on poll fallback */ }
      }
    }, 2000);
  }
  try {
    sseSource = new EventSource('/api/stream/activity');
    sseSource.onmessage = sseOnMessage;
    sseSource.onerror = sseOnError;
  } catch (ex) {
    console.log('SSE not available, falling back to polling');
    poll = setInterval(refresh, 900)
  }
  let postError = false;
  try {
    const runAbort = new AbortController();
    const runTimeoutSeconds = Math.max(60, Number(uiRunTimeoutSeconds || 3600));
    const runTimeout = setTimeout(() => runAbort.abort(), runTimeoutSeconds * 1000);
    const r = await fetch('/api/run', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        prompt: text,
        mode: intentMode,
        features: mergedFeatures()
      }),
      signal: runAbort.signal
    });
    clearTimeout(runTimeout);
    const data = await readJsonOrText(r);
    if (!r.ok || data.ok === false) {
      throw new Error(data.error || 'Request failed')
    }
    // engine thread now alive — ensure stop button is visible
    setRunningState(true, false);
    // POST returns immediately. Results delivered via SSE 'complete' event.
  } catch (e) {
    postError = true;
    updateInlineLive('Failed', 'failed');
    if (e.name === 'AbortError' || e.message.includes('abort') || e.message.includes('cancel')) {
      try {
        const r_status = await fetch('/api/status?full=true');
        const d_status = await r_status.json();
        if (d_status.session_log && d_status.session_log.length > 0) {
          renderSession(d_status.session_log);
        } else {
          addMsg('Agent', 'ERROR: ' + e.message + '\n\nDiagnostics and timeline export may have more detail.');
        }
      } catch (ex) {
        addMsg('Agent', 'ERROR: ' + e.message + '\n\nDiagnostics and timeline export may have more detail.');
      }
    } else {
      addMsg('Agent', 'ERROR: ' + e.message + '\n\nDiagnostics and timeline export may have more detail.');
    }
  } finally {
    if (postError) {
      // Error path: do full cleanup immediately
      if (sseSource) { sseSource.close(); sseSource = null }
      clearInterval(poll); poll = null;
      setRunningState(false, false);
      compactLiveTranscript();
    }
    // Success path: SSE 'complete' handler handles cleanup.
    // Always clear transient state.
    const sm = document.getElementById('streamingMsg');
    if (sm) sm.removeAttribute('id');
    inlineLiveEl = null;
    isSending = false;
    await refresh();
  }
}

async function stopRun() {
  if (!running) return;
  setRunningState(true, true);
  try {
    await fetch('/api/stop', {
      method: 'POST'
    })
  } catch (e) {
    addMsg('Agent', 'STOP ERROR: ' + e.message)
  } finally {
    await refresh()
  }
}

function downloadJson(filename, data) {
  const blob = new Blob([JSON.stringify(data, null, 2)], {
    type: 'application/json'
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 500);
}
async function exportTimeline() {
  try {
    const r = await fetch('/api/timeline/export');
    const d = await readJsonOrText(r);
    if (!r.ok || d.ok === false) {
      alert(d.error || 'Timeline export failed');
      return
    }
    const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\..*/, '').replace('T', '-');
    downloadJson(`quietforge-timeline-${stamp}.json`, d)
  } catch (e) {
    alert('Timeline export failed: ' + e.message)
  }
}

async function openProjectModal() {
  $('workspaceNameInput').value = '';
  $('availableWorkspacesContainer').style.display = 'none';
  $('customSelectText').textContent = '-- Select an existing folder --';
  $('customSelectDropdown').innerHTML = '';
  $('customSelectDropdown').style.display = 'none';
  
  $('customSelectDisplay').onclick = () => {
    const dd = $('customSelectDropdown');
    dd.style.display = dd.style.display === 'none' ? 'block' : 'none';
  };
  
  // Close dropdown when clicking outside
  document.addEventListener('click', function _closeDropdown(e) {
    if (!$('projectModal').classList.contains('open')) {
        document.removeEventListener('click', _closeDropdown);
        return;
    }
    if (!e.target.closest('#customSelectWrapper')) {
        $('customSelectDropdown').style.display = 'none';
    }
  });

  try {
    const res = await fetch('/api/projects/available');
    const data = await res.json();
    if (data.folders && data.folders.length > 0) {
      $('availableWorkspacesContainer').style.display = 'block';
      data.folders.forEach(folder => {
        const div = document.createElement('div');
        div.style.padding = '8px 12px';
        div.style.cursor = 'pointer';
        div.style.color = 'var(--text-main)';
        div.style.display = 'flex';
        div.style.alignItems = 'center';
        div.innerHTML = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="margin-right: 8px; opacity: 0.8;"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg><span>${folder}</span>`;
        div.onmouseover = () => div.style.background = 'rgba(129, 140, 248, 0.15)'; // QuietForge accent color with opacity
        div.onmouseout = () => div.style.background = 'transparent';
        div.onclick = () => {
          $('workspaceNameInput').value = folder;
          $('customSelectText').textContent = folder;
          $('customSelectDropdown').style.display = 'none';
        };
        $('customSelectDropdown').appendChild(div);
      });
    }
  } catch(e) {
    console.error('Failed to load available workspaces', e);
  }
  
  $('projectModal').classList.add('open');
}

$('newProject').onclick = openProjectModal;
$('projectClose').onclick = () => $('projectModal').classList.remove('open');
$('projectAction').onclick = async () => {
  const wsName = $('workspaceNameInput').value.trim();
  if (!wsName) {
    alert('Please enter a workspace name');
    return;
  }
  const r = await fetch('/api/projects/create', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({
      folders: [wsName]
    })
  });
  const d = await readJsonOrText(r);
  if (!r.ok || d.ok === false) {
    alert(d.error || 'Create failed');
    return
  }
  $('projectModal').classList.remove('open');
  firstStatus = true;
  await refresh();
  triggerIndex();
  $('workspaceNameInput').value = '';
};
async function openSettings() {
  try {
    const r = await fetch('/api/config/llm');
    const c = await r.json();
    if (c) {
      $('cfgModel').value = c.model || '';
      $('cfgBaseUrl').value = c.base_url || '';
      $('cfgApiKey').value = c.api_key || '';
      $('cfgMaxMessages').value = c.max_messages || 60;

      uiRunTimeoutSeconds = parseFloat(c.ui_run_timeout || c.request_timeout || 3600) || 3600;
    }
  } catch (e) {
    console.error('Failed to load llm config', e)
  }
  $('settingsModal').classList.add('open')
}
$('settingsBtn').onclick = openSettings;
$('settingsClose').onclick = () => $('settingsModal').classList.remove('open');
$('settingsSave').onclick = async () => {
  const p = {
    provider: 'openai_compatible',
    model: $('cfgModel').value,
    base_url: $('cfgBaseUrl').value,
    api_key: $('cfgApiKey').value,
    max_messages: parseInt($('cfgMaxMessages').value) || 60,

    agent_backend: 'openai_tools'
  };
  try {
    await fetch('/api/config/llm', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(p)
    })
  } catch (e) {
    console.error('Failed to save llm config', e)
  }
  $('settingsModal').classList.remove('open')
};
if ($('diagModelTest')) $('diagModelTest').onclick = async () => {
  const btn = $('diagModelTest');
  btn.disabled = true;
  btn.textContent = 'Testing...';
  if ($('diagResults')) $('diagResults').innerHTML = '<div class="diag-elapsed">Testing model connection...</div>';
  try {
    const r = await fetch('/api/diagnostics/model-test', {
      method: 'POST'
    });
    const d = await readJsonOrText(r);
    renderModelTestResult(d)
  } catch (e) {
    if ($('diagResults')) $('diagResults').innerHTML = `<div class="diag-check bad"><span>NO</span><div><strong>model connection</strong><small>${esc(e.message)}</small></div></div>`
  } finally {
    btn.disabled = false;
    btn.textContent = 'Test Model Connection';
    await refresh()
  }
};
$('send').onclick = send;
$('stopBtn').onclick = stopRun;
$('attachBtn').onclick = () => $('fileInput').click();
$('fileInput').onchange = e => uploadFiles(e.target.files).catch(err => addMsg('Agent', 'UPLOAD ERROR: ' + err.message));
$('refreshBtn').onclick = refresh;

// Mode dropdown
const modeToggle = $('modeDropdownToggle');
const modeMenu = $('modeDropdownMenu');

// Initialize dropdown from localStorage
modeToggle.textContent = intentMode.charAt(0).toUpperCase() + intentMode.slice(1);
modeMenu.querySelectorAll('.mode-dropdown-option').forEach(o => {
  o.classList.toggle('active', o.dataset.value === intentMode);
});

modeToggle.onclick = () => {
  if (running) return;
  modeMenu.classList.toggle('open');
};

// Close menu on outside click
document.addEventListener('click', e => {
  if (!e.target.closest('#modeDropdown')) {
    modeMenu.classList.remove('open');
  }
});

modeMenu.addEventListener('click', e => {
  const opt = e.target.closest('.mode-dropdown-option');
  if (!opt || running) return;
  const value = opt.dataset.value;
  intentMode = value;
  modeToggle.textContent = value.charAt(0).toUpperCase() + value.slice(1);
  modeMenu.querySelectorAll('.mode-dropdown-option').forEach(o => o.classList.remove('active'));
  opt.classList.add('active');
  modeMenu.classList.remove('open');
  localStorage.setItem('qf_intent_mode', value);
  fetch('/api/config/mode', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({mode: value})
  }).catch(() => {});
  updateModelTag();
});

let _cachedModel = 'unknown';
let _cachedWorkspace = '';

function updateModelTag(model, workspace) {
  if (model) _cachedModel = model;
  if (workspace != null) _cachedWorkspace = workspace.replace(/[/\\]$/, '').split(/[/\\]/).pop() || '';
  let text = _cachedModel;
  if (_cachedWorkspace) {
    text += ' | ' + _cachedWorkspace;
  }
  const el = document.getElementById('modelTag');
  if (el) el.textContent = text;
}

renderChips('');
refresh();


if ($('newChatBtn')) $('newChatBtn').onclick = async () => {
  const r = await fetch('/api/chat/new', {
    method: 'POST'
  });
          const d = await r.json();
          if (d.ok) renderSession(d.session_log);
          await refresh();
          document.querySelector('.col.left').classList.remove('open');
          document.body.classList.remove('drawer-open');
        };

// Mobile Drawers
if ($('menuToggle')) $('menuToggle').onclick = () => {
  document.querySelector('.col.left').classList.add('open');
  document.body.classList.add('drawer-open');
};
if ($('activityToggle')) $('activityToggle').onclick = () => {
  const right = document.querySelector('.col.right');
  if (right) {
    right.classList.add('open');
    document.body.classList.add('drawer-open');
  }
};
document.body.addEventListener('click', e => {
  const activityDropdown = $('activityDropdown');
  if (activityDropdown && activityDropdown.classList.contains('open') && !e.target.closest('#activityDropdown')) {
    activityDropdown.classList.remove('open');
  }
  if (e.target.classList && e.target.classList.contains('modal') && e.target.classList.contains('open')) {
    e.target.classList.remove('open');
    return;
  }
  if (document.body.classList.contains('drawer-open') && !e.target.closest('.col.left') && !e.target.closest('.col.right') && !e.target.closest('.mobile-toggle')) {
    document.querySelector('.col.left').classList.remove('open');
    const right = document.querySelector('.col.right');
    if (right) right.classList.remove('open');
    document.body.classList.remove('drawer-open');
  }
});


async function checkHealth() {
  try {
    const r = await fetch('/api/status');
    const d = await r.json();
    const el = $('healthStatus');
    if (el) {
      if (r.ok && (d.status === 'running' || d.status === 'idle')) {
        el.className = 'status-dot online';
        el.title = 'Backend: Online';
      } else {
        el.className = 'status-dot offline';
        el.title = 'Backend: Error';
      }
    }
  } catch (e) {
    const el = $('healthStatus');
    if (el) {
      el.className = 'status-dot offline';
      el.title = 'Backend: Offline';
    }
  }
}
checkHealth();

// --- Artifacts & Tabs Logic ---
let currentArtifacts = [];
let uiRunTimeoutSeconds = 3600;
let currentArtifactWorkspace = 'global';

function artifactKey(suffix) {
  return `qf_artifacts_${suffix}:${currentArtifactWorkspace||'global'}`
}

function artifactDismissed() {
  return localStorage.getItem(artifactKey('dismissed')) === '1'
}

function setArtifactDismissed(value) {
  value ? localStorage.setItem(artifactKey('dismissed'), '1') : localStorage.removeItem(artifactKey('dismissed'))
}

function artifactSeenCount() {
  return parseInt(localStorage.getItem(artifactKey('seen_count')) || '0', 10) || 0
}

function setArtifactSeenCount(count) {
  localStorage.setItem(artifactKey('seen_count'), String(Math.max(0, count)))
}

function updateArtifactsChrome() {
  const count = currentArtifacts.length;
  if ($('artifactCountBadge')) $('artifactCountBadge').textContent = String(count);
  if ($('artifactToggleCount')) $('artifactToggleCount').textContent = String(count);
  if ($('artifactToggleBtn')) $('artifactToggleBtn').style.display = count ? 'inline-flex' : 'none';
  if (!count) closeArtifactsOverlay();
}

function openArtifactsOverlay(mode = 'list') {
  if (mode === 'list' && (!currentArtifacts || !currentArtifacts.length)) return;
  const overlay = $('artifactOverlay');
  if (!overlay) return;
  overlay.classList.add('open');
  overlay.setAttribute('aria-hidden', 'false');
  document.body.classList.add('artifact-open');
  if (mode === 'list') {
    if ($('artifactViewer')) $('artifactViewer').style.display = 'none';
    if ($('artifactsSidebar')) $('artifactsSidebar').style.display = 'flex';
  }
}

function closeArtifactsOverlay() {
  const overlay = $('artifactOverlay');
  if (!overlay) return;
  if (overlay.contains(document.activeElement)) {
    const restoreTarget = $('artifactToggleBtn') || $('msg');
    if (restoreTarget && typeof restoreTarget.focus === 'function') restoreTarget.focus({
      preventScroll: true
    });
    else if (document.activeElement && typeof document.activeElement.blur === 'function') document.activeElement.blur();
  }
  overlay.classList.remove('open');
  overlay.setAttribute('aria-hidden', 'true');
  document.body.classList.remove('artifact-open');
}

function stripDiffFence(content) {
  return String(content || '').trim()
    .replace(/^```(?:diff|patch)\s*/i, '')
    .replace(/\s*```$/i, '');
}

function hasUnifiedDiffContent(content) {
  const text = stripDiffFence(content);
  return /^diff --git\s+a\/.+?\s+b\/.+$/m.test(text) ||
    /^---\s+(?:a\/|\/dev\/null).+$/m.test(text) && /^\+\+\+\s+(?:b\/|\/dev\/null).+$/m.test(text) && /^@@\s+-\d+(?:,\d+)?\s+\+\d+(?:,\d+)?\s+@@/m.test(text) ||
    /^\+\+\+\s+b\/.+$/m.test(text) && /^@@\s+-0,0\s+\+\d+(?:,\d+)?\s+@@/m.test(text);
}

function isDiffArtifact(art) {
  const title = String(art && art.title || '');
  const content = String(art && art.content || '');
  return hasUnifiedDiffContent(content) || (/\.(diff|patch)$/i.test(title) && hasUnifiedDiffContent(content));
}

function artifactFallbackPath(art) {
  const title = String(art && art.title || 'artifact');
  const diffLike = /^Diff[_ -]/i.test(title) || isDiffArtifact(art);
  let name = title
    .replace(/^Diff[_ -]*/i, '')
    .replace(/^Diff:\s*/i, '')
    .replace(/_/g, '/')
    .replace(/\.md$/i, '')
    .trim();
  name = name.replace(/\.(diff|patch)$/i, '');
  return name || title;
}

function diffIconForPath(path) {
  const ext = (String(path).split('.').pop() || 'txt').toLowerCase();
  if (ext === 'js' || ext === 'jsx' || ext === 'ts' || ext === 'tsx') return {
    label: ext.toUpperCase(),
    cls: 'js'
  };
  if (ext === 'py') return {
    label: 'PY',
    cls: 'py'
  };
  if (ext === 'html') return {
    label: '<>',
    cls: 'html'
  };
  if (ext === 'css') return {
    label: 'CSS',
    cls: 'css'
  };
  if (ext === 'md') return {
    label: 'MD',
    cls: 'md'
  };
  if (ext === 'json') return {
    label: '{}',
    cls: 'json'
  };
  return {
    label: ext.slice(0, 3).toUpperCase() || 'TXT',
    cls: 'txt'
  };
}

function isMarkdownArtifact(art) {
  return /\.md$/i.test(String(art && art.title || '')) && !isDiffArtifact(art);
}

function matchingDiffArtifactsForChangedFiles(changedFiles) {
  const artifacts = Array.isArray(window.latestArtifacts) ? window.latestArtifacts : [];
  const diffArtifacts = artifacts.filter(isDiffArtifact);
  const relevant = [];
  (changedFiles || []).forEach(f => {
    const bn = String(f).split(/[\\/]/).pop();
    const normalizedPath = String(f).replace(/\\/g, '/');
    const art = diffArtifacts.find(a => {
      const title = String(a.title || '');
      const fallback = artifactFallbackPath(a);
      return title === "Diff_" + bn + ".md" ||
        title === "Diff_ " + bn + ".md" ||
        title === "Diff_" + bn ||
        title === "Diff_ " + bn ||
        title === "Diff: " + bn ||
        title === "Diff: " + bn + ".md" ||
        fallback === normalizedPath ||
        fallback === bn;
    });
    if (art && !relevant.includes(art)) relevant.push(art);
  });
  return relevant;
}

function parseDiffArtifacts(artifacts) {
  const files = [];
  let totalAdditions = 0,
    totalDeletions = 0;
  artifacts.forEach(art => {
    if (!isDiffArtifact(art)) return;
    const lines = stripDiffFence(art.content).split('\n');
    let current = null;
    const ensureFile = (path) => {
      const clean = String(path || artifactFallbackPath(art)).replace(/^[ab]\//, '').trim() || artifactFallbackPath(art);
      current = {
        path: clean,
        additions: 0,
        deletions: 0,
        artifact: art
      };
      files.push(current);
      return current;
    };
    lines.forEach(line => {
      let m = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
      if (m) {
        ensureFile(m[2]);
        return
      }
      m = line.match(/^\+\+\+\s+(?:b\/)?(.+)$/);
      if (m && m[1] && m[1] !== '/dev/null') {
        ensureFile(m[1]);
        return
      }
      if (!current && ((line.startsWith('+') && !line.startsWith('+++')) || (line.startsWith('-') && !line.startsWith('---')))) {
        ensureFile(artifactFallbackPath(art));
      }
      if (!current) return;
      if (line.startsWith('+') && !line.startsWith('+++')) {
        current.additions++;
        totalAdditions++
      } else if (line.startsWith('-') && !line.startsWith('---')) {
        current.deletions++;
        totalDeletions++
      }
    });
  });
  return {
    files: files.filter(f => f.additions > 0 || f.deletions > 0),
    totalAdditions,
    totalDeletions
  };
}

function cleanDiffLines(content) {
  return String(content || '')
    .replace(/^```(?:diff|patch)?\s*/i, '')
    .replace(/\s*```$/, '')
    .split(/\r?\n/)
    .filter(line => !line.startsWith('diff --git ') && !line.startsWith('index ') && !line.startsWith('--- ') && !line.startsWith('+++ '));
}

function renderDiffWithLineNumbers(lines) {
  let oldLine = 0, newLine = 0;
  return lines.map(line => {
    // Parse hunk header: @@ -oldStart,oldCount +newStart,newCount @@
    const hunkMatch = line.match(/^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/);
    if (hunkMatch) {
      oldLine = parseInt(hunkMatch[1], 10);
      newLine = parseInt(hunkMatch[2], 10);
      return '';
    }
    let cls = 'ctx', oldNum = '', newNum = '', code = line;
    if (line.startsWith('+')) {
      cls = 'add';
      newNum = newLine++;
      code = line.substring(1);
    } else if (line.startsWith('-')) {
      cls = 'del';
      oldNum = oldLine++;
      code = line.substring(1);
    } else {
      if (line.startsWith(' ')) code = line.substring(1);
      oldNum = oldLine++;
      newNum = newLine++;
    }
    return `<div class="review-diff-line ${cls}"><span class="diff-ln">${oldNum}</span><span class="diff-ln">${newNum}</span><span class="diff-code">${esc(code || ' ')}</span></div>`;
  }).join('');
}

function renderCombinedDiffReview(diffArtifacts, parsed) {
  const sections = parsed.files.map(file => {
    const lines = cleanDiffLines(file.artifact.content);
    return `
      <section class="review-file-block">
        <div class="review-file-header">
          <span class="review-file-path">${esc(file.path)}</span>
          <span class="review-file-counts"><span class="diff-widget-additions">+${file.additions}</span><span class="diff-widget-deletions">-${file.deletions}</span></span>
        </div>
        <div class="review-diff-code">${renderDiffWithLineNumbers(lines)}</div>
      </section>
    `;
  }).join('');
  return `<div class="review-diff-stack">${sections}</div>`;
}

function reviewArtifactForFile(file) {
  return {
    title: file.path,
    html: renderCombinedDiffReview([file.artifact], {
      files: [file],
      totalAdditions: file.additions,
      totalDeletions: file.deletions
    })
  };
}

function renderPlainArtifactList(container, artifacts, title = 'Documents') {
  if (!artifacts.length) return;
  const group = document.createElement('div');
  group.className = 'artifact-doc-group';
  group.innerHTML = `<div class="artifact-group-title">${esc(title)}</div>`;
  artifacts.forEach(art => {
    const el = document.createElement('div');
    el.className = 'artifact-item';
    el.innerHTML = `
      <div class="artifact-item-title">${esc(art.title)}${art.version_count?` <span style="color:var(--text-muted);font-size:11px;">v${art.version_count+1}</span>`:''}</div>
      <div class="artifact-item-preview">${esc(String(art.content||'').replace(/<[^>]+>/g,'').substring(0,70))}...</div>
    `;
    el.classList.toggle('markdown-artifact-item', isMarkdownArtifact(art));
    el.onclick = () => viewArtifact(art);
    group.appendChild(el);
  });
  container.appendChild(group);
}

function renderDiffReviewWidget(container, diffArtifacts) {
  const parsed = parseDiffArtifacts(diffArtifacts);
  const widget = document.createElement('details');
  widget.className = 'diff-widget';
  const filesText = parsed.files.length === 1 ? '1 file changed' : `${parsed.files.length} files changed`;
  widget.innerHTML = `
    <summary class="diff-widget-header" style="cursor: pointer; user-select: none;">
      <div class="diff-widget-stats" style="display: flex; align-items: center;">
        <strong>${esc(filesText)}</strong><span class="diff-widget-additions">+${parsed.totalAdditions}</span><span class="diff-widget-deletions">-${parsed.totalDeletions}</span>
        <span class="diff-widget-arrow" style="display:inline-block; margin-left:8px; font-family:monospace; font-size:11px; transform:rotate(0deg); transition:transform 0.2s;">></span>
      </div>
      <span class="diff-widget-review-btn">Review</span>
    </summary>
    <div class="diff-widget-file-list"></div>
  `;
  
  widget.addEventListener('toggle', () => {
    const arrow = widget.querySelector('.diff-widget-arrow');
    if (arrow) arrow.style.transform = widget.open ? 'rotate(90deg)' : 'rotate(0deg)';
  });
  const list = widget.querySelector('.diff-widget-file-list');
  parsed.files.forEach(file => {
    const icon = diffIconForPath(file.path);
    const filename = String(file.path).split(/[\\/]/).pop() || file.path;
    const dir = String(file.path).slice(0, Math.max(0, String(file.path).length - filename.length));
    const row = document.createElement('button');
    row.className = 'diff-widget-file';
    row.type = 'button';
    row.innerHTML = `
      <span class="diff-widget-file-icon ${esc(icon.cls)}">${esc(icon.label)}</span>
      <span class="diff-widget-file-main">
        <span class="diff-widget-filename">${esc(filename)}</span>
        ${dir?`<span class="diff-widget-filepath">${esc(dir)}</span>`:''}
      </span>
      <span class="diff-widget-line-stats"><span class="diff-widget-additions">+${file.additions}</span><span class="diff-widget-deletions">-${file.deletions}</span></span>
      <button type="button" class="diff-widget-revert-btn" title="Revert file to previous state">
        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="1 4 1 10 7 10"></polyline><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"></path></svg>
      </button>
    `;
    row.onclick = (e) => {
      if (e.target.closest('.diff-widget-revert-btn')) {
        e.stopPropagation();
        const msgId = container.dataset.messageId;
        if (!msgId) return alert('Cannot revert: unknown message ID.');
        showConfirm('Revert File', `Are you sure you want to revert "${file.path}" to its state before this AI response?`, async () => {
          try {
            const res = await fetch('/api/chat/revert-file', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ message_id: msgId, path: file.path })
            });
            const data = await res.json();
            if (data.error) throw new Error(data.error);
            row.classList.add('reverted');
            row.querySelector('.diff-widget-revert-btn').remove();
            if (window.refreshExplorerTree) window.refreshExplorerTree();
          } catch (err) {
            alert("Failed to revert file: " + err.message);
          }
        });
        return;
      }
      viewArtifact(reviewArtifactForFile(file));
    };
    list.appendChild(row);
  });
  const reviewBtn = widget.querySelector('.diff-widget-review-btn');
  if (reviewBtn) reviewBtn.onclick = (e) => {
    e.preventDefault();
    e.stopPropagation();
    viewArtifact({
      title: parsed.files.length === 1 ? 'Review diff' : 'Review all changes',
      html: renderCombinedDiffReview(diffArtifacts, parsed)
    });
  };
  container.appendChild(widget);
}

function setupTabs() {
  const backBtn = $('artifactBackBtn');
  if (backBtn) backBtn.onclick = () => {
    if ($('artifactViewer')) $('artifactViewer').style.display = 'none';
    if ($('artifactsSidebar')) $('artifactsSidebar').style.display = 'flex';
  };
  if ($('artifactsCloseBtn')) $('artifactsCloseBtn').onclick = () => {
    closeArtifactsOverlay();
    setArtifactDismissed(true);
    setArtifactSeenCount(currentArtifacts.length);
    updateArtifactsChrome();
  };
  if ($('artifactToggleBtn')) $('artifactToggleBtn').onclick = () => {
    setArtifactDismissed(false);
    setArtifactSeenCount(currentArtifacts.length);
    updateArtifactsChrome();
    openArtifactsOverlay('list');
  };
  if ($('artifactOverlay')) $('artifactOverlay').addEventListener('click', e => {
    if (e.target === $('artifactOverlay')) closeArtifactsOverlay();
  });
}

function renderArtifacts(artifacts, workspace = 'global') {
  currentArtifactWorkspace = String(workspace || 'global');
  artifacts = Array.isArray(artifacts) ? artifacts : [];
  const unchanged = JSON.stringify(artifacts) === JSON.stringify(currentArtifacts);
  currentArtifacts = artifacts || [];
  if (artifacts.length > artifactSeenCount()) {
    setArtifactSeenCount(artifacts.length);
  } else if (!artifacts.length) {
    setArtifactSeenCount(0);
  }
  updateArtifactsChrome();
  if (unchanged) return;

  const list = $('artifactsSidebar');
  if (!list) return;
  list.innerHTML = '';

  if (currentArtifacts.length === 0) {
    return;
  }

  const diffArtifacts = currentArtifacts.filter(isDiffArtifact).reverse();
  const otherArtifacts = currentArtifacts.filter(art => !isDiffArtifact(art)).reverse();
  if (diffArtifacts.length) renderDiffReviewWidget(list, diffArtifacts);
  renderPlainArtifactList(list, otherArtifacts, diffArtifacts.length ? 'Other artifacts' : 'Artifacts');
}

let currentArtifactPath = '';
let currentArtifactRaw = '';

function viewArtifact(art) {
  openArtifactsOverlay('viewer');
  $('artifactsSidebar').style.display = 'none';
  const viewer = $('artifactViewer');
  viewer.style.display = 'flex';
  $('artifactTitle').textContent = art.title;

  const actions = $('artifactActions');
  if (actions) {
    if (art.isWorkspaceFile) {
      actions.style.display = 'flex';
      $('artifactEditBtn').style.display = 'block';
      $('artifactSaveBtn').style.display = 'none';
      currentArtifactPath = art.path;
      currentArtifactRaw = art.rawContent;
    } else {
      actions.style.display = 'none';
    }
  }

  const body = $('artifactBody');
  body.classList.remove('editing');
  body.classList.toggle('artifact-doc-body', isMarkdownArtifact(art));
  if (art.html) {
    body.innerHTML = DOMPurify.sanitize(art.html);
  } else if (window.marked) {
    body.innerHTML = md(art.content);
  } else {
    body.innerHTML = '<pre>' + esc(art.content) + '</pre>';
  }
}

let cmEditor = null;

function getCodeMirrorMode(path) {
  const ext = path.split('.').pop().toLowerCase();
  switch(ext) {
    case 'js': case 'json': return 'javascript';
    case 'py': return 'python';
    case 'html': return 'htmlmixed';
    case 'css': return 'css';
    case 'xml': return 'xml';
    default: return 'javascript';
  }
}

$('artifactEditBtn').onclick = () => {
  $('artifactEditBtn').style.display = 'none';
  $('artifactSaveBtn').style.display = 'block';
  const body = $('artifactBody');
  body.classList.add('editing');
  body.innerHTML = '';
  
  cmEditor = CodeMirror(body, {
    value: currentArtifactRaw,
    mode: getCodeMirrorMode(currentArtifactPath),
    theme: 'dracula',
    lineNumbers: true,
    matchBrackets: true,
    autoCloseBrackets: true,
    indentUnit: 2,
    tabSize: 2,
    extraKeys: {
      "Cmd-S": function(cm) { $('artifactSaveBtn').click(); },
      "Ctrl-S": function(cm) { $('artifactSaveBtn').click(); }
    }
  });
  // Ensure CodeMirror fills the container
  cmEditor.setSize("100%", "100%");
  setTimeout(() => cmEditor.refresh(), 1);
  cmEditor.focus();
};

$('artifactSaveBtn').onclick = async () => {
  if (!cmEditor) return;
  const content = cmEditor.getValue();
  try {
    const res = await fetch('/api/workspace/save-file', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: currentArtifactPath, content })
    });
    const data = await res.json();
    if (data.error) throw new Error(data.error);
    openWorkspaceFile(currentArtifactPath);
  } catch (err) {
    alert("Error saving file: " + err.message);
  }
};

setupTabs();


function updateStickyPrompts() {
  const chat = document.getElementById('chat');
  if (!chat) return;
  const chatRect = chat.getBoundingClientRect();
  const prompts = document.querySelectorAll('.msg.user');
  
  const scrollTop = chat.scrollTop;
  let visiblePrompts = [];
  
  prompts.forEach(p => {
    const rect = p.getBoundingClientRect();
    
    if (p.parentElement) {
      const parent = p.parentElement;
      // Get parent's offsetTop relative to chat container
      let parentOffsetTop = 0;
      let curr = parent;
      while (curr && curr !== chat) {
        parentOffsetTop += curr.offsetTop;
        curr = curr.offsetParent;
      }
      
      const isSticking = scrollTop > (parentOffsetTop - 14);
      const currentlySticking = p.classList.contains('sticking');
      
      if (isSticking) {
        if (!currentlySticking) {
          const h = p.offsetHeight;
          if (h > 0) {
            p.style.setProperty('--natural-height', h + 'px');
          }
        }
        p.classList.add('sticking');
      } else {
        p.classList.remove('sticking');
        p.style.removeProperty('--natural-height');
      }
    }

    if (rect.bottom > chatRect.top && rect.top < chatRect.bottom) {
      visiblePrompts.push(p);
    }
  });


}

document.getElementById('chat').addEventListener('scroll', updateStickyPrompts);

document.addEventListener('click', e => {
  const btn = e.target.closest('.copy-btn');
  if (btn) {
    const pre = btn.nextElementSibling;
    if (pre && pre.tagName === 'PRE') {
      // Fallback for secure contexts
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(pre.textContent).catch(err => console.error('Copy failed:', err));
      } else {
        const textArea = document.createElement('textarea');
        textArea.value = pre.textContent;
        textArea.style.position = 'fixed';
        document.body.appendChild(textArea);
        textArea.focus();
        textArea.select();
        try { document.execCommand('copy'); } catch (err) {}
        document.body.removeChild(textArea);
      }
      
      const originalHtml = btn.innerHTML;
      btn.innerHTML = '<svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"></polyline></svg>';
      setTimeout(() => {
        if (btn.innerHTML.includes('polyline')) btn.innerHTML = originalHtml;
      }, 2000);
    }
  }
});

// --- Workspace File Explorer Logic ---
const tabWorkspaces = document.getElementById('tabWorkspaces');
const tabExplorer = document.getElementById('tabExplorer');
const explorerTree = document.getElementById('explorerTree');
const workspaceActions = document.getElementById('workspaceActions');
const explorerActions = document.getElementById('explorerActions');
const btnRefreshExplorer = document.getElementById('refreshExplorer');

if (tabWorkspaces && tabExplorer) {
  tabWorkspaces.addEventListener('click', () => {
    tabWorkspaces.classList.add('active');
    tabExplorer.classList.remove('active');
    if ($('projectList')) $('projectList').style.display = 'block';
    if (explorerTree) explorerTree.style.display = 'none';
    if (workspaceActions) workspaceActions.style.display = 'flex';
    if (explorerActions) explorerActions.style.display = 'none';
  });

  tabExplorer.addEventListener('click', () => {
    tabExplorer.classList.add('active');
    tabWorkspaces.classList.remove('active');
    if ($('projectList')) $('projectList').style.display = 'none';
    if (explorerTree) explorerTree.style.display = 'block';
    if (workspaceActions) workspaceActions.style.display = 'none';
    if (explorerActions) explorerActions.style.display = 'flex';
    loadExplorerTree();
  });
}

if (btnRefreshExplorer) {
  btnRefreshExplorer.addEventListener('click', (e) => {
    if (tabExplorer && tabExplorer.classList.contains('active')) {
      e.preventDefault();
      e.stopPropagation();
      loadExplorerTree();
    }
  });
}

async function loadExplorerTree() {
  if (!explorerTree) return;
  explorerTree.innerHTML = '<div style="color:var(--text-muted); padding:10px;">Loading...</div>';
  try {
    const res = await fetch('/api/workspace/tree');
    if (!res.ok) throw new Error('Failed to load tree');
    const data = await res.json();
    if (!data || data.length === 0) {
      explorerTree.innerHTML = '<div style="color:var(--text-muted); padding:10px;">Workspace is empty</div>';
      return;
    }
    explorerTree.innerHTML = renderTree(data);
  } catch (err) {
    explorerTree.innerHTML = '<div style="color:var(--danger); padding:10px;">Error loading files</div>';
  }
}

window.toggleTreeNode = function(node) {
  document.querySelectorAll('.tree-node.folder').forEach(el => el.classList.remove('selected'));
  node.classList.add('selected');
  selectedExplorerPath = node.getAttribute('data-path') || '';
  const children = node.nextElementSibling;
  if (children) children.classList.toggle('open');
};

let selectedExplorerPath = '';

function renderTree(nodes) {
  let html = '';
  for (const node of nodes) {
    if (node.type === 'dir') {
      html += `
        <div class="tree-item">
          <div class="tree-node folder" data-path="${esc(node.path)}" onclick="toggleTreeNode(this)">
            <svg class="tree-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"></polyline></svg>
            <span class="tree-name">${esc(node.name)}</span>
            <button class="project-delete" onclick="event.stopPropagation(); deleteExplorerItem('${esc(node.path)}', true)">x</button>
          </div>
          <div class="tree-children">
            ${renderTree(node.children)}
          </div>
        </div>
      `;
    } else {
      html += `
        <div class="tree-item">
          <div class="tree-node file" data-path="${esc(node.path)}" onclick="openWorkspaceFile(this.getAttribute('data-path'))">
            <svg class="tree-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"></path><polyline points="13 2 13 9 20 9"></polyline></svg>
            <span class="tree-name">${esc(node.name)}</span>
            <button class="project-delete" onclick="event.stopPropagation(); deleteExplorerItem('${esc(node.path)}', false)">x</button>
          </div>
        </div>
      `;
    }
  }
  return html;
}

window.deleteExplorerItem = function(path, isDir) {
  const msg = isDir ? `Are you sure you want to delete the folder "${path}" and all its contents?` : `Are you sure you want to delete the file "${path}"?`;
  showConfirm('Delete item', msg, async () => {
    try {
      const res = await fetch('/api/workspace/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path })
      });
      const data = await res.json();
      if (data.error) throw new Error(data.error);
      if (selectedExplorerPath.startsWith(path)) selectedExplorerPath = '';
      loadExplorerTree();
    } catch (err) {
      alert("Error: " + err.message);
    }
  });
};

window.openWorkspaceFile = async function(path) {
  try {
    const res = await fetch('/api/workspace/file?path=' + encodeURIComponent(path));
    const data = await res.json();
    if (data.error) throw new Error(data.error);
    const ext = path.split('.').pop() || '';
    viewArtifact({
      title: path,
      type: 'code',
      content: "```" + ext + "\n" + data.content + "\n```",
      rawContent: data.content,
      isWorkspaceFile: true,
      path: path
    });
  } catch (err) {
    console.error('Failed to open file:', err);
    alert('Failed to open file: ' + err.message);
  }
};

window.showPromptModal = function(title, text, defaultValue, callback) {
  const modal = $('promptModal');
  if (!modal) return callback(prompt(text, defaultValue));
  $('promptTitle').textContent = title;
  $('promptText').textContent = text;
  const inputEl = $('promptInput');
  inputEl.value = defaultValue || '';
  
  modal.style.display = 'flex';
  inputEl.focus();
  inputEl.select();

  const close = () => {
    modal.style.display = 'none';
    $('promptOk').onclick = null;
    $('promptCancel').onclick = null;
    inputEl.onkeydown = null;
  };

  $('promptCancel').onclick = () => {
    close();
    callback(null);
  };

  $('promptOk').onclick = () => {
    const val = inputEl.value.trim();
    close();
    callback(val);
  };

  inputEl.onkeydown = (e) => {
    if (e.key === 'Enter') $('promptOk').click();
    if (e.key === 'Escape') $('promptCancel').click();
  };
};

const btnNewFile = document.getElementById('newFile');
const btnNewFolder = document.getElementById('newFolder');

if (btnNewFile) {
  btnNewFile.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    const prefix = selectedExplorerPath ? selectedExplorerPath + '/' : '';
    showPromptModal('New File', 'Enter the file path (e.g., src/main.js):', prefix, async (name) => {
      if (!name) return;
      try {
        const res = await fetch('/api/workspace/create-file', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: name })
        });
        const data = await res.json();
        if (data.error) throw new Error(data.error);
        loadExplorerTree();
      } catch (err) {
        alert("Error: " + err.message);
      }
    });
  });
}

if (btnNewFolder) {
  btnNewFolder.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    const prefix = selectedExplorerPath ? selectedExplorerPath + '/' : '';
    showPromptModal('New Folder', 'Enter the folder path (e.g., src/components):', prefix, async (name) => {
      if (!name) return;
      try {
        const res = await fetch('/api/workspace/create-folder', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: name })
        });
        const data = await res.json();
        if (data.error) throw new Error(data.error);
        loadExplorerTree();
      } catch (err) {
        alert("Error: " + err.message);
      }
    });
  });
}


