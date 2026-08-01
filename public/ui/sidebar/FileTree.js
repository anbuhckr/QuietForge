import { $, esc } from '../../utils/dom.js?v=1785573007908';

export function getCodeMirrorMode(path) {
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

export let explorerPath = '';
export let explorerSelection = '';

export async function loadExplorerList(path) {
  const explorerListEl = $('explorerList');
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
    console.error('EXPLORER ERROR:', err);
    explorerListEl.innerHTML = '<div style="color:var(--danger); padding:10px;">Error loading files</div>';
  }
}

export function renderExplorerList(items) {
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

export function updateExplorerBreadcrumb() {
  const explorerBreadcrumbEl = $('explorerBreadcrumb');
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

export function navigateExplorer(path) {
  loadExplorerList(path);
}

export function selectExplorerItem(path) {
  explorerSelection = path;
  document.querySelectorAll('.explorer-item').forEach(el => {
    if (el.getAttribute('data-path') === path) {
      el.classList.add('selected');
    } else {
      el.classList.remove('selected');
    }
  });
}


export function clearExplorerSelection() {
  explorerSelection = '';
}
