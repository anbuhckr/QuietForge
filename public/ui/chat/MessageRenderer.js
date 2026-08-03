import { esc } from '../../utils/dom.js?v=1785573007908';
import { viewArtifact } from './ArtifactViewer.js?v=1785573007908'; // Assuming we put viewArtifact in another module or handle it
import { showConfirm } from '../sidebar/ProjectList.js?v=1785573007908'; // Assumed location for showConfirm

export function stripDiffFence(content) {
  return String(content || '').trim()
    .replace(/^```(?:diff|patch)\s*/i, '')
    .replace(/\s*```$/i, '');
}

export function hasUnifiedDiffContent(content) {
  const text = stripDiffFence(content);
  return /^diff --git\s+a\/.+?\s+b\/.+$/m.test(text) ||
    /^---\s+(?:a\/|\/dev\/null).+$/m.test(text) && /^\+\+\+\s+(?:b\/|\/dev\/null).+$/m.test(text) && /^@@\s+-\d+(?:,\d+)?\s+\+\d+(?:,\d+)?\s+@@/m.test(text) ||
    /^\+\+\+\s+b\/.+$/m.test(text) && /^@@\s+-0,0\s+\+\d+(?:,\d+)?\s+@@/m.test(text);
}

export function isDiffArtifact(art) {
  const title = String(art && art.title || '');
  const content = String(art && art.content || '');
  return hasUnifiedDiffContent(content) || (/\.(diff|patch)$/i.test(title) && hasUnifiedDiffContent(content));
}

export function artifactFallbackPath(art) {
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

export function diffIconForPath(path) {
  const ext = (String(path).split('.').pop() || 'txt').toLowerCase();
  if (ext === 'js' || ext === 'jsx' || ext === 'ts' || ext === 'tsx') return {
    label: ext.toUpperCase(),
    cls: 'js'
  };
  if (ext === 'go') return {
    label: 'GO',
    cls: 'go'
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

export function isMarkdownArtifact(art) {
  return /\.md$/i.test(String(art && art.title || '')) && !isDiffArtifact(art);
}

export function extractDiffArtifactsFromText(text, changedFiles = []) {
  const artifacts = [];
  const diffRegex = /```diff\n([\s\S]*?)```/g;
  let match;
  let index = 0;
  while ((match = diffRegex.exec(text)) !== null) {
    const diffContent = match[1];
    const pathMatch = diffContent.match(/^---\s+[ab]\/(.+)$/m) || diffContent.match(/^\+\+\+\s+[ab]\/(.+)$/m);
    const filename = pathMatch ? pathMatch[1] : (changedFiles[index] || `unknown_${index}`);
    artifacts.push({
      title: "Diff_" + filename,
      content: match[0]
    });
    index++;
  }
  return artifacts;
}

export function matchingDiffArtifactsForChangedFiles(changedFiles) {
  const artifacts = Array.isArray(window.latestArtifacts) ? window.latestArtifacts : [];
  const diffArtifacts = artifacts.filter(isDiffArtifact);
  const relevant = [];
  (changedFiles || []).forEach(f => {
    const bn = String(f).split(/[\\/]/).pop();
    const normalizedPath = String(f).replace(/\\/g, '/');
    const art = diffArtifacts.find(a => {
      const title = String(a.title || '');
      const fallback = artifactFallbackPath(a);
      if (title === "Diff_" + bn + ".md" ||
        title === "Diff_ " + bn + ".md" ||
        title === "Diff_" + bn ||
        title === "Diff_ " + bn ||
        title === "Diff: " + bn ||
        title === "Diff: " + bn + ".md" ||
        fallback === normalizedPath ||
        fallback === bn) return true;

      // Robust fallback: Check if the diff content actually targets this file
      const content = String(a.content || '');
      if (content.includes(`diff --git a/${normalizedPath} b/${normalizedPath}`)) return true;
      if (content.includes(`+++ b/${normalizedPath}`)) return true;
      if (content.includes(`+++ ${normalizedPath}`)) return true;

      return false;
    });
    if (art && !relevant.includes(art)) relevant.push(art);
  });
  return relevant;
}

export function parseDiffArtifacts(artifacts) {
  const files = [];
  let totalAdditions = 0;
  let totalDeletions = 0;
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

export function cleanDiffLines(content) {
  return String(content || '')
    .replace(/^```(?:diff|patch)?\s*/i, '')
    .replace(/\s*```$/, '')
    .split(/\r?\n/)
    .filter(line => !line.startsWith('diff --git ') && !line.startsWith('index ') && !line.startsWith('--- ') && !line.startsWith('+++ '));
}

export function renderDiffWithLineNumbers(lines, filename = '') {
  let oldLine = 0, newLine = 0;
  let lang = 'plaintext';
  if (filename) {
    const ext = filename.split('.').pop().toLowerCase();
    const map = { js: 'javascript', ts: 'typescript', go: 'go', py: 'python', css: 'css', html: 'html', md: 'markdown', json: 'json', jsx: 'javascript', tsx: 'typescript' };
    lang = map[ext] || ext;
  }
  const hasHljs = window.hljs && window.hljs.getLanguage(lang);

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
    let highlightedCode = esc(code || ' ');
    if (hasHljs && code.trim().length > 0) {
      try {
        highlightedCode = window.hljs.highlight(code, { language: lang }).value;
      } catch (e) {}
    }
    return `<div class="review-diff-line ${cls}"><span class="diff-ln">${oldNum}</span><span class="diff-ln">${newNum}</span><span class="diff-code hljs" style="display:inline; background:transparent; padding:0;">${highlightedCode}</span></div>`;
  }).join('');
}

export function renderCombinedDiffReview(diffArtifacts, parsed) {
  const sections = parsed.files.map(file => {
    const lines = cleanDiffLines(file.artifact.content);
    return `
      <section class="review-file-block">
        <div class="review-file-header">
          <span class="review-file-path">${esc(file.path)}</span>
          <span class="review-file-counts"><span class="diff-widget-additions">+${file.additions}</span><span class="diff-widget-deletions">-${file.deletions}</span></span>
        </div>
        <div class="review-diff-code">${renderDiffWithLineNumbers(lines, file.path)}</div>
      </section>
    `;
  }).join('');
  return `<div class="review-diff-stack">${sections}</div>`;
}

export function reviewArtifactForFile(file) {
  return {
    title: file.path,
    html: renderCombinedDiffReview([file.artifact], {
      files: [file],
      totalAdditions: file.additions,
      totalDeletions: file.deletions
    })
  };
}

export function renderPlainArtifactList(container, artifacts, title = 'Documents') {
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

export function renderDiffReviewWidget(container, workspaceChanges) {
  let files = [];
  if (workspaceChanges.created) files.push(...workspaceChanges.created);
  if (workspaceChanges.modified) files.push(...workspaceChanges.modified);
  if (workspaceChanges.deleted) files.push(...workspaceChanges.deleted);
  files = [...new Set(files)]; // deduplicate
  const revertedFiles = workspaceChanges.reverted_files || [];

  let totalAdditions = 0;
  let totalDeletions = 0;
  const fileStats = workspaceChanges.stats || {};
  files.forEach(f => {
    totalAdditions += (fileStats[f]?.additions || 0);
    totalDeletions += (fileStats[f]?.deletions || 0);
  });

  const widget = document.createElement('details');
  widget.className = 'diff-widget';
  const filesText = files.length === 1 ? '1 file changed' : `${files.length} files changed`;
  widget.innerHTML = `
    <summary class="diff-widget-header" style="cursor: pointer; user-select: none;">
      <div class="diff-widget-stats" style="display: flex; align-items: center;">
        <strong>${esc(filesText)}</strong><span class="diff-widget-additions">+${totalAdditions}</span><span class="diff-widget-deletions">-${totalDeletions}</span>
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
  files.forEach(path => {
    const icon = diffIconForPath(path);
    const filename = String(path).split(/[\\/]/).pop() || path;
    const dir = String(path).slice(0, Math.max(0, String(path).length - filename.length));
    const row = document.createElement('button');
    const isReverted = revertedFiles.includes(path);
    row.className = 'diff-widget-file' + (isReverted ? ' reverted' : '');
    row.type = 'button';
    row.innerHTML = `
      <span class="diff-widget-file-icon ${esc(icon.cls)}">${esc(icon.label)}</span>
      <span class="diff-widget-file-main">
        <span class="diff-widget-filename">${esc(filename)}</span>
        ${dir ? `<span class="diff-widget-filepath">${esc(dir)}</span>` : ''}
      </span>
      <span class="diff-widget-line-stats"><span class="diff-widget-additions">+${fileStats[path]?.additions || 0}</span><span class="diff-widget-deletions">-${fileStats[path]?.deletions || 0}</span></span>
      ${isReverted ? '' : `
      <button type="button" class="diff-widget-revert-btn" title="Revert file to previous state">
        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"><polyline points="1 4 1 10 7 10"></polyline><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"></path></svg>
      </button>
      `}
    `;
    row.onclick = async (e) => {
      const msgId = container.closest('.chat-turn')?.dataset?.messageId || (() => {
        const msgs = document.querySelectorAll('.chat-turn[data-message-id]');
        return msgs.length ? msgs[msgs.length - 1].dataset.messageId : null;
      })();
      if (!msgId) return alert('Cannot perform action: unknown message ID.');

      if (e.target.closest('.diff-widget-revert-btn')) {
        e.stopPropagation();
        
        const doRevert = async (force) => {
          try {
            const res = await fetch('/api/chat/revert-file', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ message_id: msgId, path: path, conversation_id: window.currentConversationId, force: force })
            });
            const data = await res.json();
            if (res.status === 409 && data.error === "USER_EDITED") {
              showConfirm('Manual Edits Detected', `You have manually edited "${path}" since the AI modified it. Reverting will overwrite your edits. Are you sure?`, () => doRevert(true));
              return;
            }
            if (!res.ok) throw new Error(data.error || 'Failed to revert');
            row.classList.add('reverted');
            row.querySelector('.diff-widget-revert-btn')?.remove();
            if (window.refreshExplorerTree) window.refreshExplorerTree();
          } catch (err) {
            alert("Failed to revert file: " + err.message);
          }
        };

        showConfirm('Revert File', `Are you sure you want to revert "${path}" to its state before this AI response?`, () => doRevert(false));
        return;
      }
      
      try {
          const res = await fetch(`/api/chat/file-diff?path=${encodeURIComponent(path)}&message_id=${encodeURIComponent(msgId)}`);
          if (!res.ok) throw new Error("Failed to fetch diff");
          const diffContent = await res.text();
          
          const synthFile = {
              path: path,
              additions: fileStats[path]?.additions || 0,
              deletions: fileStats[path]?.deletions || 0,
              artifact: { content: diffContent }
          };

          window.viewArtifact(reviewArtifactForFile(synthFile));
      } catch (err) {
          alert("Error loading diff: " + err.message);
      }
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
