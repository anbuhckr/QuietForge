
import { state, mergedFeatures } from './core/state.js?v=1785573007908';
import { fetchJson, readJsonOrText } from './core/api.js?v=1785573007908';
import { $, esc, md, setupMarked, chatIsNearBottom, scrollChatToBottom } from './utils/dom.js?v=1785573007908';
import { fmtTokens, fmtCost, timeAgo, formatRunDuration } from './utils/formatters.js?v=1785573007908';
import { icons, determineIcon } from './utils/icons.js?v=1785573007908';
import { conversationState } from './ui/chat/ConversationState.js?v=1785573007908';
import { mentions, chipType, renderChips, updateBackdropHighlights, highlightMentions, currentMentionQuery, fetchSuggestions, renderSuggestions, chooseSuggestion } from './ui/editor/Mentions.js?v=1785573007908';
import { textOfEditor, setEditorText, resizeEditor } from './ui/editor/InputBox.js?v=1785573007908';
import { renderProjects, selectProject, showConfirm, removeProject, openProjectModal } from './ui/sidebar/ProjectList.js?v=1785573007908';
import { stripDiffFence, hasUnifiedDiffContent, isDiffArtifact, artifactFallbackPath, diffIconForPath, isMarkdownArtifact, extractDiffArtifactsFromText, matchingDiffArtifactsForChangedFiles, parseDiffArtifacts, cleanDiffLines, renderDiffWithLineNumbers, renderCombinedDiffReview, reviewArtifactForFile, renderPlainArtifactList, renderDiffReviewWidget } from './ui/chat/MessageRenderer.js?v=1785573007908';

// Import from new modules
import { setRunningState, addMsg, renderSession, updateSendStopButtons, refresh, triggerIndex, uploadFiles, send, stopRun } from './core/engine.js?v=1785573007908';
import { updateModelTag, checkHealth } from './ui/topbar/Header.js?v=1785573007908';
import { openSettings, addProviderUI, openMcpSettings, addMcpUI, openCompactionSettings, openEmbeddingSettings } from './ui/modals/SettingsModal.js?v=1785573007908';
import { setupTabs, viewArtifact, currentArtifactPath, currentArtifactRaw, currentArtifacts } from './ui/sidebar/ArtifactsPanel.js?v=1785573007908';
import { getCodeMirrorMode, loadExplorerList, navigateExplorer, selectExplorerItem, explorerPath, explorerSelection, clearExplorerSelection } from './ui/sidebar/FileTree.js?v=1785573007908';

window.$ = $;
window.esc = esc;
window.md = md;
window.state = state;
window.setRunningState = setRunningState;
window.mergedFeatures = mergedFeatures;
window.fetchJson = fetchJson;
window.readJsonOrText = readJsonOrText;
window.setupMarked = setupMarked;

setupMarked();

window.chatIsNearBottom = chatIsNearBottom;
window.scrollChatToBottom = scrollChatToBottom;
window.updateSendStopButtons = updateSendStopButtons;
window.send = send;
window.suggestions = [];
window.activeSuggestion = 0;
window.fmtTokens = fmtTokens;
window.fmtCost = fmtCost;
window.timeAgo = timeAgo;
window.formatRunDuration = formatRunDuration;
window.icons = icons;
window.determineIcon = determineIcon;
window.conversationState = conversationState;
window.mentions = mentions;
window.chipType = chipType;
window.renderChips = renderChips;
window.updateBackdropHighlights = updateBackdropHighlights;
window.highlightMentions = highlightMentions;
window.currentMentionQuery = currentMentionQuery;
window.fetchSuggestions = fetchSuggestions;
window.renderSuggestions = renderSuggestions;
window.chooseSuggestion = chooseSuggestion;
window.textOfEditor = textOfEditor;
window.setEditorText = setEditorText;
window.textOfEditor = textOfEditor;
window.resizeEditor = resizeEditor;
window.renderProjects = renderProjects;
window.selectProject = selectProject;
window.showConfirm = showConfirm;
window.removeProject = removeProject;
window.openProjectModal = openProjectModal;
window.stripDiffFence = stripDiffFence;
window.hasUnifiedDiffContent = hasUnifiedDiffContent;
window.isDiffArtifact = isDiffArtifact;
window.artifactFallbackPath = artifactFallbackPath;
window.diffIconForPath = diffIconForPath;
window.isMarkdownArtifact = isMarkdownArtifact;
window.matchingDiffArtifactsForChangedFiles = matchingDiffArtifactsForChangedFiles;
window.parseDiffArtifacts = parseDiffArtifacts;
window.cleanDiffLines = cleanDiffLines;
window.renderDiffWithLineNumbers = renderDiffWithLineNumbers;
window.renderCombinedDiffReview = renderCombinedDiffReview;
window.reviewArtifactForFile = reviewArtifactForFile;
window.renderPlainArtifactList = renderPlainArtifactList;
window.renderDiffReviewWidget = renderDiffReviewWidget;
window.extractDiffArtifactsFromText = extractDiffArtifactsFromText;

// Re-expose to window because the HTML event handlers or tightly coupled functions in app.js might need them.
window.viewArtifactByTitle = function (title) {
  const art = typeof currentArtifacts !== 'undefined' ? currentArtifacts.find(a => a.title === title) : null;
  if (art) viewArtifact(art);
};

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

state.features = JSON.parse(localStorage.getItem('qf_features') || '{}');
state.intentMode = localStorage.getItem('qf_intent_mode');
if (state.intentMode === 'auto' || !state.intentMode) state.intentMode = 'build';
state.totalTokens = { prompt: 0, completion: 0 };
state.inputPricePerM = 2.50;
state.outputPricePerM = 10.00;
window.runStartTime = 0;
state.runTimerInterval = null;
window.window.firstStatus = true;
state.statusAbort = null;
state._inFlightRefresh = false;
let isSending = false;

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
      proxies: el.querySelector('.cfg-proxies').value,
      disable_vision: el.querySelector('.cfg-disable-vision').checked,
      context_window: parseInt(el.querySelector('.cfg-context-window').value) || 0,
      max_messages: parseInt(el.querySelector('.cfg-max-messages').value) || 0,
      input_price: parseFloat(el.querySelector('.cfg-input-price')?.value) || 0,
      output_price: parseFloat(el.querySelector('.cfg-output-price')?.value) || 0,
      tail_turns: parseInt(el.querySelector('.cfg-tail-turns').value) || 0,
      preserve_recent_tokens: parseInt(el.querySelector('.cfg-preserve-recent-tokens').value) || 0,
      reserved: parseInt(el.querySelector('.cfg-reserved').value) || 0,
      tool_truncation_limit: parseInt(el.querySelector('.cfg-tool-truncation-limit').value) || 0
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

$('openCompactionBtn').onclick = openCompactionSettings;
$('compactionClose').onclick = () => $('compactionModal').classList.remove('open');
$('compactionSave').onclick = async () => {
  const payload = {
    auto: $('cfgCompactionAuto').checked,
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
modeToggle.textContent = state.intentMode.charAt(0).toUpperCase() + state.intentMode.slice(1);
modeMenu.querySelectorAll('.mode-dropdown-option').forEach(o => {
  o.classList.toggle('active', o.dataset.value === state.intentMode);
});

modeToggle.onclick = () => {
  if (state.running) return;
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
  if (!opt || state.running) return;
  const value = opt.dataset.value;
  state.intentMode = value;
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

checkHealth();

let cmEditor = null;
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

// Sticky prompts disabled for standard chat bubble layout

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
const btnOpenExplorerItem = document.getElementById('openExplorerItem');
const explorerListEl = document.getElementById('explorerList');
const explorerBreadcrumbEl = document.getElementById('explorerBreadcrumb');
const explorerBackBtn = document.getElementById('explorerBack');

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

if (btnOpenExplorerItem) {
  btnOpenExplorerItem.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    if (!explorerSelection) return alert('Select a file to open');
    const el = document.querySelector('.explorer-item[data-path="' + CSS.escape(explorerSelection) + '"]');
    const isDir = el && el.getAttribute('data-type') === 'dir';
    if (isDir) {
      alert('Cannot open a directory. Please select a file.');
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
      if (explorerSelection.startsWith(path)) clearExplorerSelection();
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
$('openEmbeddingBtn').onclick = openEmbeddingSettings;
$('embeddingClose').onclick = () => $('embeddingModal').classList.remove('open');
$('embeddingSave').onclick = async () => {
  const payload = {
    enabled: $('cfgEmbeddingEnabled').checked,
    disable_retrieval: $('cfgEmbeddingDisableRetrieval').checked,
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

window.viewArtifact = viewArtifact;
window.triggerIndex = triggerIndex;
window.refresh = refresh;
window.renderSession = renderSession;
