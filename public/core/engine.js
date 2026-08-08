import { state, mergedFeatures } from './state.js?v=1785573007908';
import { readJsonOrText } from './api.js?v=1785573007908';
import { scrollChatToBottom, $ } from '../utils/dom.js?v=1785573007908';
import { fmtTokens, fmtCost } from '../utils/formatters.js?v=1785573007908';
import { conversationState } from '../ui/chat/ConversationState.js?v=1785573007908';
import { addLiveEvent } from '../ui/chat/LiveEventRenderer.js?v=1785573007908';
import { addSystemMsg, renderChat } from '../ui/chat/ChatRenderer.js?v=1785573007908';
import { textOfEditor, setEditorText } from '../ui/editor/InputBox.js?v=1785573007908';
import { renderProjects } from '../ui/sidebar/ProjectList.js?v=1785573007908';
import { updateModelTag } from '../ui/topbar/Header.js?v=1785573007908';
import { loadExplorerList, explorerPath } from '../ui/sidebar/FileTree.js?v=1785573007908';
import { renderArtifacts } from '../ui/sidebar/ArtifactsPanel.js?v=1785573007908';
let isSending = false;
import { isDiffArtifact } from '../ui/chat/MessageRenderer.js?v=1785573007908';

export function _playNotificationSound() {
  try {
    const a = new Audio('/public/notification.mp3');
    a.volume = 0.5;
    a.play().catch(() => { });
  } catch (e) { }
}

export async function addMsg(role, txt, opts) {
  if (role.toLowerCase() === 'system') addSystemMsg(txt);
  else if (role.toLowerCase() === 'user') {
    if (conversationState.turns.length > 0) {
      conversationState.turns[conversationState.turns.length - 1].completed = true;
    }
    conversationState.turns.push({ user: txt, agent: null, liveContainer: [], completed: false, durationMs: 0 });
    await renderChat();
  }
  else if (role.toLowerCase() === 'agent') {
    const turn = conversationState._currentTurn();
    turn.agent = txt;
    await renderChat();
  }
}

export async function updateInlineLive(text, state, opts) {
  await addLiveEvent({ type: 'think', text: text });
}

export async function compactLiveTranscript(durationMs) {
  const turn = conversationState._currentTurn();
  turn.durationMs = durationMs;
  turn.completed = true;
  await renderChat();
}

export function updateTimer() {
  conversationState.updateTimer();
}

export async function renderSession(displayLog, clearDom = true) {
  await conversationState.loadDb(displayLog, clearDom);
}

export function updateSendStopButtons() {
  const editorHasText = textOfEditor().trim() !== '';
  if (state.running) {
    $('send').style.display = editorHasText ? 'flex' : 'none';
    $('stopBtn').style.display = editorHasText ? 'none' : 'flex';
    $('send').disabled = !state.projectSelected || state.stopping;
    $('stopBtn').disabled = state.stopping;
  } else {
    $('send').style.display = 'flex';
    $('stopBtn').style.display = 'none';
    $('send').disabled = !state.projectSelected;
  }
}

export async function setRunningState(isRunning, isStopping = false, backendStartTime = 0) {
  if (isRunning && !state.running) { window._newDiffArtifacts = []; state._notifQueued = false; }
  state.running = isRunning;
  state.stopping = isStopping;
  document.body.classList.toggle('running', isRunning);
  $('editor').disabled = !state.projectSelected;
  $('attachBtn').disabled = isRunning || !state.projectSelected;
  updateSendStopButtons();
  if (isStopping) await updateInlineLive('Stopping', 'stopping');
  if (isRunning && !isStopping) {
    if (backendStartTime > 0) {
      window.runStartTime = backendStartTime;
    } else if (!state.runStartTime) {
      window.runStartTime = Date.now();
    }
    if (!state.runTimerInterval) {
      state.runTimerInterval = setInterval(updateTimer, 1000);
      updateTimer();
    }
  } else if (!isRunning) {
    clearInterval(state.runTimerInterval);
    state.runTimerInterval = null;
    window.runStartTime = 0;
  }
}

export function renderBackendDiagnostics(diag) {
  diag = diag || {};
  if ($('diagBackend')) $('diagBackend').textContent = diag.selected_backend || '-';
  if ($('diagReason')) $('diagReason').textContent = diag.reason || 'No backend diagnostic reason available.';
}

export async function openFile(path) {
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

export async function refresh() {
  if (state._inFlightRefresh) return;
  state._inFlightRefresh = true;
  const wasFirstStatus = state.firstStatus;
  try {
    if (state.statusAbort) state.statusAbort.abort();
    state.statusAbort = new AbortController();
    const endpoint = state.firstStatus ? '/api/status?full=true' : '/api/status';
    const r = await fetch(endpoint, {
      signal: state.statusAbort.signal
    });
    state.statusAbort = null;
    const a = await r.json();
    window.SERVER_FEATURES = a.features || {};
    state.projectSelected = !!a.workspace;
    window.currentConversationId = a.active_conversation_id;
    $('workTitle').textContent = state.projectSelected ? ((a.project && a.project.workspace) ? a.project.workspace.split(/[\\/]/).pop() : 'Workspace') : 'Select a project';
    $('workSub').textContent = state.projectSelected ? (a.workspace || '') : 'No workspace active.';
    updateTokenDisplay();
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
      state.intentMode = effectiveMode;
      $('modeDropdownToggle').textContent = effectiveMode.charAt(0).toUpperCase() + effectiveMode.slice(1);
      $('modeDropdownMenu').querySelectorAll('.mode-dropdown-option').forEach(o => {
        o.classList.toggle('active', o.dataset.value === effectiveMode);
      });
    }

    // Sync token totals from server
    state.totalTokens = {
      prompt: a.total_prompt_tokens || 0,
      completion: a.total_completion_tokens || 0
    };
    if (a.input_token_price != null) state.inputPricePerM = a.input_token_price;
    if (a.output_token_price != null) state.outputPricePerM = a.output_token_price;
    updateModelTag(a.model, a.workspace);
    updateTokenDisplay();

    if (a.projects !== undefined) await renderProjects(a.projects);
    if (window.firstStatus) {
      const isInitialLoad = document.getElementById('chat').children.length === 0 ||
        (document.getElementById('chat').children.length === 1 && document.getElementById('chat').children[0].classList.contains('system'));

      await renderSession(a.display_log, isInitialLoad);
      window.firstStatus = false;
      if (a.running) {
        try {
          const act = await fetch('/api/activity').then(res => res.json());
          const events = act.events || a.live_events || [];
          if (events.length > 0) {
            for (const evt of events) {
              if (typeof evt === 'string') {
                await handleAgentEvent({ type: 'activity', event: evt }, false);
              } else {
                await handleAgentEvent(evt, false);
              }
            }
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
          await updateInlineLive(`Created artifact: ${newArt.title}`, state.running ? 'running' : 'done', {
            log: true
          });
        }
      }
    }
    if (a.running && !state.sseSource) {
      try {
        state.sseSource = new EventSource('/api/stream/activity');
        state.sseSource.onmessage = await sseOnMessage;
        state.sseSource.onerror = await sseOnError;
      } catch (ex) {
        console.log('SSE not available, falling back to polling');
      }
      await updateInlineLive(a.activity || 'Thinking', a.stop_requested ? 'stopping' : 'running', {
        log: false
      })
    } else if (!a.running && state.running) {
      await setRunningState(false, false);
      if (!state._notifQueued) {
        state._notifQueued = true;
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
      state.statusAbort = null;
      return;
    }
    console.warn('Status refresh skipped:', e);
  } finally {
    state._inFlightRefresh = false;
  }
}

export function triggerIndex() {
  fetch('/api/workspace/index', { method: 'POST' })
    .then(r => r.json())
    .then(d => { if (!d.ok) console.warn('cbm index:', d.detail || d.error); })
    .catch(() => { });
}

export async function uploadFiles(files) {
  if (!state.projectSelected) {
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

export async function handleAgentEvent(d, isHistory = false) {
  if (d.conversation_id && window.currentConversationId && d.conversation_id !== window.currentConversationId) {
    return; // Filter out live events belonging to a different background session
  }
  if (isHistory && (d.type === 'think' || d.type === 'action' || d.type === 'token' || d.type === 'response')) {
    return;
  }

  if (d.type === 'token_usage') {
    state.totalTokens.prompt = d.total_prompt || 0;
    state.totalTokens.completion = d.total_completion || 0;
    updateTokenDisplay();
    return;
  }
  if (d.type === 'primary_changed') {
    if (d.new_model) updateModelTag(d.new_model);
    if (!isHistory && !state.running) await refresh();
    return;
  }

  if (d.type === 'error') {
    const errMsg = d.error || d.message || 'An error occurred';
    if (!isHistory) await addSystemMsg('⚠️ ' + errMsg);
    if (state.sseSource) { state.sseSource.close(); state.sseSource = null; }
    clearInterval(state.poll); state.poll = null;
    await setRunningState(false, false);
    if (!isHistory) {
      const t = conversationState._currentTurn();
      t.completed = true;
      await renderChat();
    }
    window.firstStatus = true;
    if (!isHistory) await refresh();
    return;
  }

  if (d.type === 'complete') {
    if (!isHistory) {
      // Validate that the backend is ACTUALLY done to prevent premature complete events
      // trickling in from subagents or stray events. Wait a moment for the backend defer
      // block to set engineRunning = false if this is a genuine complete.
      await new Promise(r => setTimeout(r, 600));
      try {
        const checkStatus = await fetch('/api/status').then(r => r.json());
        if (checkStatus.running) {
          // Ignoring premature complete event because engine is still running
          return;
        }
      } catch (e) {
        console.error('Failed to verify status on complete', e);
      }
    }

    state._notifQueued = true;
    if (!isHistory) _playNotificationSound();

    if (d.response && d.reason !== 'cancelled') {
      await addLiveEvent({ type: 'replace_content', content: d.response });
    }
    await addLiveEvent({
      type: 'done',
      duration_ms: d.duration_ms,
      workspace_changes: d.workspace_changes
    });

    if (state.sseSource) {
      state.sseSource.close();
      state.sseSource = null;
    }
    clearInterval(state.poll);
    state.poll = null;
    await setRunningState(false, false);
    window.firstStatus = true;
    if (!isHistory) {
      // Wait 500ms for the backend to commit final db records (snapshot ID, run_meta)
      await new Promise(r => setTimeout(r, 500));
      await refresh();
      await renderChat();
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
  await addLiveEvent(d);
}

export async function sseOnMessage(e) {
  try {
    const d = JSON.parse(e.data);
    await handleAgentEvent(d, false);
  } catch (ex) { }
}

export async function send() {
  if (isSending) return;
  const text = textOfEditor().trim();
  if (!state.projectSelected) {
    alert('Create or select a project folder first.');
    return
  }
  if (!text) return;

  if (state.running) {
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

  setEditorText('');
  setRunningState(true, false);

  requestAnimationFrame(async () => {
    addMsg('User', text);
    updateInlineLive('Thinking...', 'running');

    try {
      state.sseSource = new EventSource('/api/stream/activity');
      state.sseSource.onmessage = sseOnMessage;
      state.sseSource.onerror = sseOnError;
    } catch (ex) {
      console.log('SSE not available, falling back to polling');
      state.poll = setInterval(refresh, 900);
    }
    let postError = false;
    try {
      const runAbort = new AbortController();
      const runTimeoutSeconds = Math.max(60, Number(state.uiRunTimeoutSeconds || 3600));
      const runTimeout = setTimeout(() => runAbort.abort(), runTimeoutSeconds * 1000);
      const r = await fetch('/api/run', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          prompt: text,
          mode: state.intentMode,
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
        if (state.sseSource) { state.sseSource.close(); state.sseSource = null }
        clearInterval(state.poll); state.poll = null;
        await setRunningState(false, false);
        await compactLiveTranscript();
      }
      // Success path: SSE 'complete' handler handles cleanup & final refresh.
      isSending = false;
    }
  });
}

export async function stopRun() {
  if (!state.running) return;
  await setRunningState(true, true);
  try {
    await fetch('/api/stop', {
      method: 'POST'
    })
  } catch (e) {
    await addMsg('Agent', 'STOP ERROR: ' + e.message)
  }
}

export async function sseOnError() {
  if (state.sseSource) {
    state.sseSource.close();
    state.sseSource = null
  }
  // High fix #5: start polling fallback immediately so live activity doesn't freeze
  if (!state.poll) state.poll = setInterval(refresh, 3000);
  // Attempt SSE reconnect after 2s (only while agent is still state.running)
  setTimeout(async () => {
    if (state.running && !state.sseSource) {
      try {
        state.sseSource = new EventSource('/api/stream/activity');
        state.sseSource.onmessage = await sseOnMessage;
        state.sseSource.onerror = await sseOnError;
        state.sseSource.onopen = async function () {
          if (state.poll) {
            clearInterval(state.poll);
            state.poll = null;
          }
          await refresh();
        };
      } catch (ex) {
        /* stay on state.poll fallback */
      }
    }
  }, 2000);
}

let _lastTokenDisplayStr = '';
export function updateTokenDisplay() {
  const p = state.totalTokens.prompt || 0;
  const c = state.totalTokens.completion || 0;
  const costIn = (p / 1_000_000) * (state.inputPricePerM || 0);
  const costOut = (c / 1_000_000) * (state.outputPricePerM || 0);
  const newStr = '▲ ' + fmtTokens(p) + ' (' + fmtCost(costIn) + ') ▼ ' + fmtTokens(c) + ' (' + fmtCost(costOut) + ')';
  if (newStr !== _lastTokenDisplayStr) {
    const el = document.getElementById('tokenDisplay');
    if (el) el.textContent = newStr;
    _lastTokenDisplayStr = newStr;
  }
}
