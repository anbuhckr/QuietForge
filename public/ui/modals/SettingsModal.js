import { state } from '../../core/state.js?v=1785573007908';
import { readJsonOrText } from '../../core/api.js?v=1785573007908';
import { $, esc } from '../../utils/dom.js?v=1785573007908';

export async function openSettings() {
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

      state.uiRunTimeoutSeconds = parseFloat(c.ui_run_timeout || c.request_timeout || 3600) || 3600;
    }
  } catch (e) {
    console.error('Failed to load llm config', e)
  }
  $('settingsModal').classList.add('open')
}

export function addProviderUI(p, isPrimary) {
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
    
    <label class="config-label" style="margin-top: 8px;">Proxies (Comma-separated)</label>
    <input type="text" class="config-input cfg-proxies" value="${esc(p.proxies || '')}" placeholder="http://proxy1.com,http://proxy2.com">
    
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
        <label class="config-label">Tail Turns</label>
        <input type="number" class="config-input cfg-tail-turns" value="${p.tail_turns || 0}" placeholder="10">
      </div>
      <div style="flex:1;">
        <label class="config-label">Preserve Recent Tokens</label>
        <input type="number" class="config-input cfg-preserve-recent-tokens" value="${p.preserve_recent_tokens || 0}" placeholder="1000">
      </div>
    </div>
    
    <div style="display:flex; gap:10px; margin-top: 8px;">
      <div style="flex:1;">
        <label class="config-label">Reserved Tokens</label>
        <input type="number" class="config-input cfg-reserved" value="${p.reserved || 0}" placeholder="2000">
      </div>
      <div style="flex:1;">
        <label class="config-label">Tool Truncation Limit</label>
        <input type="number" class="config-input cfg-tool-truncation-limit" value="${p.tool_truncation_limit || 0}" placeholder="10000">
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
          proxies: el.querySelector('.cfg-proxies').value,
          disable_vision: el.querySelector('.cfg-disable-vision').checked,
          context_window: parseInt(el.querySelector('.cfg-context-window').value) || 0,
          max_messages: parseInt(el.querySelector('.cfg-max-messages').value) || 0,
          tail_turns: parseInt(el.querySelector('.cfg-tail-turns').value) || 0,
          preserve_recent_tokens: parseInt(el.querySelector('.cfg-preserve-recent-tokens').value) || 0,
          reserved: parseInt(el.querySelector('.cfg-reserved').value) || 0,
          tool_truncation_limit: parseInt(el.querySelector('.cfg-tool-truncation-limit').value) || 0
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
    if (!providerId) { resultEl.textContent = 'Save to test'; return; }
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

export async function openMcpSettings() {
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

export function addMcpUI(id, srv) {
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

export async function openCompactionSettings() {
  $('settingsModal').classList.remove('open');
  try {
    const r = await fetch('/api/config/compaction');
    const c = await r.json();
    if (c) {
      $('cfgCompactionAuto').checked = c.auto || false;
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

export async function openEmbeddingSettings() {
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

