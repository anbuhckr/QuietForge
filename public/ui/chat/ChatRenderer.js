import { esc, md, mdAsync, scrollChatToBottom, chatIsNearBottom } from '../../utils/dom.js?v=1785573007908';
import { conversationState } from './ConversationState.js?v=1785573007908';
import { formatRunDuration } from '../../utils/formatters.js?v=1785573007908';
import { updateTokenDisplay } from '../../core/engine.js?v=1785573007908';
import { renderDiffReviewWidget } from './MessageRenderer.js?v=1785573007908';

let _isRendering = false;
let _needsReRender = false;
let _lazyObserver = null;
let _suppressLazyObserver = false;

export function ensureLazyObserver() {
  if (_lazyObserver) return;
  _lazyObserver = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      const el = entry.target;
      const idx = parseInt(el.dataset.turnIdx, 10);
      if (isNaN(idx) || idx >= conversationState.turns.length) continue;
      const turn = conversationState.turns[idx];
      
      if (entry.isIntersecting) {
        if (el.dataset.renderedFinal !== "true") {
          el.style.minHeight = '';
          renderTurn(el, turn).then(() => {
            if (turn.completed) el.dataset.renderedFinal = "true";
          });
        }
      } else {
        const immediateStart = Math.max(0, conversationState.turns.length - 3);
        if (idx < immediateStart && turn.completed && el.dataset.renderedFinal === "true") {
          const h = entry.boundingClientRect.height;
          if (h > 0) el.style.minHeight = h + 'px';
          el.innerHTML = '';
          const duration = formatRunDuration(turn.durationMs || 0);
          el.innerHTML = `<div class="msg agent" style="height: 100%; display: flex; align-items: center; justify-content: center;"><div class="bubble markdown-body" style="opacity:0.5;font-style:italic;">Worked for ${esc(duration)} — scroll to load</div></div>`;
          el.dataset.renderedFinal = "false";
        }
      }
    }
  }, { root: document.getElementById('chat'), rootMargin: '400px 0px' });
}

export async function renderTurn(turnGroup, turn) {
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

        let cleanedUser = (turn.user || '').trim();

        d.innerHTML = `<div class="label">User</div><div class="bubble markdown-body" style="white-space: pre-wrap;">${esc(cleanedUser)}</div>`;
        turnGroup.appendChild(d);
        window.lastUserMsg = d; // update global pointer

        mdAsync(cleanedUser).then(html => {
          let content = html;
          content = content.replace(/(^|\s)(\/[a-zA-Z0-9_-]+)/g, '$1<span class="hl-slash">$2</span>');
          content = content.replace(/(^|\s)(@[a-zA-Z0-9_.-]+\/?)/g, (match, space, tag) => {
            return space + `<span class="${tag.endsWith('/') ? 'hl-folder' : 'hl-mention'}">${tag}</span>`;
          });
          const bubble = d.querySelector('.bubble');
          if (bubble) {
            bubble.style.whiteSpace = '';
            bubble.innerHTML = content;
          }
        });
      }

      if (turn.snapshot && turn.messageId && !d.querySelector('.revert-btn')) {
        const btn = document.createElement('button');
        btn.className = 'revert-btn';
        btn.title = 'Revert workspace to this point';
        btn.textContent = '\u21B6';
        btn.dataset.messageId = turn.userMessageId || turn.messageId;
        btn.onclick = () => {
          showConfirm('Revert Chat', 'Are you sure you want to revert to before this message? This message and all subsequent messages will be lost.', async () => {
            try {
              const targetId = turn.userMessageId || turn.messageId;
              const r = await fetch('/api/chat/revert', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ message_id: targetId, conversation_id: window.currentConversationId })
              });
              if (r.ok) {
                const data = await r.json();
                conversationState.loadDb(data.display_log, true, true);
                await refresh();
              }
            } catch (e) {
              console.error('Revert error:', e);
              alert('Revert error: ' + e.message);
            }
          });
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
        const durationTxt = formatRunDuration(turn.durationMs || 0);
        details.innerHTML = `<summary><span class="compact-label">Worked for ${esc(durationTxt)}</span><span class="compact-arrow">›</span></summary>` + sharedLogHtml;
      } else {
        details.classList.add('flat-live');
        details.open = true;
        details.innerHTML = `<summary style="cursor:pointer; display:flex; align-items:center;"><span style="margin-right:8px; font-size:10px; opacity:0.7;">▼</span><span class="livetext" style="font-weight:600;">Running background tasks...</span><span class="timer" style="margin-left:8px; opacity:0.6; font-family:var(--mono); font-variant-numeric: tabular-nums; display:inline-block; min-width:65px;"></span></summary>` + sharedLogHtml;
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
        const durationTxt = formatRunDuration(turn.durationMs || 0);
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

    if (!log.dataset.scrollAttached) {
      log.dataset.scrollAttached = "true";
      log.addEventListener('scroll', () => {
        if (log._scrollRaf) return;
        log._scrollRaf = requestAnimationFrame(() => {
          log.dataset.userScrolledUp = (log.scrollHeight - log.scrollTop - log.clientHeight) >= 10 ? "true" : "false";
          log._scrollRaf = null;
        });
      }, { passive: true });
    }

    const renderedCount = parseInt(log.dataset.count || "0", 10);
    const isScrolledToBottom = log.dataset.userScrolledUp !== "true";
    let needsScroll = false;

    if (turn.liveContainer.length > renderedCount) {
      for (let i = renderedCount; i < turn.liveContainer.length; i++) {
        const block = turn.liveContainer[i];
        if (block.think) {
          const entry = document.createElement('div');
          entry.className = 'live-entry markdown-body';
          entry.dataset.thinkIdx = i;
          entry.dataset.len = block.think.length;
          if (turn.completed) {
            entry.style.whiteSpace = 'pre-wrap';
            entry.textContent = block.think;
            mdAsync(block.think).then(html => {
               if (!html.trim()) html = esc(block.think);
               entry.style.whiteSpace = '';
               entry.innerHTML = html;
            });
          } else {
            entry.style.whiteSpace = 'pre-wrap';
            entry.textContent = block.think;
          }
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
      if (isScrolledToBottom || renderedCount === 0) {
        needsScroll = true;
      }
    }
    if (turn.liveContainer.length > 0 && renderedCount > 0) {
      const lastIdx = turn.liveContainer.length - 1;
      const block = turn.liveContainer[lastIdx];
      if (block) {
        if (block.think) {
          const thinkNode = log.querySelector(`[data-think-idx="${lastIdx}"]`);
          if (thinkNode) {
            const currentLen = parseInt(thinkNode.dataset.len || "0", 10);
            if (block.think.length > currentLen) {
              if (turn.completed) {
                thinkNode.style.whiteSpace = 'pre-wrap';
                thinkNode.textContent = block.think;
                mdAsync(block.think).then(html => {
                   if (!html.trim()) html = esc(block.think);
                   thinkNode.style.whiteSpace = '';
                   thinkNode.innerHTML = html;
                });
              } else {
                thinkNode.style.whiteSpace = 'pre-wrap';
                thinkNode.textContent = block.think;
              }
              thinkNode.dataset.len = block.think.length;
              if (isScrolledToBottom) {
                needsScroll = true;
              }
            }
          }
        }
        if (block.tools) {
          const toolKey = 't_' + lastIdx;
          const renderedTools = parseInt(log.dataset[toolKey] || "0", 10);
          if (block.tools.length > renderedTools) {
            for (let j = renderedTools; j < block.tools.length; j++) {
              const entry = document.createElement('div');
              entry.className = 'live-entry markdown-body action-entry';
              entry.style.animation = 'none';
              if (block.tools[j] === 'compacting...') {
                entry.innerHTML = `<p>⚙️ compacting...</p>`;
              } else {
                entry.innerHTML = `<p>⚙️ ${esc(block.tools[j])}</p>`;
              }
              log.appendChild(entry);
            }
            log.dataset[toolKey] = block.tools.length;
            if (isScrolledToBottom) {
              needsScroll = true;
            }
          }
        }
      }
    }
    if (needsScroll) {
      requestAnimationFrame(() => {
        log.scrollTop = 9999999;
      });
    }
  } else {
    let wrapper = turnGroup.querySelector('.live-container');
    if (wrapper) wrapper.remove();
  }

  // 3. Agent Message
  let content = turn.agent || "";

  if (content.includes('<think') || content.includes('<thought')) {
    content = content.replace(/<(?:think|thought)>([\s\S]*?)(<\/(?:think|thought)>|$)/gi, '').trim();
  }

  // Strip XML tool calls to prevent raw JSON arguments from ruining the UI
  if (content.includes('<invoke') || content.includes('<tool_call>')) {
    content = content.replace(/<invoke\s+name=["'][^"']+["']\s*>[\s\S]*?<\/invoke>/gi, '');
    content = content.replace(/<tool_call>[\s\S]*?<\/tool_call>/gi, '');
    content = content.trim();
  }

  // Strip diff blocks because they are rendered separately in the workspace changes widget
  if (content.includes('```diff')) {
    content = content.replace(/```diff\n[\s\S]*?```/gi, '').trim();
  }

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

  const hasTools = turn.liveContainer.some(b => b.tools && b.tools.length > 0);
  const hasAgentContent = !!content || !!turn.workspaceChanges || hasTools;

  let d = turnGroup.querySelector('.msg.agent');

  // Activate this line to debug the rendering process
  // console.log('Rerender turn:', turn.messageId, turn, 'hasAgentContent:', hasAgentContent);

  if (hasAgentContent) {
    if (!d) {
      d = document.createElement('div');
      d.className = 'msg agent';
      d.innerHTML = `<div class="label">Agent</div><div class="bubble markdown-body"></div>`;
      turnGroup.appendChild(d);
    }
    d.style.display = '';

    // Fast text update during live streaming; full Markdown parse ONCE on turn completion
    const contentKey = (turn.completed ? 'comp:' : 'stream:') + content.length + ':' + (content.length > 0 ? content.charCodeAt(0) + content.charCodeAt(content.length - 1) + content.charCodeAt(Math.floor(content.length / 2)) : 0);

    const bubble = d.querySelector('.bubble');
    if (bubble && bubble.dataset.contentKey !== contentKey) {
      if (turn.completed) {
        bubble.dataset.contentKey = contentKey;
        // Inject raw text immediately to secure layout bounds and improve LCP
        bubble.style.whiteSpace = 'pre-wrap';
        bubble.textContent = content;
        
        // Upgrade to parsed HTML asynchronously to avoid blocking main thread
        mdAsync(content).then(html => {
          requestAnimationFrame(() => {
            bubble.style.whiteSpace = '';
            bubble.innerHTML = html;
          });
        });
      } else {
        bubble.style.whiteSpace = 'pre-wrap';
        bubble.textContent = content;
        bubble.dataset.contentKey = contentKey;
      }
    }

    // Render any UI widgets for changed files
    if (turn.workspaceChanges) {
      let changedFiles = [];
      if (turn.workspaceChanges.created) changedFiles.push(...turn.workspaceChanges.created);
      if (turn.workspaceChanges.modified) changedFiles.push(...turn.workspaceChanges.modified);
      if (changedFiles.length > 0) {
        if (!d.dataset.renderedDiffs) {
          renderDiffReviewWidget(d, turn.workspaceChanges);
          d.dataset.renderedDiffs = "true";
        }
      }
    }
  } else {
    if (d) d.style.display = 'none';
  }
  updateTokenDisplay();
}

export async function doRender(shouldFollow) {
  const chat = document.getElementById('chat');

  if (conversationState.turns.length === 0) {
    chat.innerHTML = '<div class="msg system"><div class="label">System</div><div class="bubble">Ready. Mention workspace context with @todo.py, @recent, @diff.</div></div>';
    if (shouldFollow) scrollChatToBottom();
    return;
  }

  if (chat.children.length === 1 && chat.children[0].classList.contains('system')) {
    chat.innerHTML = '';
  }

  while (chat.children.length > conversationState.turns.length) {
    chat.removeChild(chat.lastChild);
  }

  ensureLazyObserver();

  // Only fully render the last 3 turns immediately; defer older ones
  const immediateStart = Math.max(0, conversationState.turns.length - 3);

  for (let i = 0; i < conversationState.turns.length; i++) {
    const turn = conversationState.turns[i];
    let turnGroup = chat.children[i];
    const isLast = (i === conversationState.turns.length - 1);

    if (!turnGroup) {
      turnGroup = document.createElement('div');
      turnGroup.className = 'chat-turn';
      turnGroup.dataset.messageId = turn.messageId || '';
      turnGroup.dataset.turnIdx = String(i);
      chat.appendChild(turnGroup);
    } else {
      if ((turnGroup.dataset.messageId || '') !== (turn.messageId || '')) {
        turnGroup.innerHTML = '';
        turnGroup.dataset.messageId = turn.messageId || '';
        turnGroup.dataset.turnIdx = String(i);
        delete turnGroup.dataset.renderedFinal;
        delete turnGroup.dataset.renderedDiffs;
      } else if (!isLast && turnGroup.dataset.renderedFinal === "true") {
        continue;
      }
    }

    // Deferred lazy rendering for old completed turns not yet in DOM
    if (i < immediateStart && turn.completed && turnGroup.dataset.renderedFinal !== "true") {
      if (turnGroup.children.length === 0) {
        // Create lightweight placeholder, render on scroll via IntersectionObserver
      const placeholder = document.createElement('div');
      placeholder.className = 'msg agent';
      placeholder.style.minHeight = '48px';
      if (turn.user && !turn.hidden) {
        const userDiv = document.createElement('div');
        userDiv.className = 'msg user';
        const fullUserText = (turn.user || '').trim();
        userDiv.innerHTML = `<div class="label">User</div><div class="bubble markdown-body">${esc(fullUserText)}</div>`;
        turnGroup.appendChild(userDiv);
      }
      const duration = formatRunDuration(turn.durationMs || 0);
      placeholder.innerHTML = `<div class="bubble markdown-body" style="opacity:0.5;font-style:italic;">Worked for ${esc(duration)} — scroll to load</div>`;
      turnGroup.appendChild(placeholder);
      }
      if (!_suppressLazyObserver) {
        _lazyObserver.observe(turnGroup);
      }
      continue;
    }

    await renderTurn(turnGroup, turn);

    // Mark as final if it's a completed turn
    if (turn.completed) {
      turnGroup.dataset.renderedFinal = "true";
    }
  }

  conversationState.updateTimer();
}

export async function renderChat() {
  if (_isRendering) {
    _needsReRender = true;
    return;
  }
  _isRendering = true;

  try {
    _needsReRender = false;
    await doRender(false); // Skip synchronous scrolling
    
    requestAnimationFrame(() => {
      if (chatIsNearBottom()) {
        scrollChatToBottom();
      }
    });
  } finally {
    _isRendering = false;
    if (_needsReRender) {
      _needsReRender = false;
      requestAnimationFrame(renderChat);
    }
  }
}

export async function addSystemMsg(txt) {
  conversationState.turns.push({ system: txt });
  await renderChat();
}