import { conversationState } from './ConversationState.js?v=1785573007908';
import { renderChat } from './ChatRenderer.js?v=1785573007908';
let _streamRenderQueued = false;

export function formatToolCall(rawStr) {
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

export function parseThinkBlocks(content, toolsJson) {
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

export async function addLiveEvent(evt) {
  const turn = conversationState.currentTurn();
  const kind = evt.type || evt.kind || 'activity';
  let rawText = String(evt.text || evt.event || evt.message || evt.error || '');
  if (kind !== 'token' && kind !== 'think') {
    rawText = rawText.trim();
  }
  if (!rawText && kind !== 'token' && kind !== 'think' && kind !== 'replace_content') return;

  if (kind === 'think') {
    if (/\[Thought process omitted/i.test(rawText)) return;
    const beforeLen = turn.liveContainer.length;
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

  } else if (kind === 'action') {
    if (rawText.startsWith('Executing: ')) {
      let tool = rawText.replace(/^Executing:\s*/, '');
      tool = formatToolCall(tool);
      if (turn.liveContainer.length === 0) {
        turn.liveContainer.push({ think: null, tools: [] });
      }
      turn.liveContainer[turn.liveContainer.length - 1].tools.push(tool);
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
    const parsed = parseThinkBlocks(evt.content, "");
    const thinkEntries = parsed.filter(e => e.think).map(e => e.think);
    if (thinkEntries.length > 0 && turn.liveContainer.length > 0) {
      turn.liveContainer[turn.liveContainer.length - 1].think = thinkEntries[thinkEntries.length - 1];
    }
  } else if (kind === 'token') {
    turn.agent = (turn.agent || '') + rawText;
  } else if (kind === 'done' || kind === 'complete') {
    turn.completed = true;
    turn._needsFullRender = true;
    if (evt.duration_ms) turn.durationMs = evt.duration_ms;
    if (evt.workspace_changes) turn.workspaceChanges = evt.workspace_changes;
  }

  if (kind === 'token' || kind === 'think') {
    if (!_streamRenderQueued) {
      _streamRenderQueued = true;
      requestAnimationFrame(() => {
        _streamRenderQueued = false;
        renderChat();
      });
    }
    return;
  }

  requestAnimationFrame(() => {
    renderChat();
  });
}