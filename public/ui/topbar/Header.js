import { readJsonOrText } from '../../core/api.js?v=1785573007908';
import { $ } from '../../utils/dom.js?v=1785573007908';

let _cachedModel = '';
let _cachedWorkspace = '';

export function updateModelTag(model, workspace) {
  if (model) _cachedModel = model;
  if (workspace != null) _cachedWorkspace = workspace.replace(/[/\\]$/, '').split(/[/\\]/).pop() || '';
  let text = _cachedModel;
  if (_cachedWorkspace) {
    text += ' | ' + _cachedWorkspace;
  }
  const el = document.getElementById('modelTag');
  if (el) el.textContent = text;
}

export async function checkHealth() {
  try {
    const r = await fetch('/api/status');
    const d = await r.json();
    const el = $('healthStatus');
    if (el) {
      if (r.ok && (d.status === 'state.running' || d.status === 'idle')) {
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

export function downloadJson(filename, data) {
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

export async function exportTimeline() {
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

