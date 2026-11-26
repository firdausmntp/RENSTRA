import { state, $, $$, api, toast, updateErrBadge } from './api.js?v=1.4.0';
import {
  loadOverview, loadLogs, renderLogs,
  loadSkills, renderSkills, loadMemory, loadAccounts, loadCronAndSessions, loadChatPlayground
} from './views.js?v=1.4.0';

function openDrawer(open) {
  $('#sidebar').classList.toggle('open', open);
  $('#drawer-backdrop').classList.toggle('open', open);
}

$('#btn-menu')?.addEventListener('click', () => openDrawer(true));
$('#btn-drawer-close')?.addEventListener('click', () => openDrawer(false));
$('#drawer-backdrop')?.addEventListener('click', () => openDrawer(false));
window.addEventListener('resize', () => {
  if (window.innerWidth > 820) openDrawer(false);
});
window.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') openDrawer(false);
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
      if (state.tab === 'logs') renderLogs();
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
  if (!e.target.dataset.level) return;
  $$('#log-level-tabs .btn').forEach((b) => b.classList.remove('active'));
  e.target.classList.add('active');
  state.level = e.target.dataset.level;
  renderLogs();
});
$('#log-search').addEventListener('input', (e) => { state.search = e.target.value; renderLogs(); });
$('#log-preset-tabs')?.addEventListener('click', (e) => {
  if (e.target.dataset.preset === undefined) return;
  $$('#log-preset-tabs .btn').forEach((b) => b.classList.remove('active'));
  e.target.classList.add('active');
  $('#log-search').value = e.target.dataset.preset;
  state.search = e.target.dataset.preset;
  renderLogs();
});
$('#log-list')?.addEventListener('click', (e) => {
  const row = e.target.closest('.log-row');
  if (!row || row.dataset.logIdx === undefined) return;
  const entry = (state.filteredLogs || [])[Number(row.dataset.logIdx)];
  if (!entry) return;
  $('#log-detail-title').textContent = `${entry.level || 'INFO'}: ${entry.message || 'Log Entry'}`;
  $('#log-detail-body').textContent = JSON.stringify(entry, null, 2);
  $('#log-detail-modal').classList.add('open');
  $('#log-detail-modal').setAttribute('aria-hidden', 'false');
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
  if (!e.target.dataset.sfilter) return;
  $$('#skill-filter-tabs .btn').forEach((b) => b.classList.remove('active'));
  e.target.classList.add('active');
  state.skillFilter = e.target.dataset.sfilter;
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

$$('[data-save-mem]').forEach((b) => b.addEventListener('click', async () => {
  const target = b.dataset.saveMem;
  await api('/api/memory', { method: 'POST', body: JSON.stringify({ target, text: $('#ed-' + target).value }) });
  toast(`${target.toUpperCase()}.md saved`);
  await loadMemory();
}));
$('#btn-reset-soul').addEventListener('click', async () => {
  await api('/api/memory', { method: 'POST', body: JSON.stringify({ target: 'reset_soul' }) });
  toast('SOUL.md reset to Zero Refusal default');
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
  if (!id || !confirm(`Delete smart memory fact #${id}?`)) return;
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
    f.kind.value = acc.Kind || 'openai';
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
  await loadOverview();
});
$('#btn-del-model').addEventListener('click', async () => {
  const ref = $('#sel-default-model').value;
  if (!ref || !confirm(`Delete model "${ref}"?`)) return;
  await api('/api/models/delete', { method: 'POST', body: JSON.stringify({ ref }) });
  toast('Model removed');
  await loadAccounts();
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
  const viewId = e.target.dataset.viewSess;
  if (viewId) {
    const data = await api(`/api/sessions/messages?chat_id=${encodeURIComponent(viewId)}`);
    const msgs = data.messages || [];
    $('#transcript-title').textContent = `Transcript: Chat ${viewId}`;
    $('#transcript-body').innerHTML = !msgs.length
      ? '<div class="muted">No messages in active session.</div>'
      : msgs.map((m) => `<div class="list-item" style="flex-direction:column;align-items:stretch;"><div class="mono muted">${(m.Role || m.role || 'msg').toUpperCase()}</div><div style="white-space:pre-wrap;word-break:break-word;">${(m.Content || m.content || '(tool call)').replace(/</g, '&lt;')}</div></div>`).join('');
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
  toast('Fallback chain saved & reloaded');
  await Promise.all([loadAccounts(), loadOverview()]);
});
$('#btn-restart-svc')?.addEventListener('click', async () => {
  if (!confirm('Restart RENSTRA service to load all latest config & notify Telegram?')) return;
  await api('/api/service/restart', { method: 'POST' });
  toast('Service restarting... Page will reload shortly');
  setTimeout(() => window.location.reload(), 3000);
});
$('#form-mcp-add')?.addEventListener('submit', async (e) => {
  e.preventDefault();
  const payload = Object.fromEntries(new FormData(e.target).entries());
  await api('/api/mcp/save', { method: 'POST', body: JSON.stringify(payload) });
  e.target.reset();
  toast('MCP server saved to mcp.yaml');
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
  const url = state.token ? `/api/backup?token=${encodeURIComponent(state.token)}` : '/api/backup';
  window.location.href = url;
});
$('#btn-restore')?.addEventListener('click', () => $('#inp-restore-file')?.click());
$('#inp-restore-file')?.addEventListener('change', async (e) => {
  const file = e.target.files?.[0];
  if (!file) return;
  if (!confirm(`Restore backup from "${file.name}"?`)) {
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
  const inp = $('#chat-pg-input');
  const prompt = inp.value.trim();
  if (!prompt) return;
  inp.value = '';
  $('#btn-chat-send').disabled = true;
  $('#chat-pg-status').textContent = 'Thinking...';
  try {
    await api('/api/chat', { method: 'POST', body: JSON.stringify({ chat_id: cid, prompt }) });
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
    $('#sb-version').textContent = ov.version || 'v1.3.0-beta';
    $('#sb-uptime').textContent = 'uptime ' + `${Math.floor((ov.uptime_sec||0)/3600)}h ${Math.floor(((ov.uptime_sec||0)%3600)/60)}m ${(ov.uptime_sec||0)%60}s`;
    if ($('#sb-ui-src')) {
      $('#sb-ui-src').textContent = `ui: ${ov.ui_source || 'embedded'}${ov.ui_version ? ' (' + ov.ui_version + ')' : ''}`;
    }
    $('#top-model').textContent = 'model: ' + (ov.active_model || '-');
    $('#btn-logout')?.classList.toggle('hidden', !ov.auth_required);
    updateErrBadge(ov.error_count || 0);
  } catch {}
}

const initSeg = window.location.pathname.replace(/^\/+|\/+$/g, '');
activateTab(ROUTES.includes(initSeg) ? initSeg : 'overview', false);
if (state.tab !== 'overview') loadChromeMeta();
connectSSE();
