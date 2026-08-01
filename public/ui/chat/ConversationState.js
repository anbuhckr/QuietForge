import { state } from '../../core/state.js?v=1785573007908';
import { renderChat } from './ChatRenderer.js?v=1785573007908';
import { parseThinkBlocks, formatToolCall } from './LiveEventRenderer.js?v=1785573007908';

class ConversationState {
  constructor() {
    this.turns = [];
    this._loadPromise = null;
    this._renderPromise = null;
  }

  currentTurn() {
    if (this.turns.length === 0) {
      this.turns.push({ user: null, agent: null, liveContainer: [], completed: false, durationMs: 0 });
    }
    return this.turns[this.turns.length - 1];
  }

  async clear() {
    this.turns = [];
    await renderChat();
  }

  async loadDb(displayLog, clearDom = true, isRevert = false) {
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
            let isSame = existingTurn && existingTurn.messageId === msg.id && !isRevert;
            this.turns.push({
              user: parts[0]?.content || '',
              agent: isSame ? existingTurn.agent : null,
              liveContainer: isSame ? existingTurn.liveContainer : [],
              completed: false,
              durationMs: msg.run_meta ? (msg.run_meta.duration_ms || 0) : 0,
              messageId: msg.id,
              userMessageId: msg.id,
              snapshot: msg.snapshot,
              hidden: msg.metadata ? msg.metadata.hidden : false,
              _pendingTools: []
            });
          }
        } else if (role === 'assistant') {
          const turn = this.currentTurn();
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
          } else if (oldTurn && oldTurn.messageId) {
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
                const parsed = parseThinkBlocks(p.content, "");
                for (const e of parsed) {
                  if (e.think) {
                    turn.liveContainer.push({ think: e.think, tools: [] });
                  } else if (e.content) {
                    turn.agent = (turn.agent || '') + e.content;
                  }
                }
              } else if (p.type === 'tool_use') {
                let toolStr = `${p.tool_name} ${p.arguments}`;
                try { toolStr = formatToolCall(toolStr); } catch (e) { }

                if (turn.liveContainer.length === 0) {
                  turn.liveContainer.push({ think: null, tools: [] });
                }
                turn.liveContainer[turn.liveContainer.length - 1].tools.push(toolStr);
              }
            });
          }

          if (msg.run_meta && msg.run_meta.duration_ms) {
            turn.completed = true;
            turn.durationMs = msg.run_meta.duration_ms;
          } else if (oldTurn.durationMs) {
            turn.completed = oldTurn.completed;
            turn.durationMs = oldTurn.durationMs;
          }
          if (msg.run_meta && msg.run_meta.workspace_changes) {
            turn.workspaceChanges = msg.run_meta.workspace_changes;
            turn.artifacts = msg.run_meta.artifacts;
          } else if (oldTurn.workspaceChanges) {
            turn.workspaceChanges = oldTurn.workspaceChanges;
            turn.artifacts = oldTurn.artifacts;
          }
        }
      });

      // Suppress IntersectionObserver while setting initial scroll position
      this._suppressLazyObserver = true;
      await renderChat();
      scrollChatToBottom(true);
      this._suppressLazyObserver = false;

      // Register lazy observer for off-screen turns after viewport is at bottom
      const chat = document.getElementById('chat');
      if (chat && this._lazyObserver) {
        chat.querySelectorAll('.chat-turn').forEach((el, idx) => {
          if (idx < Math.max(0, this.turns.length - 3) && el.dataset.renderedFinal !== "true") {
            this._lazyObserver.observe(el);
          }
        });
      }
    } finally {
      const release = resolveLock;
      this._loadPromise = null;
      if (release) release();
    }
  }

  formatRunDuration(ms) {
    const seconds = Math.max(1, Math.round(ms / 1000));
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    const rest = seconds % 60;
    return rest ? `${minutes}m ${rest}s` : `${minutes}m`;
  }

  updateTimer() {
    if (!state.running || !window.runStartTime) return;
    const elapsedMs = Date.now() - window.runStartTime;
    if (elapsedMs < 0) return;
    const s = Math.floor(elapsedMs / 1000);
    const activeTimer = document.querySelector('.chat-turn:last-child .inline-live .timer');
    if (activeTimer) {
      activeTimer.textContent = `[${Math.floor(s / 60).toString().padStart(2, '0')}:${(s % 60).toString().padStart(2, '0')}]`;
    }
  }
}

export const conversationState = new ConversationState();
