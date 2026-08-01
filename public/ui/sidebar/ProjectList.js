import { $, esc } from '../../utils/dom.js?v=1785573007908';
import { readJsonOrText } from '../../core/api.js?v=1785573007908';
import { refresh, triggerIndex, renderSession } from '../../core/engine.js?v=1785573007908';

export function renderProjects(projects) {
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
        if (chatDelBtn) chatDelBtn.onclick = (e) => {
          e.stopPropagation();
          showConfirm('Delete Conversation', `Are you sure you want to delete "${c.title}"?`, async () => {
            const r = await fetch('/api/chat/delete', {
              method: 'POST',
              headers: {
                'Content-Type': 'application/json'
              },
              body: JSON.stringify({
                id: c.id,
                workspace: p.path
              })
            });
            const d = await r.json();
            if (d.ok) await renderSession(d.display_log);
            else alert(d.error || 'Delete failed');
            if (typeof refresh === 'function') refresh();
          });
        };
        chats.appendChild(row)
      })
    }
    box.appendChild(d)
  })
}
export async function selectProject(path) {
  if (window.running) return;
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
  window.firstStatus = true;
  document.querySelector('.col.left').classList.remove('open');
  document.body.classList.remove('drawer-open');
  await refresh();
  triggerIndex()
}

export function showConfirm(title, text, onConfirm) {
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
export async function removeProject(path, e) {
  if (e) e.stopPropagation();
  if (window.running) return;
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
    window.firstStatus = true;
    await refresh();
  });
}
export async function openProjectModal() {
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
  window.firstStatus = true;
  await refresh();
  triggerIndex();
  $('workspaceNameInput').value = '';
};
