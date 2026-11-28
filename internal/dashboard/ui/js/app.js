import { state, $, $$, api, toast, esc, updateErrBadge } from './api.js?v=1.4.6';
import {
  loadOverview, loadLogs, renderLogs, logMatchesFilter, renderLogRowHTML,
  loadSkills, renderSkills, loadMemory, updateMemoryQuotaUI,
  loadAccounts, loadCronAndSessions, setCronPanelOpen, loadChatPlayground
} from './views.js?v=1.4.6';

function openDrawer(open) {
  $('#sidebar').classList.toggle('open', open);
  $('#drawer-backdrop').classList.toggle('open', open);
}

function closeAllModals() {
  $$('.modal.open').forEach((m) => {
    m.classList.remove('open');
    m.setAttribute('aria-hidden', 'true');
  });
  const sm = $('#sys-menu');
  if (sm) sm.open = false;
}

$('#btn-menu')?.addEventListener('click', () => openDrawer(true));
$('#btn-drawer-close')?.addEventListener('click', () => openDrawer(false));
$('#drawer-backdrop')?.addEventListener('click', () => openDrawer(false));
window.addEventListener('resize', () => {
  if (window.innerWidth > 820) openDrawer(false);
});
window.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    openDrawer(false);
    closeAllModals();
  }
});
document.addEventListener('click', (e) => {
  const sm = $('#sys-menu');
  if (sm && sm.open && !sm.contains(e.target)) sm.open = false;
  if (e.target.classList?.contains('modal') && e.target.classList.contains('open')) {
    e.target.classList.remove('open');
    e.target.setAttribute('aria-hidden', 'true');
  }
});

const ROUTES = ['overview', 'logs', 'skills', 'memory', 'accounts', 'cron', 'chat'];

function activateTab(tab, push = true) {
  if (!ROUTES.includes(tab)) tab = 'overview';
  state.tab = tab;
  $$('.nav-btn').forEach((b) => b.classList.toggle('active', b.dataset.tab === tab));
  $$('.tab-panel').forEach((p) => p.classList.toggle('active', p.id === 'tab-' + tab));
  const btn = $(`.nav-btn[data-tab="${tab}"]`);
  if (btn) $('#page-title').textContent = btn.querySelector('.nav-lbl').textContent.trim();
  if (push && window.location.pathname !== '/' + tab) {
    history.pushState({ tab }, '', '/' + tab);
  }
  openDrawer(false);
  refreshActiveTab();
}

$$('.nav-btn').forEach((btn) => {
  btn.addEventListener('click', () => activateTab(btn.dataset.tab, true));
});
window.addEventListener('popstate', () => {
  const seg = window.location.pathname.replace(/^\/+|\/+$/g, '');
  activateTab(seg || 'overview', false);
});

async function refreshActiveTab() {
  try {
    if (state.tab === 'overview') await loadOverview();
    else if (state.tab === 'logs') await loadLogs();
    else if (state.tab === 'skills') await loadSkills();
    else if (state.tab === 'memory') await loadMemory();
    else if (state.tab === 'accounts') await loadAccounts();
    else if (state.tab === 'cron') await loadCronAndSessions();
    else if (state.tab === 'chat') await loadChatPlayground();
  } catch (e) {
    if (e.message !== 'Unauthorized') toast('Error: ' + e.message);
  }
}

function connectSSE() {
  const url = state.token ? `/api/logs/stream?token=${encodeURIComponent(state.token)}` : '/api/logs/stream';
  const es = new EventSource(url);
  es.onopen = () => {
    $('#sse-status').classList.add('connected');
    $('#sse-label').textContent = 'LIVE SSE';
  };
  es.onmessage = (ev) => {
    if (state.paused) return;
    try {
      const entry = JSON.parse(ev.data);
      state.logs.unshift(entry);
      if (state.logs.length > 500) state.logs.length = 500;
      if (entry.level === 'ERROR') {
        updateErrBadge(parseInt($('#nav-err-badge').textContent || '0', 10) + 1);
      }
      if (state.tab === 'logs' && logMatchesFilter(entry)) {
        state.filteredLogs.unshift(entry);
        const box = $('#log-list');
        if (box) {
          if (state.filteredLogs.length === 1) box.innerHTML = '';
          box.insertAdjacentHTML('afterbegin', renderLogRowHTML(entry, entry.id ?? `sse-${Date.now()}`));
          while (box.children.length > 300) box.lastElementChild?.remove();
        }
        if ($('#log-count')) {
          const n = state.filteredLogs.length;
          $('#log-count').textContent = `${n} ${n === 1 ? 'entry' : 'entries'}`;
        }
      }
    } catch {}
  };
  es.onerror = () => {
    $('#sse-status').classList.remove('connected');
    $('#sse-label').textContent = 'Reconnecting';
  };
}

function openSkillModal(name) {
  const sk = state.skills.find((x) => x.name === name);
  const f = $('#form-skill');
  if (sk) {
    $('#skill-modal-title').textContent = 'Edit Skill: ' + sk.name;
    f.mode.value = 'update';
    $('#sk-name').value = sk.name;
    $('#sk-name').readOnly = true;
    $('#sk-desc').value = sk.description;
    $('#sk-body').value = sk.body;
  } else {
    $('#skill-modal-title').textContent = 'New Skill';
    f.mode.value = 'create';
    $('#sk-name').value = '';
    $('#sk-name').readOnly = false;
    $('#sk-desc').value = '';
    $('#sk-body').value = '';
  }
  $('#skill-modal').classList.add('open');
  $('#skill-modal').setAttribute('aria-hidden', 'false');
}

function closeSkillModal() {
  $('#skill-modal').classList.remove('open');
  $('#skill-modal').setAttribute('aria-hidden', 'true');
}

$('#log-level-tabs').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-level]');
  if (!btn) return;
  $$('#log-level-tabs .btn').forEach((b) => b.classList.remove('active'));
  btn.classList.add('active');
  state.level = btn.dataset.level;
  renderLogs();
});
$('#log-search').addEventListener('input', (e) => { state.search = e.target.value; renderLogs(); });
$('#log-preset-tabs')?.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-preset]');
  if (!btn) return;
  $$('#log-preset-tabs .btn').forEach((b) => b.classList.remove('active'));
  btn.classList.add('active');
  $('#log-search').value = btn.dataset.preset;
  state.search = btn.dataset.preset;
  renderLogs();
});

function openLogDetailModal(entry) {
  if (!entry) return;
  $('#log-detail-title').textContent = `${entry.level || 'INFO'}: ${entry.message || 'Log Entry'}`;
  const sumBox = $('#log-detail-summary');
  if (sumBox) {
    const attrs = entry.attrs || {};
    const pairs = [
      ['Time', entry.time || '—'],
      ['Level', entry.level || 'INFO'],
      ['Event', entry.message || '—'],
      ...Object.entries(attrs).filter(([, v]) => v !== '' && v !== null && v !== undefined)
    ];
    sumBox.innerHTML = `<table class="kv-table"><tbody>${pairs.map(([k, v]) => {
      const val = typeof v === 'string' ? v : JSON.stringify(v);
      const isErr = k === 'error' || k === 'err';
      return `<tr><td class="kv-key mono">${esc(k)}</td><td class="kv-val mono ${isErr ? 'text-red' : ''}">${esc(val)}</td></tr>`;
    }).join('')}</tbody></table>`;
  }
  $('#log-detail-body').textContent = JSON.stringify(entry, null, 2);
  setLogModalTab('summary');
  $('#log-detail-modal').classList.add('open');
  $('#log-detail-modal').setAttribute('aria-hidden', 'false');
}

function setLogModalTab(tab) {
  $$('#log-modal-tabs .btn').forEach((b) => b.classList.toggle('active', b.dataset.lmtab === tab));
  $('#log-detail-summary')?.classList.toggle('hidden', tab !== 'summary');
  $('#log-detail-body')?.classList.toggle('hidden', tab !== 'json');
}

$('#log-modal-tabs')?.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-lmtab]');
  if (btn) setLogModalTab(btn.dataset.lmtab);
});

function findLogByRow(row) {
  const id = row?.dataset?.logId;
  if (!id) return null;
  const byId = state.filteredLogs.find((x) => String(x.id) === id);
  if (byId) return byId;
  if (id.startsWith('i-')) return state.filteredLogs[Number(id.slice(2))] || null;
  return state.logs.find((x) => String(x.id) === id) || null;
}

$('#log-list')?.addEventListener('click', (e) => {
  const row = e.target.closest('.log-row');
  if (row) openLogDetailModal(findLogByRow(row));
});
$('#log-list')?.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' || e.key === ' ') {
    const row = e.target.closest('.log-row');
    if (row) {
      e.preventDefault();
      openLogDetailModal(findLogByRow(row));
    }
  }
});
$('#btn-log-detail-close')?.addEventListener('click', () => {
  $('#log-detail-modal').classList.remove('open');
  $('#log-detail-modal').setAttribute('aria-hidden', 'true');
});
$('#btn-log-copy')?.addEventListener('click', async () => {
  await navigator.clipboard?.writeText($('#log-detail-body').textContent || '');
  toast('Log JSON copied');
});
$('#btn-log-pause').addEventListener('click', () => {
  state.paused = !state.paused;
  $('#btn-log-pause').textContent = state.paused ? 'Resume' : 'Pause';
});
$('#btn-log-download').addEventListener('click', () => {
  const url = state.token ? `/api/logs/download?token=${encodeURIComponent(state.token)}` : '/api/logs/download';
  window.open(url, '_blank');
});
$('#btn-log-clear').addEventListener('click', async () => {
  await api('/api/logs/clear', { method: 'POST' });
  state.logs = [];
  state.filteredLogs = [];
  updateErrBadge(0);
  renderLogs();
  toast('Logs cleared');
});

function applyTheme(t) {
  state.theme = t === 'dark' ? 'dark' : 'light';
  document.documentElement.setAttribute('data-theme', state.theme);
  localStorage.setItem('renstra_theme', state.theme);
  if ($('#btn-theme')) $('#btn-theme').textContent = state.theme === 'dark' ? 'Light' : 'Dark';
}
applyTheme(state.theme);
$('#btn-theme')?.addEventListener('click', () => applyTheme(state.theme === 'dark' ? 'light' : 'dark'));

$('#btn-sync-ui')?.addEventListener('click', async () => {
  toast('Syncing UI from GitHub...');
  const res = await api('/api/ui/sync', { method: 'POST' });
  toast(`UI synced (${res.ui_version}). Reloading...`);
  setTimeout(() => window.location.reload(), 600);
});

$('#skill-filter-tabs')?.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-sfilter]');
  if (!btn) return;
  $$('#skill-filter-tabs .btn').forEach((b) => b.classList.remove('active'));
  btn.classList.add('active');
  state.skillFilter = btn.dataset.sfilter;
  state.skillPage = 1;
  renderSkills();
});
$('#btn-sk-prev')?.addEventListener('click', () => { state.skillPage--; renderSkills(); });
$('#btn-sk-next')?.addEventListener('click', () => { state.skillPage++; renderSkills(); });
$('#skill-search').addEventListener('input', () => { state.skillPage = 1; renderSkills(); });
$('#btn-skill-new').addEventListener('click', () => openSkillModal(''));
$('#btn-skill-close').addEventListener('click', closeSkillModal);
$('#btn-skill-cancel').addEventListener('click', closeSkillModal);
$('#btn-skill-reload').addEventListener('click', async () => {
  const r = await api('/api/skills/reload', { method: 'POST' });
  toast(`Reloaded (${r.count} skills)`);
  await loadSkills();
});
$('#form-skill').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = e.target;
  await api('/api/skills/save', {
    method: 'POST',
    body: JSON.stringify({ mode: f.mode.value, name: f.name.value, description: f.description.value, body: f.body.value })
  });
  closeSkillModal();
  toast('Skill saved');
  await loadSkills();
});
$('#skills-grid').addEventListener('click', async (e) => {
  const editName = e.target.dataset.editSkill;
  if (editName) return openSkillModal(editName);
  const delName = e.target.dataset.delSkill;
  if (delName && confirm(`Delete skill "${delName}"?`)) {
    await api('/api/skills/delete', { method: 'POST', body: JSON.stringify({ name: delName }) });
    toast('Skill deleted');
    await loadSkills();
  }
});

$('#mem-file-tabs')?.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-mem-tab]');
  if (!btn) return;
  $$('#mem-file-tabs .btn').forEach((b) => b.classList.remove('active'));
  btn.classList.add('active');
  state.memTab = btn.dataset.memTab;
  updateMemoryQuotaUI();
});
$$('[data-save-mem]').forEach((b) => b.addEventListener('click', async () => {
  const target = b.dataset.saveMem;
  await api('/api/memory', { method: 'POST', body: JSON.stringify({ target, text: $('#ed-' + target).value }) });
  toast(`${target.toUpperCase()}.md saved`);
  await loadMemory();
}));
$('#btn-reset-soul').addEventListener('click', async () => {
  if (!confirm('Reset SOUL.md to the default Zero Refusal template?')) return;
  await api('/api/memory', { method: 'POST', body: JSON.stringify({ target: 'reset_soul' }) });
  toast('SOUL.md reset to default');
  await loadMemory();
});
$$('[data-clear-mem]').forEach((b) => b.addEventListener('click', async () => {
  const target = b.dataset.clearMem;
  if (!confirm(`Clear ${target.toUpperCase()}.md?`)) return;
  await api('/api/memory', { method: 'POST', body: JSON.stringify({ target, text: '' }) });
  toast(`${target.toUpperCase()}.md cleared`);
  await loadMemory();
}));
$('#btn-save-limit').addEventListener('click', async () => {
  const limit = Number($('#mem-limit-input').value || 0);
  await api('/api/memory', { method: 'POST', body: JSON.stringify({ target: 'limit', limit }) });
  toast('Memory quota updated');
  await loadMemory();
});
$('#mem-fact-search')?.addEventListener('input', loadMemory);
$('#memory-facts-list')?.addEventListener('click', async (e) => {
  const id = e.target.dataset.delFact;
  if (!id || !confirm(`Delete episodic memory fact #${id}?`)) return;
  await api('/api/memory/facts/delete', { method: 'POST', body: JSON.stringify({ id: Number(id) }) });
  toast(`Fact #${id} deleted`);
  await loadMemory();
});

$('#ov-turns-body').addEventListener('click', async (e) => {
  const id = e.target.dataset.delTurn;
  if (!id) return;
  await api('/api/turns/delete', { method: 'POST', body: JSON.stringify({ id: Number(id) }) });
  toast(`Turn #${id} deleted`);
  await loadOverview();
});

$('#accounts-list').addEventListener('click', async (e) => {
  const editName = e.target.dataset.editAccount;
  if (editName) {
    const acc = (state.accounts || []).find((a) => a.Name === editName);
    if (!acc) return;
    const f = $('#form-account');
    f.name.value = acc.Name;
    f.kind.value = acc.Kind || 'custom';
    f.api_key.value = '';
    f.default_model.value = acc.DefaultModel || '';
    f.base_url.value = acc.BaseURL || '';
    if ($('#btn-acc-submit')) $('#btn-acc-submit').textContent = 'Update Account';
    f.scrollIntoView({ behavior: 'smooth', block: 'center' });
    toast(`Editing account "${acc.Name}"`);
    return;
  }
  const name = e.target.dataset.delAccount;
  if (name && confirm(`Delete account "${name}"?`)) {
    await api('/api/accounts/delete', { method: 'POST', body: JSON.stringify({ name }) });
    toast('Account deleted');
    await loadAccounts();
  }
});
$('#btn-acc-reset')?.addEventListener('click', () => {
  $('#form-account').reset();
  if ($('#btn-acc-submit')) $('#btn-acc-submit').textContent = 'Save Account';
});
$('#form-account').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  await api('/api/accounts', { method: 'POST', body: JSON.stringify(Object.fromEntries(fd.entries())) });
  e.target.reset();
  if ($('#btn-acc-submit')) $('#btn-acc-submit').textContent = 'Save Account';
  toast('Account saved');
  await loadAccounts();
});
$('#btn-set-model').addEventListener('click', async () => {
  await api('/api/model/default', { method: 'POST', body: JSON.stringify({ ref: $('#sel-default-model').value }) });
  toast('Default model updated');
  await Promise.all([loadAccounts(), loadOverview()]);
});
$('#form-model-add').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const payload = Object.fromEntries(fd.entries());
  payload.vision = fd.get('vision') === 'on';
  const res = await api('/api/models/add', { method: 'POST', body: JSON.stringify(payload) });
  e.target.reset();
  toast(`${res.count || 1} model(s) registered`);
  await loadAccounts();
});
$('#models-detail-list')?.addEventListener('click', async (e) => {
  const useRef = e.target.dataset.useModel;
  if (useRef) {
    await api('/api/model/default', { method: 'POST', body: JSON.stringify({ ref: useRef }) });
    toast(`Active model: ${useRef}`);
    await Promise.all([loadAccounts(), loadOverview()]);
    return;
  }
  const editRef = e.target.dataset.editModel;
  if (editRef) {
    const m = (state.modelDetails || []).find((x) => x.ref === editRef);
    if (!m) return;
    $('#model-edit-title').textContent = `Edit Model: ${m.ref}`;
    $('#me-old-ref').value = m.ref;
    $('#me-provider').value = m.provider;
    $('#me-model').value = m.model;
    $('#me-context').value = String(m.context || 128000);
    $('#me-max-output').value = String(m.max_output || 4096);
    $('#me-vision').checked = Boolean(m.vision);
    $('#model-edit-modal').classList.add('open');
    $('#model-edit-modal').setAttribute('aria-hidden', 'false');
    return;
  }
  const delRef = e.target.dataset.delModel;
  if (delRef && confirm(`Delete model "${delRef}"?`)) {
    await api('/api/models/delete', { method: 'POST', body: JSON.stringify({ ref: delRef }) });
    toast('Model removed');
    await loadAccounts();
  }
});
function closeModelEditModal() {
  $('#model-edit-modal')?.classList.remove('open');
  $('#model-edit-modal')?.setAttribute('aria-hidden', 'true');
}
$('#btn-model-edit-close')?.addEventListener('click', closeModelEditModal);
$('#btn-model-edit-cancel')?.addEventListener('click', closeModelEditModal);
$('#form-model-edit')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const oldRef = $('#me-old-ref').value;
  const provider = $('#me-provider').value.trim();
  const model = $('#me-model').value.trim();
  const context = $('#me-context').value.trim();
  const max_output = $('#me-max-output').value.trim();
  const vision = $('#me-vision').checked;
  if (oldRef && oldRef !== `${provider}:${model}`) {
    await api('/api/models/delete', { method: 'POST', body: JSON.stringify({ ref: oldRef }) }).catch(() => {});
  }
  await api('/api/models/add', { method: 'POST', body: JSON.stringify({ provider, model, context, max_output, vision }) });
  closeModelEditModal();
  toast(`Model "${provider}:${model}" updated`);
  await loadAccounts();
});

function openCronModal(job) {
  $('#cron-modal-title').textContent = job ? `Edit Cron #${job.ID}` : 'New Cron Job';
  $('#cr-id').value = job ? job.ID : 0;
  $('#cr-chat').value = job ? job.ChatID : 1;
  $('#cr-chat').readOnly = Boolean(job);
  $('#cr-sched').value = job ? job.Schedule : '';
  $('#cr-prompt').value = job ? job.Prompt : '';
  $('#cron-modal').classList.add('open');
  $('#cron-modal').setAttribute('aria-hidden', 'false');
}
function closeCronModal() {
  $('#cron-modal').classList.remove('open');
  $('#cron-modal').setAttribute('aria-hidden', 'true');
}
$('#btn-cron-toggle')?.addEventListener('click', () => setCronPanelOpen(!state.cronOpen));
$('#btn-cron-new').addEventListener('click', () => openCronModal(null));
$('#btn-cron-close').addEventListener('click', closeCronModal);
$('#btn-cron-cancel').addEventListener('click', closeCronModal);
$('#form-cron').addEventListener('submit', async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  await api('/api/cron/save', {
    method: 'POST',
    body: JSON.stringify({
      id: Number(fd.get('id') || 0),
      chat_id: Number(fd.get('chat_id') || 1),
      schedule: fd.get('schedule'),
      prompt: fd.get('prompt')
    })
  });
  closeCronModal();
  toast('Cron schedule saved');
  await loadCronAndSessions();
});
$('#cron-body').addEventListener('click', async (e) => {
  const editId = e.target.dataset.editCron;
  if (editId) {
    const job = (state.jobs || []).find((j) => j.ID === Number(editId));
    if (job) openCronModal(job);
    return;
  }
  const toggleId = e.target.dataset.toggleCron;
  if (toggleId) {
    await api('/api/cron/toggle', { method: 'POST', body: JSON.stringify({ id: Number(toggleId), enabled: e.target.dataset.cronNext === 'true' }) });
    return loadCronAndSessions();
  }
  const delId = e.target.dataset.delCron;
  if (delId) {
    await api('/api/cron/delete', { method: 'POST', body: JSON.stringify({ id: Number(delId) }) });
    return loadCronAndSessions();
  }
});

$('#btn-transcript-close')?.addEventListener('click', () => {
  $('#transcript-modal').classList.remove('open');
  $('#transcript-modal').setAttribute('aria-hidden', 'true');
});
$('#sessions-body').addEventListener('click', async (e) => {
  const openChatId = e.target.dataset.openChat;
  if (openChatId) {
    if ($('#chat-pg-id')) $('#chat-pg-id').value = openChatId;
    if ($('#chat-pg-model')) $('#chat-pg-model').value = '';
    activateTab('chat', true);
    return;
  }
  const viewId = e.target.dataset.viewSess;
  if (viewId) {
    const data = await api(`/api/sessions/messages?chat_id=${encodeURIComponent(viewId)}`);
    const msgs = data.messages || [];
    $('#transcript-title').textContent = `Transcript: Chat ${viewId}`;
    $('#transcript-body').innerHTML = !msgs.length
      ? '<div class="muted">No messages in active session.</div>'
      : msgs.map((m) => `<div class="list-item" style="flex-direction:column;align-items:stretch;"><div class="mono muted">${esc((m.Role || m.role || 'msg').toUpperCase())}</div><div style="white-space:pre-wrap;word-break:break-word;">${esc(m.Content || m.content || '(tool call)')}</div></div>`).join('');
    $('#transcript-modal').classList.add('open');
    $('#transcript-modal').setAttribute('aria-hidden', 'false');
    return;
  }
  const cid = e.target.dataset.clearSess;
  if (!cid || !confirm(`Reset session history for chat ${cid}?`)) return;
  await api('/api/sessions/clear', { method: 'POST', body: JSON.stringify({ chat_id: Number(cid) }) });
  toast(`Session ${cid} reset`);
  await loadCronAndSessions();
});

$('#btn-refresh').addEventListener('click', refreshActiveTab);
$('#form-fallbacks')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const fallbacks = $('#fb-list-input').value;
  const smart_fallback = $('#fb-smart-chk').checked;
  await api('/api/fallback/save', { method: 'POST', body: JSON.stringify({ fallbacks, smart_fallback }) });
  toast('Fallback chain saved');
  await Promise.all([loadAccounts(), loadOverview()]);
});
$('#btn-restart-svc')?.addEventListener('click', async () => {
  $('#sys-menu').open = false;
  if (!confirm('Restart RENSTRA service to load latest config and notify Telegram?')) return;
  await api('/api/service/restart', { method: 'POST' });
  toast('Service restarting... Reloading shortly');
  setTimeout(() => window.location.reload(), 3000);
});
$('#form-mcp-add')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const payload = Object.fromEntries(new FormData(e.target).entries());
  await api('/api/mcp/save', { method: 'POST', body: JSON.stringify(payload) });
  e.target.reset();
  toast('MCP server saved');
  await loadSkills();
});
$('#mcp-list')?.addEventListener('click', async (e) => {
  const name = e.target.dataset.delMcp;
  if (name && confirm(`Remove MCP server "${name}"?`)) {
    await api('/api/mcp/delete', { method: 'POST', body: JSON.stringify({ name }) });
    toast('MCP server removed');
    await loadSkills();
  }
});
$('#btn-backup')?.addEventListener('click', () => {
  $('#sys-menu').open = false;
  const url = state.token ? `/api/backup?token=${encodeURIComponent(state.token)}` : '/api/backup';
  window.location.href = url;
});
$('#btn-restore')?.addEventListener('click', () => {
  $('#sys-menu').open = false;
  $('#inp-restore-file')?.click();
});
$('#inp-restore-file')?.addEventListener('change', async (e) => {
  const file = e.target.files?.[0];
  if (!file) return;
  if (!confirm(`Restore backup from "${file.name}"? This overwrites current data.`)) {
    e.target.value = '';
    return;
  }
  try {
    const fd = new FormData();
    fd.append('file', file);
    const headers = state.token ? { Authorization: `Bearer ${state.token}` } : {};
    const res = await fetch('/api/restore', { method: 'POST', headers, body: fd });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Restore failed');
    toast(`Restored ${data.count || 0} files`);
    await refreshActiveTab();
  } catch (err) {
    toast('Restore error: ' + err.message);
  } finally {
    e.target.value = '';
  }
});
$('#btn-chat-load')?.addEventListener('click', loadChatPlayground);
$('#btn-chat-clear')?.addEventListener('click', async () => {
  const cid = Number($('#chat-pg-id')?.value || 1) || 1;
  if (!confirm(`Reset session ${cid}?`)) return;
  await api('/api/sessions/clear', { method: 'POST', body: JSON.stringify({ chat_id: cid }) });
  toast(`Session ${cid} cleared`);
  await loadChatPlayground();
});
$('#form-chat-pg')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const cid = Number($('#chat-pg-id')?.value || 1) || 1;
  const model = $('#chat-pg-model')?.value || '';
  const inp = $('#chat-pg-input');
  const prompt = inp.value.trim();
  if (!prompt) return;
  inp.value = '';
  $('#btn-chat-send').disabled = true;
  $('#chat-pg-status').textContent = model ? `Thinking (${model})...` : 'Thinking...';
  try {
    await api('/api/chat', { method: 'POST', body: JSON.stringify({ chat_id: cid, prompt, model }) });
    await loadChatPlayground();
  } finally {
    $('#btn-chat-send').disabled = false;
    $('#chat-pg-status').textContent = 'Ready';
  }
});
$('#btn-logout')?.addEventListener('click', () => {
  state.token = '';
  localStorage.removeItem('renstra_dash_token');
  $('#auth-modal').classList.add('open');
  $('#auth-modal').setAttribute('aria-hidden', 'false');
});
$('#btn-reset-fb').addEventListener('click', async () => {
  await api('/api/fallback/reset', { method: 'POST' });
  toast('Circuit breakers reset');
  await loadOverview();
});
$('#form-auth').addEventListener('submit', async (e) => {
  e.preventDefault();
  state.token = $('#auth-token-input').value.trim();
  localStorage.setItem('renstra_dash_token', state.token);
  try {
    await api('/api/overview');
    $('#auth-err').classList.add('hidden');
    $('#auth-modal').classList.remove('open');
    $('#auth-modal').setAttribute('aria-hidden', 'true');
    await refreshActiveTab();
  } catch {
    $('#auth-err').textContent = 'Invalid dashboard token';
    $('#auth-err').classList.remove('hidden');
  }
});

async function loadChromeMeta() {
  try {
    const ov = await api('/api/overview');
    $('#sb-version').textContent = ov.version || 'v1.4.5';
    $('#sb-uptime').textContent = 'uptime ' + `${Math.floor((ov.uptime_sec || 0) / 3600)}h ${Math.floor(((ov.uptime_sec || 0) % 3600) / 60)}m ${(ov.uptime_sec || 0) % 60}s`;
    if ($('#sb-ui-src')) {
      $('#sb-ui-src').textContent = `ui: ${ov.ui_source || 'embedded'}${ov.ui_version ? ' (' + ov.ui_version + ')' : ''}`;
    }
    $('#top-model').textContent = 'model: ' + (ov.active_model || '—');
    $('#btn-logout')?.classList.toggle('hidden', !ov.auth_required);
    updateErrBadge(ov.error_count || 0);
  } catch {}
}

const initSeg = window.location.pathname.replace(/^\/+|\/+$/g, '');
activateTab(ROUTES.includes(initSeg) ? initSeg : 'overview', false);
if (state.tab !== 'overview') loadChromeMeta();
connectSSE();
