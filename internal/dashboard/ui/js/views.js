import { state, $, api, fmtTok, fmtDur, esc, updateErrBadge } from './api.js';

export async function loadOverview() {
  const [ov, turnsData] = await Promise.all([api('/api/overview'), api('/api/turns?limit=10')]);
  $('#sb-version').textContent = ov.version || 'v1.3.0';
  $('#sb-uptime').textContent = 'uptime ' + fmtDur(ov.uptime_sec || 0);
  $('#top-model').textContent = 'model: ' + (ov.active_model || '-');
  $('#ov-model').textContent = ov.active_model || '-';
  $('#ov-provider').textContent = `${ov.provider || '-'} • ctx ${fmtTok(ov.context_window)}`;
  $('#ov-cost-today').textContent = '$' + (ov.usage_today?.CostUSD || 0).toFixed(4);
  $('#ov-tok-today').textContent = `↑${fmtTok(ov.usage_today?.TokensIn)} ↓${fmtTok(ov.usage_today?.TokensOut)}`;
  $('#ov-cost-30d').textContent = '$' + (ov.usage_30d?.CostUSD || 0).toFixed(4);
  $('#ov-tok-30d').textContent = `↑${fmtTok(ov.usage_30d?.TokensIn)} ↓${fmtTok(ov.usage_30d?.TokensOut)}`;
  $('#ov-errs').textContent = ov.error_count || 0;
  $('#ov-warns').textContent = ov.warn_count || 0;
  $('#ov-skills').textContent = `${ov.skills_count || 0} skills loaded`;
  $('#btn-logout')?.classList.toggle('hidden', !ov.auth_required);
  updateErrBadge(ov.error_count || 0);

  const chart = $('#daily-chart');
  const days = ov.daily_usage || [];
  if (!days.length) {
    chart.innerHTML = '<div class="muted">No daily usage recorded yet.</div>';
  } else {
    const max = Math.max(...days.map((d) => d.tokens_in + d.tokens_out), 1);
    chart.innerHTML = days.map((d) => {
      const tot = d.tokens_in + d.tokens_out;
      const pct = Math.max(4, Math.round((tot / max) * 100));
      return `<div class="bar-col" title="${d.day}: ${fmtTok(tot)} tok ($${d.cost_usd.toFixed(4)})"><div class="bar" style="height:${pct}%"></div><div class="bar-lbl">${d.day.slice(5)}</div></div>`;
    }).join('');
  }

  const fbBox = $('#ov-fallbacks');
  const fbs = ov.fallbacks || [];
  fbBox.innerHTML = !fbs.length
    ? '<div class="muted">No fallback targets configured.</div>'
    : fbs.map((f) => {
        const st = f.cooldown_left
          ? `<span class="badge badge-err">Cooldown ${esc(f.cooldown_left)}</span>`
          : `<span class="badge badge-ok">Healthy</span>`;
        return `<div class="list-item"><div><div class="mono">${esc(f.provider)}:${esc(f.model)}</div><div class="muted">${esc(f.last_error || 'Ready')}</div></div>${st}</div>`;
      }).join('');

  const turns = turnsData.turns || [];
  $('#ov-turns-body').innerHTML = !turns.length
    ? '<tr><td colspan="6" class="muted">No turns recorded yet.</td></tr>'
    : turns.map((t) => {
        const cls = t.status === 'error' ? 'badge-err' : t.status === 'done' ? 'badge-ok' : 'badge-warn';
        return `<tr><td class="mono">#${t.id}</td><td class="mono">${t.chat_id}</td><td><span class="badge ${cls}">${esc(t.status)}</span></td><td class="muted">${esc(t.started_at)}</td><td class="text-red">${esc(t.error || '-')}</td><td><button class="btn btn-sm btn-danger" data-del-turn="${t.id}">Delete</button></td></tr>`;
      }).join('');
}

export async function loadLogs() {
  const data = await api(`/api/logs?level=${encodeURIComponent(state.level)}&q=${encodeURIComponent(state.search)}&limit=250`);
  state.logs = data.entries || [];
  updateErrBadge(data.error_count || 0);
  renderLogs();
}

export function renderLogs() {
  const box = $('#log-list');
  const rows = state.logs.filter((e) => {
    if (state.level !== 'ALL' && e.level !== state.level) return false;
    if (state.search && !JSON.stringify(e).toLowerCase().includes(state.search.toLowerCase())) return false;
    return true;
  });
  box.innerHTML = !rows.length
    ? '<div class="muted" style="padding:12px;">No matching log entries.</div>'
    : rows.map((e) => {
        const cls = e.level === 'ERROR' ? 'badge-err' : e.level === 'WARN' ? 'badge-warn' : 'badge-info';
        const attrs = e.attrs ? `<div class="log-attrs">${esc(JSON.stringify(e.attrs))}</div>` : '';
        return `<div class="log-row"><span class="muted log-time">${esc(e.time)}</span><span class="badge ${cls}">${esc(e.level)}</span><span class="log-msg">${esc(e.message)}</span>${attrs}</div>`;
      }).join('');
}

export async function loadSkills() {
  const data = await api('/api/skills');
  state.skills = data.skills || [];
  renderSkills();
}

export function renderSkills() {
  const q = ($('#skill-search').value || '').toLowerCase();
  const list = state.skills.filter((s) => s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q));
  $('#skills-grid').innerHTML = !list.length
    ? '<div class="muted">No skills found.</div>'
    : list.map((s) => `
      <div class="skill-card">
        <div class="card-head">
          <strong class="mono">${esc(s.name)}</strong>
          <span class="badge ${s.writable ? 'badge-ok' : 'badge-info'}">${s.writable ? 'Writable' : 'Read-Only'}</span>
        </div>
        <div class="muted">${esc(s.description)}</div>
        <div class="card-actions">
          <button class="btn btn-sm btn-ghost" data-edit-skill="${esc(s.name)}">Edit</button>
          ${s.writable ? `<button class="btn btn-sm btn-danger" data-del-skill="${esc(s.name)}">Delete</button>` : ''}
        </div>
      </div>`).join('');
}

export async function loadMemory() {
  const d = await api('/api/memory');
  $('#ed-soul').value = d.soul || '';
  $('#ed-memory').value = d.memory || '';
  $('#ed-user').value = d.user || '';
  $('#mem-soul-count').textContent = `${d.soul_chars}/${d.soul_limit}`;
  $('#mem-memory-count').textContent = `${d.memory_chars}/${d.memory_limit}`;
  $('#mem-user-count').textContent = `${d.user_chars}/${d.user_limit}`;
  if ($('#mem-limit-input')) $('#mem-limit-input').value = d.memory_limit || 2200;
}

export async function loadAccounts() {
  const d = await api('/api/accounts');
  const box = $('#accounts-list');
  box.innerHTML = !(d.accounts || []).length
    ? '<div class="muted">No provider accounts configured.</div>'
    : d.accounts.map((a) => `
      <div class="list-item">
        <div>
          <div><strong class="mono">${esc(a.Name)}</strong> <span class="badge badge-info">${esc(a.Kind)}</span></div>
          <div class="muted mono">model: ${esc(a.DefaultModel || '-')} • key: ${esc(a.MaskedKey)}</div>
        </div>
        <button class="btn btn-sm btn-danger" data-del-account="${esc(a.Name)}">Delete</button>
      </div>`).join('');
  const sel = $('#sel-default-model');
  sel.innerHTML = (d.models || []).map((m) => `<option value="${esc(m)}" ${m === d.default_model ? 'selected' : ''}>${esc(m)}</option>`).join('');
}

export async function loadCronAndSessions() {
  const [c, s] = await Promise.all([api('/api/cron'), api('/api/sessions')]);
  const jobs = c.jobs || [];
  state.jobs = jobs;
  $('#cron-body').innerHTML = !jobs.length
    ? '<tr><td colspan="5" class="muted">No cron schedules.</td></tr>'
    : jobs.map((j) => `<tr><td class="mono">#${j.ID}</td><td class="mono">${j.ChatID}</td><td class="mono">${esc(j.Schedule)}</td><td>${esc(j.Prompt)}</td><td><button class="btn btn-sm btn-ghost" data-edit-cron="${j.ID}">Edit</button> <button class="btn btn-sm btn-ghost" data-toggle-cron="${j.ID}" data-cron-next="${!j.Enabled}">${j.Enabled ? 'Disable' : 'Enable'}</button> <button class="btn btn-sm btn-danger" data-del-cron="${j.ID}">Delete</button></td></tr>`).join('');
  const sess = s.sessions || [];
  $('#sessions-body').innerHTML = !sess.length
    ? '<tr><td colspan="5" class="muted">No active sessions.</td></tr>'
    : sess.map((x) => `<tr><td class="mono">${x.chat_id}</td><td class="mono">${esc(x.model || '-')}</td><td>${x.message_count}</td><td class="muted">${esc(x.last_at)}</td><td><button class="btn btn-sm btn-ghost" data-view-sess="${x.chat_id}">View</button> <button class="btn btn-sm btn-danger" data-clear-sess="${x.chat_id}">Reset</button></td></tr>`).join('');
}
