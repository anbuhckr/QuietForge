window.viewArtifactByTitle = function (title) {
  const art = typeof currentArtifacts !== 'undefined' ? currentArtifacts.find(a => a.title === title) : null;
  if (art) viewArtifact(art);
};

const $ = id => document.getElementById(id);
const esc = s => String(s ?? '').replace(/[&<>]/g, c => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;'
}[c]));
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
  /* removed inlineLiveEl */
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
let totalTokens = { prompt: 0, completion: 0 };
let inputPricePerM = 2.50;
let outputPricePerM = 10.00;

function _fmtTokens(n) {
  return n.toLocaleString();
}
function _fmtCost(cents) {
  return '$' + cents.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
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
    a.play().catch(() => { });
  } catch (e) { }
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
  return (items || []).map(item => ({ ...item, prefix: qObj.prefix }));
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


function chatIsNearBottom(threshold = 90) {
  const c = $('chat');
  return c.scrollHeight - c.clientHeight <= c.scrollTop + threshold;
}

function scrollChatToBottom() {
  const c = $('chat');
  c.scrollTop = c.scrollHeight;
}

let runStartTime = 0, runTimerInterval = null;

class ConversationState {
  constructor() {
    this.turns = [];
    this._loadPromise = null;
    this._renderPromise = null;
  }

  _currentTurn() {
    if (this.turns.length === 0) {
      this.turns.push({ user: null, agent: null, liveContainer: [], completed: false, durationMs: 0 });
    }
    return this.turns[this.turns.length - 1];
  }

  async clear() {
    this.turns = [];
    await this.render();
  }

  formatToolCall(rawStr) {
    const spaceIdx = rawStr.indexOf(' ');
    if (spaceIdx === -1) return rawStr;
    const toolName = rawStr.substring(0, spaceIdx);
    const argStr = rawStr.substring(spaceIdx + 1);
    try {
      const args = JSON.parse(argStr);
      let s = [];

      if (toolName === 'view_file' || toolName === 'read_file' || toolName === 'read') {
        let f = args.AbsolutePath || args.path || args.TargetFile || args.filePath || args.filepath || '';
        if (typeof f === 'string' && (f.includes('/') || f.includes('\\'))) f = f.split(/[\\/]/).pop();
        let start = args.StartLine || args.start || '';
        let end = args.EndLine || args.end || '';
        if (start && end) return `${toolName} ${f} ${start}-${end}`;
        if (start) return `${toolName} ${f} ${start}-...`;
        return `${toolName} ${f}`;
      } else if (toolName === 'replace_file_content' || toolName === 'multi_replace_file_content' || toolName === 'write_to_file' || toolName === 'write_file' || toolName === 'edit' || toolName === 'write') {
        let f = args.TargetFile || args.AbsolutePath || args.path || args.filePath || args.filepath || '';
        if (typeof f === 'string' && (f.includes('/') || f.includes('\\'))) f = f.split(/[\\/]/).pop();
        return `${toolName} ${f}`;
      } else if (toolName === 'grep_search' || toolName === 'grep' || toolName === 'search' || toolName === 'glob') {
        let f = args.SearchPath || args.path || args.filePath || args.filepath || args.DirectoryPath || '';
        if (typeof f === 'string' && (f.includes('/') || f.includes('\\'))) f = f.split(/[\\/]/).pop();
        return `${toolName} "${args.Query || args.query || args.pattern || '*'}" in ${f}`;
      } else if (toolName === 'list_dir' || toolName === 'list') {
        let f = args.DirectoryPath || args.path || args.filePath || args.filepath || '';
        if (typeof f === 'string' && (f.includes('/') || f.includes('\\'))) f = f.split(/[\\/]/).pop();
        return `list ${f}`;
      } else if (toolName === 'run_command' || toolName === 'command' || toolName === 'run') {
        return `run ${args.CommandLine || args.command || args.cmd || ''}`;
      }

      for (let k in args) {
        let v = args[k];
        if (typeof v === 'string') {
          if (v.includes('\n') || v.length > 50) {
            v = v.replace(/\n/g, '\\n');
            if (v.length > 50) v = v.substring(0, 50) + '...';
          } else if (v.includes('/') || v.includes('\\')) {
            v = v.split(/[\\/]/).pop();
          }
        } else if (typeof v === 'object' && v !== null) {
          v = JSON.stringify(v);
          if (v.length > 50) v = v.substring(0, 50) + '...';
        } else {
          v = String(v);
        }
        if (typeof v === 'string' && !v.startsWith('"') && !v.startsWith('[')) {
          v = `"${v}"`;
        }
        s.push(v);
      }
      return `${toolName} ${s.join(' ')}`;
    } catch (e) {
      return rawStr;
    }
  }

  async addSystemMsg(txt) {
    this.turns.push({ system: txt });
    await this.render();
  }

  async loadDb(displayLog, clearDom = true) {
    while (this._loadPromise) {
      await this._loadPromise;
    }
    let resolveLock;
    this._loadPromise = new Promise(r => resolveLock = r);

    try {
      const source = displayLog || [];
      const oldTurns = [...this.turns];
      this.turns = [];
      const clearedTurns = new Set();
      if (clearDom) {
        document.getElementById('chat').innerHTML = '';
      }

    source.forEach(msg => {
      const role = String(msg.role || '').toLowerCase();
      const parts = msg.parts || [];

      if (role === 'user') {
        if (this.turns.length > 0) {
          this.turns[this.turns.length - 1].completed = true;
        }

        const isToolResult = parts.some(p => p.type === 'tool_result' || (p.type === 'text' && p.content && p.content.match(/^\[.*? Result\]\n/)));
        if (isToolResult) {
        } else {
          let existingTurn = oldTurns[this.turns.length];
          let isSame = existingTurn && existingTurn.messageId === msg.id;
          this.turns.push({
            user: parts[0]?.content || '',
            agent: isSame ? existingTurn.agent : null,
            liveContainer: isSame ? existingTurn.liveContainer : [],
            completed: false,
            durationMs: msg.run_meta ? (msg.run_meta.duration_ms || 0) : 0,
            messageId: msg.id,
            snapshot: msg.snapshot,
            hidden: msg.metadata ? msg.metadata.hidden : false,
            _pendingTools: []
          });
        }
      } else if (role === 'assistant') {
        const turn = this._currentTurn();
        if (msg.run_meta) {
          turn.completed = true;
          turn.durationMs = msg.run_meta.duration_ms || turn.durationMs;
        }
        const oldTurn = oldTurns[this.turns.length - 1] || {};
        
        const isSame = !oldTurn.completed && (oldTurn.messageId === msg.id);
        
        if (isSame) {
          turn.liveContainer = oldTurn.liveContainer;
          turn.agent = oldTurn.agent;
          turn.messageId = msg.id;
          turn.id = oldTurn.id;
          turn._skipDbParse = true;
        } else if (oldTurn && oldTurn.id) {
          turn.id = oldTurn.id;
          turn.messageId = msg.id;
          turn._needsFullRender = true;
        } else {
          turn.messageId = msg.id;
        }
        
        if (turn._skipDbParse) {
          return;
        }

        if (!clearedTurns.has(this.turns.length - 1)) {
          turn.liveContainer = [];
          turn.agent = '';
          clearedTurns.add(this.turns.length - 1);
        }
        
        if (msg.parts && Array.isArray(msg.parts)) {
          const parts = msg.parts;
          parts.forEach(p => {
            if (p.type === 'text') {
              const parsed = this._parseThinkBlocks(p.content, "");
              for (const e of parsed) {
                if (e.think) {
                  turn.liveContainer.push({ think: e.think, tools: [] });
                } else if (e.content) {
                  turn.agent = (turn.agent || '') + e.content;
                }
              }
            } else if (p.type === 'tool_use') {
              let toolStr = `${p.tool_name} ${p.arguments}`;
              try { toolStr = this.formatToolCall(toolStr); } catch (e) { }
              
              if (turn.liveContainer.length === 0) {
                turn.liveContainer.push({ think: null, tools: [] });
              }
              turn.liveContainer[turn.liveContainer.length - 1].tools.push(toolStr);
            }
          });
        }

        const partsSummary = parts.map(p => p.type === 'tool_use' ? p.tool_name : 'text:' + (p.content || '').substring(0, 30));


        if (msg.run_meta && msg.run_meta.duration_ms) {
          turn.completed = true;
          turn.durationMs = msg.run_meta.duration_ms;
        } else if (oldTurn.durationMs) {
          turn.completed = oldTurn.completed;
          turn.durationMs = oldTurn.durationMs;
        }
        if (msg.run_meta && msg.run_meta.workspace_changes) {
          turn.workspaceChanges = msg.run_meta.workspace_changes;
        } else if (oldTurn.workspaceChanges) {
          turn.workspaceChanges = oldTurn.workspaceChanges;
        }
      }
    });
    // No pending tools flush needed since they are pushed directly to liveContainer
    await this.render();
    } finally {
      const release = resolveLock;
      this._loadPromise = null;
      if (release) release();
    }
  }

  async addLiveEvent(evt) {
    const turn = this._currentTurn();
    const kind = evt.type || evt.kind || 'activity';
    let rawText = String(evt.text || evt.event || evt.message || evt.error || '');
    if (kind !== 'token' && kind !== 'think') {
      rawText = rawText.trim();
    }
    if (!rawText && kind !== 'token' && kind !== 'think' && kind !== 'replace_content') return;

    if (kind === 'think') {
      if (/\[Thought process omitted/i.test(rawText)) return;
      const beforeLen = turn.liveContainer.length;
      // const beforeTools = beforeLen > 0 ? turn.liveContainer[beforeLen - 1].tools.length : 0;
      if (turn.liveContainer.length === 0 || turn.liveContainer[turn.liveContainer.length - 1].tools.length > 0) {
        turn.liveContainer.push({ think: rawText, tools: [] });
      } else {
        const lastBlock = turn.liveContainer[turn.liveContainer.length - 1];
        if (lastBlock.think === 'Thinking...') {
          lastBlock.think = rawText;
        } else {
          lastBlock.think = (lastBlock.think || '') + rawText;
        }
      }
      // const lcSnapshot = turn.liveContainer.map(e => ({ think: e.think, tools: e.tools }));

    } else if (kind === 'action') {
      if (rawText.startsWith('Executing: ')) {
        let tool = rawText.replace(/^Executing:\s*/, '');
        tool = this.formatToolCall(tool);
        const beforeLen = turn.liveContainer.length;
        // const beforeTools = beforeLen > 0 ? turn.liveContainer[beforeLen - 1].tools.length : 0;
        if (turn.liveContainer.length === 0) {
          turn.liveContainer.push({ think: null, tools: [] });
        }
        turn.liveContainer[turn.liveContainer.length - 1].tools.push(tool);
        // const lcSnapshot = turn.liveContainer.map(e => ({ think: e.think, tools: e.tools }));

      } else return;
    } else if (kind === 'activity') {
      if (rawText === 'Compacting memory...') {
        if (turn.liveContainer.length === 0) {
          turn.liveContainer.push({ think: null, tools: [] });
        }
        turn.liveContainer[turn.liveContainer.length - 1].tools.push('compacting...');
      } else if (rawText.toLowerCase().includes('error')) {
        turn.liveContainer.push({ think: '❌ ' + rawText, tools: [] });
      } else return;
    } else if (kind === 'replace_content') {
      turn.agent = evt.content;
      const parsed = this._parseThinkBlocks(evt.content, "");
      const thinkEntries = parsed.filter(e => e.think).map(e => e.think);
      if (thinkEntries.length > 0 && turn.liveContainer.length > 0) {
        turn.liveContainer[turn.liveContainer.length - 1].think = thinkEntries[thinkEntries.length - 1];
      }
    } else if (kind === 'token') {
      turn.agent = (turn.agent || '') + rawText;
    } else if (kind === 'done' || kind === 'complete') {
      turn.completed = true;
      if (evt.duration_ms) turn.durationMs = evt.duration_ms;
      if (evt.workspace_changes) turn.workspaceChanges = evt.workspace_changes;
      // const lcSnapshot = turn.liveContainer.map(e => ({ think: e.think, tools: e.tools }));
    }

    requestAnimationFrame(async () => {
      await this.render();
    });
  }

  _parseThinkBlocks(content, toolsJson) {
    const results = [];
    const thinkRegex = /<(?:think|thought)>([\s\S]*?)(<\/(?:think|thought)>|$)/gi;
    let lastIdx = 0;
    let match;
    while ((match = thinkRegex.exec(content)) !== null) {
      if (match.index > lastIdx) {
        const between = content.substring(lastIdx, match.index);
        if (between.trim()) {
          results.push({ think: null, content: between });
        }
      }
      let thinkText = match[1].trim();
      if (thinkText && /\[Thought process omitted/i.test(thinkText)) {
        thinkText = "";
      }
      results.push({ think: thinkText, content: "" });
      lastIdx = thinkRegex.lastIndex;
    }
    if (lastIdx < content.length) {
      const remaining = content.substring(lastIdx);
      if (remaining.trim()) {
        results.push({ think: null, content: remaining });
      }
    }
    return results;
  }

  formatRunDuration(ms) {
    const seconds = Math.max(1, Math.round(ms / 1000));
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    const rest = seconds % 60;
    return rest ? `${minutes}m ${rest}s` : `${minutes}m`;
  }

  async render() {
    while (this._renderPromise) {
      await this._renderPromise;
    }
    let resolveLock;
    this._renderPromise = new Promise(r => resolveLock = r);

    try {
      const shouldFollow = chatIsNearBottom();
      const chat = document.getElementById('chat');

      if (this.turns.length === 0) {
        chat.innerHTML = '<div class="msg system"><div class="label">System</div><div class="bubble">Ready. Mention workspace context with @todo.py, @recent, @diff.</div></div>';
        if (shouldFollow) scrollChatToBottom();
        return;
      }

    if (chat.children.length === 1 && chat.children[0].classList.contains('system')) {
      chat.innerHTML = '';
    }

    while (chat.children.length > this.turns.length) {
      chat.removeChild(chat.lastChild);
    }

    for (let i = 0; i < this.turns.length; i++) {
      const turn = this.turns[i];
      let turnGroup = chat.children[i];
      const isLast = (i === this.turns.length - 1);

      if (!turnGroup) {
        turnGroup = document.createElement('div');
        turnGroup.className = 'chat-turn';
        chat.appendChild(turnGroup);
      } else {
        // Skip re-rendering fully completed historical turns to preserve DOM state
        if (!isLast && turnGroup.dataset.renderedFinal === "true") {
          continue;
        }
      }

      await this._renderTurn(turnGroup, turn);

      // Mark as final if it's a completed historical turn
      if (!isLast && turn.completed) {
        turnGroup.dataset.renderedFinal = "true";
      }
    }

    updateStickyPrompts();
    if (shouldFollow) scrollChatToBottom();
    } finally {
      const release = resolveLock;
      this._renderPromise = null;
      if (release) release();
    }
  }

  async _renderTurn(turnGroup, turn) {
    if (turn.system) {
      let sysD = turnGroup.querySelector('.msg.agent');
      if (!sysD) {
        sysD = document.createElement('div');
        sysD.className = 'msg agent';
        sysD.innerHTML = `<div class="label">System</div><div class="bubble">${esc(turn.system)}</div>`;
        turnGroup.appendChild(sysD);
      }
      return;
    }

    // 1. User Message
    if (turn.user) {
      if (turn.hidden) {
        let d = turnGroup.querySelector('.msg.user');
        if (d) d.remove();
      } else {
        let d = turnGroup.querySelector('.msg.user');
        if (!d) {
          d = document.createElement('div');
          d.className = 'msg user';

          let cleanedUser = (turn.user || '').trimStart();
          if (cleanedUser.startsWith('{') && cleanedUser.includes('"context":')) {
            const regex = /\r?\n\r?\n/g;
            let match;
            while ((match = regex.exec(cleanedUser)) !== null) {
              let possibleJson = cleanedUser.substring(0, match.index);
              try {
                let parsed = JSON.parse(possibleJson);
                if (parsed.context) {
                  cleanedUser = cleanedUser.substring(match.index + match[0].length);
                  break;
                }
              } catch (e) {
                // keep looking
              }
            }
          }

          let content = window.DOMPurify ? window.DOMPurify.sanitize(window.marked.parse(cleanedUser)) : esc(cleanedUser);
          content = content.replace(/(^|\s)(\/[a-zA-Z0-9_-]+)/g, '$1<span class="hl-slash">$2</span>');
          content = content.replace(/(^|\s)(@[a-zA-Z0-9_.-]+\/?)/g, (match, space, tag) => {
            return space + `<span class="${tag.endsWith('/') ? 'hl-folder' : 'hl-mention'}">${tag}</span>`;
          });
          d.innerHTML = `<div class="label">User</div><div class="bubble markdown-body">${content}</div>`;
          turnGroup.appendChild(d);
          lastUserMsg = d; // update global pointer
        }

        if (turn.snapshot && turn.messageId && !d.querySelector('.revert-btn')) {
          const btn = document.createElement('button');
          btn.className = 'revert-btn';
          btn.title = 'Revert workspace to this point';
          btn.textContent = '\u21B6';
          btn.dataset.messageId = turn.messageId;
          btn.onclick = async () => {
            const r = await fetch('/api/chat/revert', {
              method: 'POST', headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ message_id: turn.messageId, conversation_id: window.currentConversationId })
            });
            if (r.ok) {
              const data = await r.json();
              this.loadDb(data.display_log, true);
              await refresh();
            }
          };
          d.querySelector('.label').after(btn);
        }
      }
    }

    // 2. Live Logs (Background activity)
    if (turn.liveContainer.length > 0) {
      let wrapper = turnGroup.querySelector('.live-container');
      if (!wrapper) {
        wrapper = document.createElement('div');
        wrapper.className = 'live-container';
        wrapper.style.display = 'flex';
        wrapper.style.flexDirection = 'column';
        wrapper.style.gap = '6px';
        wrapper.style.marginBottom = '10px';
        wrapper.style.marginTop = '2px';

        const details = document.createElement('details');
        details.className = 'inline-live';

        const sharedLogHtml = `<div class="compact-log live-log" style="margin-top:8px; padding-left:16px; display:flex; flex-direction:column; gap:4px; max-height:200px; overflow-y:auto; overflow-x:hidden; scrollbar-width:thin; scrollbar-color: rgba(255,255,255,0.14) transparent;"></div>`;

        if (turn.completed) {
          details.classList.add('live-compact');
          details.open = false;
          const durationTxt = this.formatRunDuration(turn.durationMs || 0);
          details.innerHTML = `<summary><span class="compact-label">Worked for ${esc(durationTxt)}</span><span class="compact-arrow">›</span></summary>` + sharedLogHtml;
        } else {
          details.classList.add('flat-live');
          details.open = true;
          details.innerHTML = `<summary style="cursor:pointer; display:flex; align-items:center;"><span style="margin-right:8px; font-size:10px; opacity:0.7;">▼</span><span class="livetext" style="font-weight:600;">Running background tasks...</span><span class="timer" style="margin-left:8px; opacity:0.6; font-variant-numeric: tabular-nums;"></span></summary>` + sharedLogHtml;
        }

        details.dataset.completedState = turn.completed ? "true" : "false";
        wrapper.appendChild(details);

        const agentNode = turnGroup.querySelector('.msg.agent');
        if (agentNode) {
          turnGroup.insertBefore(wrapper, agentNode);
        } else {
          turnGroup.appendChild(wrapper);
        }
      } else {
        const details = wrapper.querySelector('details.inline-live');
        const isCompleted = turn.completed;
        const currentCompletedState = details.dataset.completedState === "true";

        if (isCompleted) {
          if (!currentCompletedState) {
            details.classList.remove('flat-live');
            details.classList.add('live-compact');
            details.open = false;
            details.dataset.completedState = "true";
          }

          // ALWAYS update duration text if it's completed, in case the value changed (e.g. from 1s to the real duration)
          const durationTxt = this.formatRunDuration(turn.durationMs || 0);
          const summary = details.querySelector('summary');
          if (summary) {
            summary.innerHTML = `<span class="compact-label">Worked for ${esc(durationTxt)}</span><span class="compact-arrow">›</span>`;
          }
        }
      }

      const details = wrapper.querySelector('details.inline-live');
      const log = details.querySelector('.compact-log');
      
      if (turn._needsFullRender) {
        log.innerHTML = '';
        log.dataset.count = "0";
        for (const key in log.dataset) {
          if (key.startsWith('t_')) delete log.dataset[key];
        }
        delete turn._needsFullRender;
      }
      
      const renderedCount = parseInt(log.dataset.count || "0", 10);
      const isScrolledToBottom = (log.scrollHeight - log.scrollTop - log.clientHeight) < 10;

      if (turn.liveContainer.length > renderedCount) {
        for (let i = renderedCount; i < turn.liveContainer.length; i++) {
          const block = turn.liveContainer[i];
          if (block.think) {
            let html = window.DOMPurify ? window.DOMPurify.sanitize(window.marked.parse(block.think)) : esc(block.think);
            if (!html.trim()) html = esc(block.think);
            const entry = document.createElement('div');
            entry.className = 'live-entry markdown-body';
            entry.dataset.thinkIdx = i;
            entry.dataset.len = block.think.length;
            entry.innerHTML = html;
            log.appendChild(entry);
          }
          if (block.tools) {
            block.tools.forEach(t => {
              const entry = document.createElement('div');
              entry.className = 'live-entry markdown-body action-entry';
              entry.style.animation = 'none';
              if (t === 'compacting...') {
                entry.innerHTML = `<p>⚙️ compacting...</p>`;
              } else {
                entry.innerHTML = `<p>⚙️ ${esc(t)}</p>`;
              }
              log.appendChild(entry);
            });
            log.dataset['t_' + i] = block.tools.length;
          }
        }
        log.dataset.count = turn.liveContainer.length;
      }
      for (let i = 0; i < Math.min(turn.liveContainer.length, renderedCount); i++) {
        const block = turn.liveContainer[i];
        if (block.think) {
          const thinkNode = log.querySelector(`[data-think-idx="${i}"]`);
          if (thinkNode) {
            const currentLen = parseInt(thinkNode.dataset.len || "0", 10);
            if (block.think.length > currentLen) {
              thinkNode.innerHTML = window.DOMPurify ? window.DOMPurify.sanitize(window.marked.parse(block.think)) : esc(block.think);
              thinkNode.dataset.len = block.think.length;
            }
          }
        }
        if (!block.tools) continue;
        const toolKey = 't_' + i;
        const renderedTools = parseInt(log.dataset[toolKey] || "0", 10);
        if (block.tools.length > renderedTools) {
          for (let j = renderedTools; j < block.tools.length; j++) {
            const entry = document.createElement('div');
            entry.className = 'live-entry markdown-body action-entry';
            entry.style.animation = 'none';
            entry.innerHTML = `<p>⚙️ ${esc(block.tools[j])}</p>`;
            log.appendChild(entry);
          }
          log.dataset[toolKey] = block.tools.length;
        }
      }
      if (isScrolledToBottom) {
        log.scrollTop = log.scrollHeight;
      }
    } else {
      let wrapper = turnGroup.querySelector('.live-container');
      if (wrapper) wrapper.remove();
    }

    // 3. Agent Message
    if (turn.agent || turn.liveContainer.length > 0) {
      let d = turnGroup.querySelector('.msg.agent');
      if (!d) {
        d = document.createElement('div');
        d.className = 'msg agent';
        d.innerHTML = `<div class="label">Agent</div><div class="bubble markdown-body"></div>`;
        turnGroup.appendChild(d);
      }

      let content = turn.agent || "";

      // Parse all <think> tags from DeepSeek R1/Qwythos (hide completely in final response, but extract to live block)
      let match;
      const thinkRegex = /<(?:think|thought)>([\s\S]*?)(<\/(?:think|thought)>|$)/gi;
      let blockIndex = 0;
      while ((match = thinkRegex.exec(content)) !== null) {
        let thinkText = match[1].trim();
        if (thinkText && !/\[Thought process omitted/i.test(thinkText)) {
          // Find next available non-error block
          while (blockIndex < turn.liveContainer.length &&
            turn.liveContainer[blockIndex].think &&
            turn.liveContainer[blockIndex].think.startsWith('❌')) {
            blockIndex++;
          }

          if (blockIndex >= turn.liveContainer.length) {
            turn.liveContainer.push({ think: null, tools: [] });
          }

          if (!turn.liveContainer[blockIndex].think) {
            turn.liveContainer[blockIndex].think = thinkText;
          }
          blockIndex++;
        }
      }
      content = content.replace(/<(?:think|thought)>([\s\S]*?)(<\/(?:think|thought)>|$)/gi, '').trim();

      // If the model put its entire final response inside a <think> block (common with Qwythos),
      // it would leave the main bubble completely empty. Promote the last thought back to the main bubble.
      if (!content && turn.completed && turn.liveContainer.length > 0) {
        const lastIdx = turn.liveContainer.length - 1;
        const lastBlock = turn.liveContainer[lastIdx];
        if (lastBlock.think && lastBlock.tools.length === 0) {
          content = lastBlock.think;
          
          // Remove from the live container DOM to prevent it from appearing in both places
          const log = turnGroup.querySelector('.compact-log');
          if (log) {
            const thinkNode = log.querySelector(`[data-think-idx="${lastIdx}"]`);
            if (thinkNode) thinkNode.remove();
          }
        }
      }

      if (window.marked) {
        content = md(content);
      } else {
        content = esc(content);
      }

      const bubble = d.querySelector('.bubble');
      if (bubble) {
        bubble.innerHTML = content;
      }

      // Render any UI widgets for changed files
      if (turn.workspaceChanges) {
        let changedFiles = [];
        if (turn.workspaceChanges.created) changedFiles.push(...turn.workspaceChanges.created);
        if (turn.workspaceChanges.modified) changedFiles.push(...turn.workspaceChanges.modified);
        if (changedFiles.length > 0 && window.latestArtifacts) {
          if (!d.dataset.renderedDiffs) {
            let relevantDiffs = matchingDiffArtifactsForChangedFiles(changedFiles);
            if (relevantDiffs.length > 0) {
              renderDiffReviewWidget(d, relevantDiffs);
              d.dataset.renderedDiffs = "true";
            }
          }
        }
      }
      if (!content && !turn.workspaceChanges) {
        const hasTools = turn.liveContainer.some(b => b.tools && b.tools.length > 0);
        if (!hasTools) {
          d.remove();
        }
      }
    } else {
      let d = turnGroup.querySelector('.msg.agent');
      if (d) d.remove();
    }
    _updateTokenDisplay();
  }

  updateTimer() {
    if (!running) return;
    const s = Math.floor((Date.now() - runStartTime) / 1000);
    const nodes = document.querySelectorAll('.inline-live .timer');
    nodes.forEach(tEl => {
      tEl.textContent = `[${Math.floor(s / 60).toString().padStart(2, '0')}:${(s % 60).toString().padStart(2, '0')}]`;
    });
  }
}

const conversationState = new ConversationState();

// Wrappers for old API
async function addMsg(role, txt, opts) {
  if (role.toLowerCase() === 'system') conversationState.addSystemMsg(txt);
  else if (role.toLowerCase() === 'user') {
    if (conversationState.turns.length > 0) {
      conversationState.turns[conversationState.turns.length - 1].completed = true;
    }
    conversationState.turns.push({ user: txt, agent: null, liveContainer: [], completed: false, durationMs: 0 });
    await conversationState.render();
  }
  else if (role.toLowerCase() === 'agent') {
    const turn = conversationState._currentTurn();
    turn.agent = txt;
    await conversationState.render();
  }
}
async function updateInlineLive(text, state, opts) {
  await conversationState.addLiveEvent({ type: 'think', text: text });
}
async function compactLiveTranscript(durationMs) {
  const turn = conversationState._currentTurn();
  turn.durationMs = durationMs;
  turn.completed = true;
  await conversationState.render();
}
function updateTimer() {
  conversationState.updateTimer();
}
async function renderSession(displayLog, clearDom = true) {
  await conversationState.loadDb(displayLog, clearDom);
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

async function setRunningState(isRunning, isStopping = false, backendStartTime = 0) {
  if (isRunning && !running) { window._newDiffArtifacts = []; _notifQueued = false; }
  running = isRunning;
  stopping = isStopping;
  document.body.classList.toggle('running', isRunning);
  $('editor').disabled = !projectSelected;
  $('attachBtn').disabled = isRunning || !projectSelected;
  updateSendStopButtons();
  if (isStopping) await updateInlineLive('Stopping', 'stopping');
  if (isRunning && !isStopping) {
    if (backendStartTime > 0) {
      runStartTime = backendStartTime;
    } else if (!runStartTime) {
      runStartTime = Date.now();
    }
    if (!runTimerInterval) {
      runTimerInterval = setInterval(updateTimer, 1000);
      updateTimer();
    }
  } else if (!isRunning) {
    clearInterval(runTimerInterval);
    runTimerInterval = null;
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

async function renderProjects(projects = []) {
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
          if (d.ok) await renderSession(d.display_log);
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
          if (d.ok) await renderSession(d.display_log);
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
    window.currentConversationId = a.active_conversation_id;
    $('workTitle').textContent = projectSelected ? ((a.project && a.project.workspace) ? a.project.workspace.split(/[\\/]/).pop() : 'Workspace') : 'Select a project';
    $('workSub').textContent = projectSelected ? (a.workspace || '') : 'No workspace active.';
    _updateTokenDisplay();
    renderBackendDiagnostics(a.backend_diagnostics || {});
    await setRunningState(!!a.running, !!a.stop_requested, a.start_time);
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



    if (a.projects !== undefined) await renderProjects(a.projects);
    if (firstStatus) {
      const isInitialLoad = document.getElementById('chat').children.length === 0 ||
        (document.getElementById('chat').children.length === 1 && document.getElementById('chat').children[0].classList.contains('system'));

      await renderSession(a.display_log, isInitialLoad);
      firstStatus = false;
      if (a.running) {
        try {
          const act = await fetch('/api/activity').then(res => res.json());
          const events = act.events || a.live_events || [];
          if (events.length > 0) {
            events.forEach(async evt => {
              if (typeof evt === 'string') {
                await ({ type: 'activity', event: evt }, true);
              } else {
                await handleAgentEvent(evt, true);
              }
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
          await updateInlineLive(`Created artifact: ${newArt.title}`, running ? 'running' : 'done', {
            log: true
          });
        }
      }
    }
    if (a.running && !sseSource) {
      try {
        sseSource = new EventSource('/api/stream/activity');
        sseSource.onmessage = await sseOnMessage;
        sseSource.onerror = await sseOnError;
      } catch (ex) {
        console.log('SSE not available, falling back to polling');
      }
      await updateInlineLive(a.activity || 'Thinking', a.stop_requested ? 'stopping' : 'running', {
        log: false
      })
    } else if (!a.running && running) {
      await setRunningState(false, false);
      if (!_notifQueued) {
        _notifQueued = true;
        _playNotificationSound();
      }
      if (a.display_log) {
        await renderSession(a.display_log, false);
      }
      if (typeof loadExplorerList === 'function' && typeof explorerPath !== 'undefined') {
        loadExplorerList(explorerPath);
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
    .catch(() => { });
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
    await renderSession([]);
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
// Enhancement: SSE streaming for live activity updates  
async function handleAgentEvent(d, isHistory = false) {
  if (d.conversation_id && window.currentConversationId && d.conversation_id !== window.currentConversationId) {
    return; // Filter out live events belonging to a different background session
  }
  if (isHistory && (d.type === 'think' || d.type === 'action' || d.type === 'token' || d.type === 'response')) {
    return;
  }

  if (d.type === 'token_usage') {
    totalTokens.prompt = d.total_prompt || 0;
    totalTokens.completion = d.total_completion || 0;
    _updateTokenDisplay();
    return;
  }
  if (d.type === 'primary_changed') {
    if (!isHistory && !running) await refresh();
    return;
  }

  if (d.type === 'error') {
    const errMsg = d.error || d.message || 'An error occurred';
    if (!isHistory) await conversationState.addSystemMsg('⚠️ ' + errMsg);
    if (sseSource) { sseSource.close(); sseSource = null; }
    clearInterval(poll); poll = null;
    await setRunningState(false, false);
    if (!isHistory) {
      const t = conversationState._currentTurn();
      t.completed = true;
      await conversationState.render();
    }
    firstStatus = true;
    if (!isHistory) await refresh();
    return;
  }

  if (d.type === 'complete') {

    _notifQueued = true;
    if (!isHistory) _playNotificationSound();

    if (d.response && d.reason !== 'cancelled') {
      await conversationState.addLiveEvent({ type: 'replace_content', content: d.response });
    }
    await conversationState.addLiveEvent({
      type: 'done',
      duration_ms: d.duration_ms,
      workspace_changes: d.workspace_changes
    });

    if (sseSource) {
      sseSource.close();
      sseSource = null;
    }
    clearInterval(poll);
    poll = null;
    await setRunningState(false, false);
    firstStatus = true;
    if (!isHistory) {
      // Wait 500ms for the backend to commit final db records (snapshot ID, run_meta)
      await new Promise(r => setTimeout(r, 500));
      await refresh();
      await conversationState.render();
      if (typeof loadExplorerList === 'function' && typeof explorerPath !== 'undefined') {
        loadExplorerList(explorerPath);
      }
    }
    return;
  }

  if (d.type === 'prompt') {
    if (!isHistory) {
      $('promptToolName').innerText = d.tool || 'unknown';
      let cmdDisplay = d.command || '';
      if (typeof cmdDisplay === 'object') {
        cmdDisplay = JSON.stringify(cmdDisplay, null, 2);
      }
      $('promptToolCommand').innerText = cmdDisplay;
      $('toolPromptModal').classList.add('open');

      const handleDecision = async (approve) => {
        $('toolPromptModal').classList.remove('open');
        $('promptApproveBtn').onclick = null;
        $('promptRejectBtn').onclick = null;
        try {
          await fetch('/api/tool/approve', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ call_id: d.call_id, approve })
          });
        } catch (e) { console.error('Failed to send tool approval', e); }
      };

      $('promptApproveBtn').onclick = () => handleDecision(true);
      $('promptRejectBtn').onclick = () => handleDecision(false);
    }
    return;
  }

  // Default pass-through to new state manager
  await conversationState.addLiveEvent(d);
}
async function sseOnMessage(e) {
  try {
    const d = JSON.parse(e.data);
    await handleAgentEvent(d, false);
  } catch (ex) { }
}

async function sseOnError() {
  if (sseSource) {
    sseSource.close();
    sseSource = null
  }
  // High fix #5: start polling fallback immediately so live activity doesn't freeze
  if (!poll) poll = setInterval(refresh, 900);
  // Attempt SSE reconnect after 2s (only while agent is still running)
  setTimeout(async () => {
    if (running && !sseSource) {
      try {
        sseSource = new EventSource('/api/stream/activity');
        sseSource.onmessage = await sseOnMessage;
        sseSource.onerror = await sseOnError;
        sseSource.onopen = async function () {
          if (poll) {
            clearInterval(poll);
            poll = null;
          }
          await refresh();
        };
      } catch (ex) {
        /* stay on poll fallback */
      }
    }
  }, 2000);
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
        body: JSON.stringify({ prompt: text, conversation_id: window.currentConversationId })
      });
      const data = await r.json();
      if (r.ok && data.ok) {
        setEditorText('');
        await addMsg('User', text);
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

  await addMsg('User', text);
  await updateInlineLive('Thinking...', 'running');
  await setRunningState(true, false);
  setEditorText('');

  try {
    sseSource = new EventSource('/api/stream/activity');
    sseSource.onmessage = await sseOnMessage;
    sseSource.onerror = await sseOnError;
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
        features: mergedFeatures(),
        conversation_id: window.currentConversationId
      }),
      signal: runAbort.signal
    });
    clearTimeout(runTimeout);
    const data = await readJsonOrText(r);
    if (!r.ok || data.ok === false) {
      throw new Error(data.error || 'Request failed')
    }
    // engine thread now alive — ensure stop button is visible
    await setRunningState(true, false);
    // POST returns immediately. Results delivered via SSE 'complete' event.
  } catch (e) {
    postError = true;
    await updateInlineLive('Failed', 'failed');
    if (e.name === 'AbortError' || e.message.includes('abort') || e.message.includes('cancel')) {
      try {
        const r_status = await fetch('/api/status?full=true');
        const d_status = await r_status.json();
        if (d_status.display_log && d_status.display_log.length > 0) {
          await renderSession(d_status.display_log);
        } else {
          await addMsg('Agent', 'ERROR: ' + e.message + '\n\nDiagnostics and timeline export may have more detail.');
        }
      } catch (ex) {
        await addMsg('Agent', 'ERROR: ' + e.message + '\n\nDiagnostics and timeline export may have more detail.');
      }
    } else {
      await addMsg('Agent', 'ERROR: ' + e.message + '\n\nDiagnostics and timeline export may have more detail.');
    }
  } finally {
    if (postError) {
      // Error path: do full cleanup immediately
      if (sseSource) { sseSource.close(); sseSource = null }
      clearInterval(poll); poll = null;
      await setRunningState(false, false);
      await compactLiveTranscript();
    }
    // Success path: SSE 'complete' handler handles cleanup.
    isSending = false;
    await refresh();
    await conversationState.render();
  }
}

async function stopRun() {
  if (!running) return;
  await setRunningState(true, true);
  try {
    await fetch('/api/stop', {
      method: 'POST'
    })
  } catch (e) {
    await addMsg('Agent', 'STOP ERROR: ' + e.message)
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
  } catch (e) {
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
      const pc = $('providersContainer');
      pc.innerHTML = '';
      if (c.providers && c.providers.length > 0) {
        c.providers.forEach((p, idx) => addProviderUI(p, idx === 0));
      } else {
        addProviderUI({ base_url: '', api_key: '' }, true);
      }
      if (c.shell_access === 'ask') {
        $('cfgShellAccess').value = 'ask';
      } else {
        $('cfgShellAccess').value = 'allow';
      }

      uiRunTimeoutSeconds = parseFloat(c.ui_run_timeout || c.request_timeout || 3600) || 3600;
    }
  } catch (e) {
    console.error('Failed to load llm config', e)
  }
  $('settingsModal').classList.add('open')
}
function addProviderUI(p, isPrimary) {
  const pc = $('providersContainer');
  const div = document.createElement('div');
  div.className = 'provider-item config-group';
  div.dataset.id = p.id || '';
  div.style.border = '1px solid #30363d';
  div.style.padding = '10px';
  div.style.marginBottom = '10px';
  div.style.borderRadius = '6px';
  div.style.position = 'relative';

  const title = isPrimary ? 'Primary Provider' : 'Fallback Provider';
  const removeBtn = isPrimary ? '' : `<button type="button" class="remove-prov" title="Remove" style="position:absolute; right:10px; top:8px; background:none; border:none; color:#f85149; cursor:pointer; font-size:16px;">×</button>`;
  const makePrimaryBtn = isPrimary ? '' : `<button type="button" class="make-primary-prov" style="position:absolute; right:40px; top:12px; background:none; border:none; color:var(--text-color); cursor:pointer; font-size:11px; text-decoration:underline;">Make Primary</button>`;

  div.innerHTML = `
    <div style="font-weight: 600; margin-bottom: 8px; font-size: 12px; color: #8b949e;">${title}</div>
    
    <label class="config-label" style="margin-top: 8px;">Model Name (e.g. gpt-4o)</label>
    <input type="text" class="config-input cfg-model" value="${esc(p.model || '')}">
    
    <label class="config-label" style="margin-top: 8px;">Base URL</label>
    <input type="text" class="config-input cfg-base-url" value="${esc(p.base_url || '')}" placeholder="https://api.openai.com/v1">
    
    <label class="config-label" style="margin-top: 8px;">API Key</label>
    <input type="password" class="config-input cfg-api-key" value="${esc(p.api_key || '')}" placeholder="sk-...">
    
    <div style="display:flex; gap:10px; margin-top: 8px;">
      <div style="flex:1;">
        <label class="config-label">Context Window</label>
        <input type="number" class="config-input cfg-context-window" value="${p.context_window || 0}" placeholder="0 (Auto)">
      </div>
      <div style="flex:1;">
        <label class="config-label">Max Messages</label>
        <input type="number" class="config-input cfg-max-messages" value="${p.max_messages || 0}" placeholder="0 (Auto)">
      </div>
    </div>
    
    <div style="display:flex; gap:10px; margin-top: 8px;">
      <div style="flex:1;">
        <label class="config-label">Price / 1M Input Tokens ($)</label>
        <input type="number" class="config-input cfg-input-price" value="${p.input_price || 0}" step="0.01" min="0" placeholder="Auto from catalog">
      </div>
      <div style="flex:1;">
        <label class="config-label">Price / 1M Output Tokens ($)</label>
        <input type="number" class="config-input cfg-output-price" value="${p.output_price || 0}" step="0.01" min="0" placeholder="Auto from catalog">
      </div>
    </div>
    
    <label class="config-label" style="margin-top: 8px; display:flex; align-items:center; gap:8px;">
      <input type="checkbox" class="cfg-disable-vision" ${p.disable_vision ? 'checked' : ''}> Disable Vision (Strip Images)
    </label>
    
    <div style="margin-top: 8px; display: flex; align-items: center; gap: 8px;">
      <button type="button" class="diag-button prov-test-btn" data-provider-id="${esc(p.id || '')}">Test Connection</button>
      <span class="prov-test-result" style="font-size: 12px; color: var(--text-muted);"></span>
    </div>
    
    ${makePrimaryBtn}
    ${removeBtn}
  `;
  if (!isPrimary) {
    div.querySelector('.remove-prov').onclick = () => div.remove();
    div.querySelector('.make-primary-prov').onclick = () => {
      const pcContainer = $('providersContainer');
      const allDivs = Array.from(pcContainer.querySelectorAll('.provider-item'));
      const oldIndex = allDivs.indexOf(div);

      const pList = allDivs.map((el) => {
        return {
          id: el.dataset.id,
          model: el.querySelector('.cfg-model').value,
          base_url: el.querySelector('.cfg-base-url').value,
          api_key: el.querySelector('.cfg-api-key').value,
          disable_vision: el.querySelector('.cfg-disable-vision').checked,
          context_window: parseInt(el.querySelector('.cfg-context-window').value) || 0,
          max_messages: parseInt(el.querySelector('.cfg-max-messages').value) || 0
        };
      });

      const item = pList.splice(oldIndex, 1)[0];
      pList.unshift(item);

      pcContainer.innerHTML = '';
      pList.forEach((p, idx) => addProviderUI(p, idx === 0));

      // Automatically save settings so the new primary takes effect immediately
      $('settingsSave').click();
    };
  }
  div.querySelector('.prov-test-btn').onclick = async function () {
    const btn = this;
    const resultEl = div.querySelector('.prov-test-result');
    const providerId = btn.dataset.providerId;
    if (!providerId) { resultEl.textContent = 'No provider ID'; return; }
    btn.disabled = true;
    btn.textContent = 'Testing...';
    resultEl.textContent = 'Connecting...';
    resultEl.style.color = 'var(--text-muted)';
    try {
      const r = await fetch('/api/diagnostics/model-test?provider=' + encodeURIComponent(providerId), { method: 'POST' });
      const d = await readJsonOrText(r);
      if (d.ok) {
        resultEl.innerHTML = '✅ OK <small>(' + esc(d.detail || '') + ')</small>';
        resultEl.style.color = 'var(--success)';
      } else {
        resultEl.innerHTML = '❌ ERR <small>' + esc(d.detail || '') + '</small>';
        resultEl.style.color = 'var(--danger)';
      }
    } catch (e) {
      resultEl.textContent = 'Error: ' + e.message;
      resultEl.style.color = 'var(--danger)';
    } finally {
      btn.disabled = false;
      btn.textContent = 'Test Connection';
    }
  };
  pc.appendChild(div);
}

$('addProviderBtn').onclick = () => addProviderUI({ base_url: '', api_key: '' }, false);

$('settingsBtn').onclick = openSettings;
$('settingsClose').onclick = () => $('settingsModal').classList.remove('open');
$('settingsSave').onclick = async () => {
  const pList = Array.from(document.querySelectorAll('.provider-item')).map((el, idx) => {
    return {
      id: el.dataset.id || '',
      model: el.querySelector('.cfg-model').value,
      base_url: el.querySelector('.cfg-base-url').value,
      api_key: el.querySelector('.cfg-api-key').value,
      disable_vision: el.querySelector('.cfg-disable-vision').checked,
      context_window: parseInt(el.querySelector('.cfg-context-window').value) || 0,
      max_messages: parseInt(el.querySelector('.cfg-max-messages').value) || 0,
      input_price: parseFloat(el.querySelector('.cfg-input-price')?.value) || 0,
      output_price: parseFloat(el.querySelector('.cfg-output-price')?.value) || 0
    };
  });
  const p = {
    providers: pList,
    shell_access: $('cfgShellAccess').value,
  };
  try {
    await fetch('/api/config/llm', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(p)
    })
    if (pList.length > 0) updateModelTag(pList[0].model);
  } catch (e) {
    console.error('Failed to save llm config', e)
  }
  $('settingsModal').classList.remove('open')
};
async function openMcpSettings() {
  $('settingsModal').classList.remove('open');
  try {
    const r = await fetch('/api/config/mcp');
    const c = await r.json();
    const mc = $('mcpContainer');
    mc.innerHTML = '';
    if (c && c.servers && Object.keys(c.servers).length > 0) {
      for (const [id, srv] of Object.entries(c.servers)) {
        addMcpUI(id, srv);
      }
    } else {
      addMcpUI('my-mcp', { command: ['npx', '-y', '@modelcontextprotocol/server-postgres'], environment: {} });
    }
  } catch (e) {
    console.error('Failed to load mcp config', e);
  }
  $('mcpModal').classList.add('open');
}

function addMcpUI(id, srv) {
  const mc = $('mcpContainer');
  const div = document.createElement('div');
  div.className = 'mcp-item config-group';
  div.style.border = '1px solid #30363d';
  div.style.padding = '10px';
  div.style.marginBottom = '10px';
  div.style.borderRadius = '6px';
  div.style.position = 'relative';

  let cmdStr = '';
  if (srv.command) {
    cmdStr = srv.command.join(' ');
  }
  let envStr = '';
  if (srv.environment) {
    envStr = Object.entries(srv.environment).map(([k, v]) => `${k}=${v}`).join('\n');
  }

  div.innerHTML = `
    <button type="button" class="remove-mcp" style="position:absolute; right:10px; top:10px; background:none; border:none; color:#f85149; cursor:pointer;">×</button>
    <label class="config-label">Server ID</label>
    <input type="text" class="config-input mcp-id" value="${esc(id)}">
    
    <label class="config-label" style="margin-top: 8px;">Command</label>
    <input type="text" class="config-input mcp-cmd" value="${esc(cmdStr)}" placeholder="e.g. npx -y @modelcontextprotocol/server-github">
    
    <label class="config-label" style="margin-top: 8px;">Environment Variables</label>
    <textarea class="config-input mcp-env" style="min-height: 60px; font-family: monospace; font-size: 11px;" placeholder="GITHUB_TOKEN=abc...
DATABASE_URL=postgres://...">${esc(envStr)}</textarea>
    
    <label class="config-label" style="margin-top: 8px; display:flex; align-items:center; gap:8px;">
      <input type="checkbox" class="mcp-disabled" ${srv.disabled ? 'checked' : ''}> Disabled
    </label>
  `;
  div.querySelector('.remove-mcp').onclick = () => div.remove();
  mc.appendChild(div);
}

$('openMcpModalBtn').onclick = openMcpSettings;
$('mcpClose').onclick = () => $('mcpModal').classList.remove('open');
$('addMcpBtn').onclick = () => addMcpUI('new-mcp', { command: [], environment: {} });

$('mcpSave').onclick = async () => {
  const servers = {};
  Array.from(document.querySelectorAll('.mcp-item')).forEach(el => {
    const id = el.querySelector('.mcp-id').value.trim();
    if (!id) return;
    const cmdStr = el.querySelector('.mcp-cmd').value.trim();
    const envStr = el.querySelector('.mcp-env').value;
    const disabled = el.querySelector('.mcp-disabled').checked;

    // Split command by space
    const command = cmdStr ? cmdStr.split(' ').filter(c => c) : [];

    // Parse env
    const environment = {};
    envStr.split('\n').forEach(line => {
      const parts = line.split('=');
      if (parts.length >= 2) {
        const k = parts[0].trim();
        const v = parts.slice(1).join('=').trim();
        if (k) environment[k] = v;
      }
    });

    servers[id] = {
      type: "local",
      command: command,
      environment: environment,
      disabled: disabled
    };
  });

  try {
    await fetch('/api/config/mcp', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ servers: servers })
    });
  } catch (e) {
    console.error('Failed to save mcp config', e);
  }
  $('mcpModal').classList.remove('open');
};

async function openCompactionSettings() {
  $('settingsModal').classList.remove('open');
  try {
    const r = await fetch('/api/config/compaction');
    const c = await r.json();
    if (c) {
      $('cfgCompactionAuto').checked = c.auto || false;
      $('cfgCompactionTailTurns').value = c.tail_turns || 10;
      $('cfgCompactionPreserveTokens').value = c.preserve_recent_tokens || 1000;
      $('cfgCompactionReserved').value = c.reserved || 2000;
      $('cfgCompactionTruncation').value = c.tool_truncation_limit || 10000;
      $('cfgCompactionPrune').checked = c.prune || false;
      $('cfgCompactionModel').value = c.model || '';
      $('cfgCompactionBaseURL').value = c.base_url || '';
      $('cfgCompactionAPIKey').value = c.api_key || '';
    }
  } catch (e) {
    console.error('Failed to load compaction config', e);
  }
  $('compactionModal').classList.add('open');
}

$('openCompactionBtn').onclick = openCompactionSettings;
$('compactionClose').onclick = () => $('compactionModal').classList.remove('open');

$('compactionSave').onclick = async () => {
  const payload = {
    auto: $('cfgCompactionAuto').checked,
    tail_turns: parseInt($('cfgCompactionTailTurns').value) || 10,
    preserve_recent_tokens: parseInt($('cfgCompactionPreserveTokens').value) || 1000,
    reserved: parseInt($('cfgCompactionReserved').value) || 2000,
    tool_truncation_limit: parseInt($('cfgCompactionTruncation').value) || 10000,
    prune: $('cfgCompactionPrune').checked,
    model: $('cfgCompactionModel').value.trim() || undefined,
    base_url: $('cfgCompactionBaseURL').value.trim() || undefined,
    api_key: $('cfgCompactionAPIKey').value.trim() || undefined,
  };

  try {
    await fetch('/api/config/compaction', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    });
  } catch (e) {
    console.error('Failed to save compaction config', e);
  }
  $('compactionModal').classList.remove('open');
};

$('send').onclick = send;
$('stopBtn').onclick = stopRun;
$('attachBtn').onclick = () => $('fileInput').click();
$('fileInput').onchange = async e => await (uploadFiles(e.target.files).catch(async err => await addMsg('Agent', 'UPLOAD ERROR: ' + err.message)));
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
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode: value })
  }).catch(() => { });
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
(async () => { await refresh(); })();


if ($('newChatBtn')) $('newChatBtn').onclick = async () => {
  const r = await fetch('/api/chat/new', {
    method: 'POST'
  });
  const d = await r.json();
  if (d.ok) await renderSession(d.display_log);
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
  return `qf_artifacts_${suffix}:${currentArtifactWorkspace || 'global'}`
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
      <div class="artifact-item-title">${esc(art.title)}${art.version_count ? ` <span style="color:var(--text-muted);font-size:11px;">v${art.version_count + 1}</span>` : ''}</div>
      <div class="artifact-item-preview">${esc(String(art.content || '').replace(/<[^>]+>/g, '').substring(0, 70))}...</div>
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
        ${dir ? `<span class="diff-widget-filepath">${esc(dir)}</span>` : ''}
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
              body: JSON.stringify({ message_id: msgId, path: file.path, conversation_id: window.currentConversationId })
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
  switch (ext) {
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
      "Cmd-S": function (cm) { $('artifactSaveBtn').click(); },
      "Ctrl-S": function (cm) { $('artifactSaveBtn').click(); }
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
        try { document.execCommand('copy'); } catch (err) { }
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

// --- Workspace File Explorer Logic (flat list, Windows Explorer style) ---
const tabWorkspaces = document.getElementById('tabWorkspaces');
const tabExplorer = document.getElementById('tabExplorer');
const explorerTree = document.getElementById('explorerTree');
const workspaceActions = document.getElementById('workspaceActions');
const explorerActions = document.getElementById('explorerActions');
const explorerActionsBottom = document.getElementById('explorerActionsBottom');
const btnEditExplorerItem = document.getElementById('editExplorerItem');
const explorerListEl = document.getElementById('explorerList');
const explorerBreadcrumbEl = document.getElementById('explorerBreadcrumb');
const explorerBackBtn = document.getElementById('explorerBack');

let explorerPath = '';
let explorerSelection = '';
let clipboard = { action: null, srcPath: '' };

if (tabWorkspaces && tabExplorer) {
  tabWorkspaces.addEventListener('click', () => {
    tabWorkspaces.classList.add('active');
    tabExplorer.classList.remove('active');
    if ($('projectList')) $('projectList').style.display = 'block';
    if (explorerTree) explorerTree.style.display = 'none';
    if (workspaceActions) workspaceActions.style.display = 'flex';
    if (explorerActions) explorerActions.style.display = 'none';
    if (explorerActionsBottom) explorerActionsBottom.style.display = 'none';
  });

  tabExplorer.addEventListener('click', () => {
    tabExplorer.classList.add('active');
    tabWorkspaces.classList.remove('active');
    if ($('projectList')) $('projectList').style.display = 'none';
    if (explorerTree) explorerTree.style.display = 'block';
    if (workspaceActions) workspaceActions.style.display = 'none';
    if (explorerActions) explorerActions.style.display = 'flex';
    if (explorerActionsBottom) explorerActionsBottom.style.display = 'flex';
    loadExplorerList('');
  });
}

if (btnEditExplorerItem) {
  btnEditExplorerItem.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    if (!explorerSelection) return alert('Select a file to edit');
    const el = document.querySelector('.explorer-item[data-path="' + CSS.escape(explorerSelection) + '"]');
    const isDir = el && el.getAttribute('data-type') === 'dir';
    if (isDir) {
      alert('Cannot edit a directory. Please select a file.');
    } else {
      openWorkspaceFile(explorerSelection);
    }
  });
}

if (explorerBackBtn) {
  explorerBackBtn.addEventListener('click', () => {
    if (!explorerPath) return;
    const parts = explorerPath.split('/').filter(Boolean);
    parts.pop();
    const parentPath = parts.join('/');
    navigateExplorer(parentPath);
  });
}

async function loadExplorerList(path) {
  if (!explorerListEl) return;
  if (path !== explorerPath || !explorerListEl.innerHTML.trim()) {
    explorerListEl.innerHTML = '<div style="color:var(--text-muted); padding:10px;">Loading...</div>';
  }
  try {
    const res = await fetch('/api/workspace/list?path=' + encodeURIComponent(path));
    if (!res.ok) throw new Error('Failed to load');
    const data = await res.json();
    if (!data || data.length === 0) {
      explorerListEl.innerHTML = '<div style="color:var(--text-muted); padding:10px;">Folder is empty</div>';
    } else {
      explorerListEl.innerHTML = renderExplorerList(data);
    }
    explorerPath = path;
    updateExplorerBreadcrumb();
    explorerSelection = '';
  } catch (err) {
    explorerListEl.innerHTML = '<div style="color:var(--danger); padding:10px;">Error loading files</div>';
  }
}

function renderExplorerList(items) {
  const folderSvg = '<svg class="item-icon" viewBox="0 0 24 24" fill="none" stroke="#facc15" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg>';
  const fileSvg = '<svg class="item-icon" viewBox="0 0 24 24" fill="none" stroke="#94a3b8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"></path><polyline points="13 2 13 9 20 9"></polyline></svg>';

  let html = '';
  for (const item of items) {
    const icon = item.type === 'dir' ? folderSvg : fileSvg;
    html += `<div class="explorer-item" data-path="${esc(item.path)}" data-type="${esc(item.type)}">
      ${icon}
      <span class="item-name">${esc(item.name)}</span>
    </div>`;
  }
  return html;
}

function updateExplorerBreadcrumb() {
  if (!explorerBreadcrumbEl) return;
  if (!explorerPath) {
    explorerBreadcrumbEl.innerHTML = '<span class="crumb" data-path="">Workspace</span>';
  } else {
    const parts = explorerPath.split('/').filter(Boolean);
    let html = '<span class="crumb" data-path="">Workspace</span>';
    let buildPath = '';
    for (const part of parts) {
      buildPath += (buildPath ? '/' : '') + part;
      html += '<span class="crumb-sep">/</span><span class="crumb" data-path="' + esc(buildPath) + '">' + esc(part) + '</span>';
    }
    explorerBreadcrumbEl.innerHTML = html;
  }
}

function navigateExplorer(path) {
  loadExplorerList(path);
}

function selectExplorerItem(path) {
  explorerSelection = path;
  document.querySelectorAll('.explorer-item').forEach(el => {
    if (el.getAttribute('data-path') === path) {
      el.classList.add('selected');
    } else {
      el.classList.remove('selected');
    }
  });
}

if (explorerListEl) {
  explorerListEl.addEventListener('click', (e) => {
    const item = e.target.closest('.explorer-item');
    if (!item) return;
    const path = item.getAttribute('data-path');
    selectExplorerItem(path);
  });

  explorerListEl.addEventListener('dblclick', (e) => {
    const item = e.target.closest('.explorer-item');
    if (!item) return;
    const path = item.getAttribute('data-path');
    const type = item.getAttribute('data-type');
    if (type === 'dir') {
      navigateExplorer(path);
    } else {
      openWorkspaceFile(path);
    }
  });
}

if (explorerBreadcrumbEl) {
  explorerBreadcrumbEl.addEventListener('click', (e) => {
    const crumb = e.target.closest('.crumb');
    if (!crumb) return;
    const path = crumb.getAttribute('data-path');
    navigateExplorer(path);
  });
}

window.openWorkspaceFile = async function (path) {
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

window.deleteExplorerItem = async function (path, isDir) {
  const msg = isDir
    ? `Are you sure you want to delete the folder "${path}" and all its contents?`
    : `Are you sure you want to delete the file "${path}"?`;
  showConfirm('Delete item', msg, async () => {
    try {
      const res = await fetch('/api/workspace/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path })
      });
      const data = await res.json();
      if (data.error) throw new Error(data.error);
      if (explorerSelection.startsWith(path)) explorerSelection = '';
      loadExplorerList(explorerPath);
    } catch (err) {
      alert("Error: " + err.message);
    }
  });
};

window.showPromptModal = function (title, text, defaultValue, callback) {
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
    const prefix = explorerPath ? explorerPath + '/' : '';
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
        // After creating, navigate to the parent folder if needed
        const parent = name.includes('/') ? name.substring(0, name.lastIndexOf('/')) : '';
        if (parent !== explorerPath) navigateExplorer(parent);
        else loadExplorerList(explorerPath);
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
    const prefix = explorerPath ? explorerPath + '/' : '';
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
        loadExplorerList(explorerPath);
      } catch (err) {
        alert("Error: " + err.message);
      }
    });
  });
}

// Copy / Paste / Delete buttons
const btnExplorerCopy = document.getElementById('explorerCopy');
const btnExplorerPaste = document.getElementById('explorerPaste');
const btnExplorerDelete = document.getElementById('explorerDelete');

if (btnExplorerCopy) {
  btnExplorerCopy.addEventListener('click', () => {
    if (!explorerSelection) return alert('Select a file or folder to copy');
    clipboard = { action: 'copy', srcPath: explorerSelection };
    showNotification('Copied to clipboard', 'success');
  });
}

if (btnExplorerPaste) {
  btnExplorerPaste.addEventListener('click', async () => {
    if (!clipboard.action || !clipboard.srcPath) return alert('Nothing to paste');
    if (!explorerPath && explorerSelection) {
      // If a folder is selected in root, paste into it
      const selectedEl = document.querySelector('.explorer-item.selected');
      if (selectedEl && selectedEl.getAttribute('data-type') === 'dir') {
        // OK: destination is the selected dir
      }
    }
    const srcName = clipboard.srcPath.split('/').pop();
    const destPath = explorerPath ? explorerPath + '/' + srcName : srcName;
    try {
      const res = await fetch('/api/workspace/copy', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ src: clipboard.srcPath, dest: destPath })
      });
      const data = await res.json();
      if (data.error) throw new Error(data.error);
      loadExplorerList(explorerPath);
    } catch (err) {
      alert('Paste failed: ' + err.message);
    }
  });
}

if (btnExplorerDelete) {
  btnExplorerDelete.addEventListener('click', () => {
    if (!explorerSelection) return alert('Select a file or folder to delete');
    const el = document.querySelector('.explorer-item[data-path="' + CSS.escape(explorerSelection) + '"]');
    const isDir = el && el.getAttribute('data-type') === 'dir';
    window.deleteExplorerItem(explorerSelection, isDir);
  });
}




// Semantic Embedding Settings
async function openEmbeddingSettings() {
  $('settingsModal').classList.remove('open');
  try {
    const res = await fetch('/api/config/embedding');
    const data = await res.json();
    if (data.embedding) {
      $('cfgEmbeddingEnabled').checked = !!data.embedding.enabled;
      $('cfgEmbeddingBaseURL').value = data.embedding.base_url || '';
      $('cfgEmbeddingModel').value = data.embedding.model || '';
      $('cfgEmbeddingAPIKey').value = data.embedding.api_key || '';
    } else {
      $('cfgEmbeddingEnabled').checked = false;
      $('cfgEmbeddingBaseURL').value = '';
      $('cfgEmbeddingModel').value = '';
      $('cfgEmbeddingAPIKey').value = '';
    }
  } catch (e) {
    console.error(e);
  }
  $('embeddingModal').classList.add('open');
}

$('openEmbeddingBtn').onclick = openEmbeddingSettings;
$('embeddingClose').onclick = () => $('embeddingModal').classList.remove('open');
$('embeddingSave').onclick = async () => {
  const payload = {
    enabled: $('cfgEmbeddingEnabled').checked,
    base_url: $('cfgEmbeddingBaseURL').value,
    model: $('cfgEmbeddingModel').value,
    api_key: $('cfgEmbeddingAPIKey').value
  };
  try {
    await fetch('/api/config/embedding', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    $('embeddingModal').classList.remove('open');
    showNotification("Embedding settings saved", "success");
  } catch (e) {
    showNotification("Error saving embedding settings", "error");
  }
};

window.showNotification = function (msg, type = 'info') {
  const notif = document.createElement('div');
  notif.textContent = msg;
  notif.style.position = 'fixed';
  notif.style.bottom = '20px';
  notif.style.right = '20px';
  notif.style.padding = '12px 24px';
  notif.style.background = type === 'error' ? '#ef4444' : (type === 'success' ? '#10b981' : '#3b82f6');
  notif.style.color = '#fff';
  notif.style.borderRadius = '6px';
  notif.style.boxShadow = '0 4px 6px rgba(0,0,0,0.1)';
  notif.style.zIndex = '9999';
  notif.style.opacity = '0';
  notif.style.transition = 'opacity 0.3s ease';
  notif.style.fontFamily = 'system-ui, -apple-system, sans-serif';
  notif.style.fontWeight = '500';

  document.body.appendChild(notif);

  requestAnimationFrame(() => {
    notif.style.opacity = '1';
  });

  setTimeout(() => {
    notif.style.opacity = '0';
    setTimeout(() => notif.remove(), 300);
  }, 3000);
};
