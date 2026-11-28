import { state, $, api, fmtTok, fmtDur, esc, updateErrBadge } from './api.js?v=1.4.5';

export async function loadOverview() {
  const [ov, turnsData] = await Promise.all([api('/api/overview'), api('/api/turns?limit=10')]);
  $('#sb-version').textContent = ov.version || 'v1.3.0-beta';
  $('#sb-uptime').textContent = 'uptime ' + fmtDur(ov.uptime_sec || 0);
  if ($('#sb-ui-src')) {
    $('#sb-ui-src').textContent = `ui: ${ov.ui_source || 'embedded'}${ov.ui_version ? ' (' + ov.ui_version + ')' : ''}`;
  }
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

  const mUsage = ov.usage_by_model || [];
  if ($('#ov-model-usage-body')) {
    $('#ov-model-usage-body').innerHTML = !mUsage.length
      ? '<tr><td colspan="6" class="muted">No model token usage recorded yet.</td></tr>'
      : mUsage.map((m) => `<tr><td class="mono"><strong>${esc(m.model)}</strong></td><td class="mono">${m.requests}</td><td class="mono">${fmtTok(m.tokens_in)}</td><td class="mono">${fmtTok(m.tokens_out)}</td><td class="mono">${fmtTok((m.tokens_in || 0) + (m.tokens_out || 0))}</td><td class="mono">$${(m.cost_usd || 0).toFixed(4)}</td></tr>`).join('');
  }
}

export async function loadLogs() {
  const data = await api(`/api/logs?level=${encodeURIComponent(state.level)}&q=${encodeURIComponent(state.search)}&limit=250`);
  state.logs = data.entries || [];
  updateErrBadge(data.error_count || 0);
  renderLogs();
}

function shortTime(iso) {
  if (!iso) return '-';
  const idx = iso.indexOf('T');
  if (idx >= 0) return iso.slice(idx + 1, idx + 9);
  return iso;
}

function formatLogChips(attrs) {
  if (!attrs) return '';
  const chips = [];
  const skip = new Set(['tokens_in', 'tokens_out', 'context_used', 'context_window']);
  if (attrs.tokens_in !== undefined || attrs.tokens_out !== undefined) {
    chips.push(`<span class="attr-chip">${fmtTok(attrs.tokens_in || 0)} in · ${fmtTok(attrs.tokens_out || 0)} out</span>`);
  }
  if (attrs.context_used && attrs.context_window) {
    const pct = Math.round((Number(attrs.context_used) / Math.max(1, Number(attrs.context_window))) * 100);
    chips.push(`<span class="attr-chip">ctx ${fmtTok(attrs.context_used)}/${fmtTok(attrs.context_window)} (${pct}%)</span>`);
  }
  for (const [k, v] of Object.entries(attrs)) {
    if (skip.has(k) || v === '' || v === null || v === undefined) continue;
    if (k === 'duration_ms') {
      const ms = Number(v);
      const label = ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
      chips.push(`<span class="attr-chip">${label}</span>`);
      continue;
    }
    if (k === 'output_bytes') {
      const bytes = Number(v);
      const label = bytes >= 1024 ? `${(bytes / 1024).toFixed(1)}KB` : `${bytes}B`;
      chips.push(`<span class="attr-chip">${label}</span>`);
      continue;
    }
    if (k === 'cost_usd') {
      chips.push(`<span class="attr-chip">$${esc(String(v))}</span>`);
      continue;
    }
    if (k === 'ok') {
      chips.push(`<span class="badge ${v ? 'badge-ok' : 'badge-err'}">${v ? 'ok' : 'fail'}</span>`);
      continue;
    }
    if (k === 'status') {
      const cls = v === 'done' ? 'badge-ok' : v === 'error' ? 'badge-err' : 'badge-warn';
      chips.push(`<span class="badge ${cls}">${esc(v)}</span>`);
      continue;
    }
    let val = typeof v === 'string' ? v : JSON.stringify(v);
    if (val.length > 72) val = val.slice(0, 72) + '…';
    const isErr = k === 'error' || k === 'err';
    chips.push(`<span class="attr-chip ${isErr ? 'is-err' : ''}">${esc(k)}: ${esc(val)}</span>`);
  }
  return chips.length ? `<div class="log-attrs">${chips.join('')}</div>` : '';
}

export function renderLogs() {
  const box = $('#log-list');
  const rows = state.logs.filter((e) => {
    if (state.level !== 'ALL' && e.level !== state.level) return false;
    if (state.search && !JSON.stringify(e).toLowerCase().includes(state.search.toLowerCase())) return false;
    return true;
  });
  state.filteredLogs = rows;
  if ($('#log-count')) $('#log-count').textContent = `${rows.length} ${rows.length === 1 ? 'entry' : 'entries'}`;
  box.innerHTML = !rows.length
    ? '<div class="muted" style="padding:14px;">No matching log entries.</div>'
    : rows.map((e, idx) => {
        const cls = e.level === 'ERROR' ? 'badge-err' : e.level === 'WARN' ? 'badge-warn' : e.level === 'DEBUG' ? 'badge' : 'badge-info';
        const attrs = formatLogChips(e.attrs);
        return `<div class="log-row" data-log-idx="${idx}" style="cursor:pointer;" title="Click to inspect full log details"><span class="muted log-time" title="${esc(e.time)}">${esc(shortTime(e.time))}</span><span class="badge ${cls}">${esc(e.level)}</span><span class="log-msg">${esc(e.message)}</span>${attrs}</div>`;
      }).join('');
}

export async function loadSkills() {
  const [data, mcpData] = await Promise.all([api('/api/skills'), api('/api/mcp').catch(() => ({ servers: [] }))]);
  state.skills = data.skills || [];
  state.skillPage = 1;
  renderSkills();
  const mcpBox = $('#mcp-list');
  if (mcpBox) {
    const servers = mcpData.servers || [];
    mcpBox.innerHTML = !servers.length
      ? '<div class="muted">No MCP servers configured in mcp.yaml.</div>'
      : servers.map((s) => `<div class="list-item"><div><div class="mono"><strong>${esc(s.name)}</strong></div><div class="muted mono">${esc(s.url ? 'url: ' + s.url : s.command + ' ' + (s.args || []).join(' '))}</div></div><button class="btn btn-sm btn-danger" data-del-mcp="${esc(s.name)}">Delete</button></div>`).join('');
  }
}

export function renderSkills() {
  const q = ($('#skill-search').value || '').toLowerCase();
  const filtered = state.skills.filter((s) => {
    if (state.skillFilter === 'writable' && !s.writable) return false;
    if (state.skillFilter === 'readonly' && s.writable) return false;
    return s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q);
  });
  const totalPages = Math.max(1, Math.ceil(filtered.length / state.skillPageSize));
  if (state.skillPage > totalPages) state.skillPage = totalPages;
  if (state.skillPage < 1) state.skillPage = 1;
  const start = (state.skillPage - 1) * state.skillPageSize;
  const list = filtered.slice(start, start + state.skillPageSize);

  if ($('#skills-page-info')) {
    $('#skills-page-info').textContent = `Page ${state.skillPage} / ${totalPages} (${filtered.length} skills)`;
  }
  if ($('#btn-sk-prev')) $('#btn-sk-prev').disabled = state.skillPage <= 1;
  if ($('#btn-sk-next')) $('#btn-sk-next').disabled = state.skillPage >= totalPages;

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
  const q = $('#mem-fact-search')?.value || '';
  const [d, fData] = await Promise.all([
    api('/api/memory'),
    api(`/api/memory/facts?q=${encodeURIComponent(q)}`).catch(() => ({ facts: [] }))
  ]);
  $('#ed-soul').value = d.soul || '';
  $('#ed-memory').value = d.memory || '';
  $('#ed-user').value = d.user || '';
  $('#mem-soul-count').textContent = `${d.soul_chars}/${d.soul_limit}`;
  $('#mem-memory-count').textContent = `${d.memory_chars}/${d.memory_limit}`;
  $('#mem-user-count').textContent = `${d.user_chars}/${d.user_limit}`;
  if ($('#mem-limit-input')) $('#mem-limit-input').value = d.memory_limit || 2200;
  const fBox = $('#memory-facts-list');
  if (fBox) {
    const facts = fData.facts || [];
    fBox.innerHTML = !facts.length
      ? '<div class="muted">Belum ada smart episodic fact tersimpan.</div>'
      : facts.map((f) => `<div class="list-item"><div><div class="mono"><strong>#${f.id}</strong> <span class="badge badge-info">${esc(f.scope)}</span>${f.tags ? ` <span class="badge">${esc(f.tags)}</span>` : ''}</div><div style="white-space:pre-wrap;word-break:break-word;margin-top:4px;">${esc(f.content)}</div></div><button class="btn btn-sm btn-danger" data-del-fact="${f.id}">Delete</button></div>`).join('');
  }
}

export async function loadAccounts() {
  const [d, ov] = await Promise.all([api('/api/accounts'), api('/api/overview').catch(() => ({}))]);
  state.accounts = d.accounts || [];
  state.modelDetails = d.model_details || [];
  if ($('#fb-list-input')) $('#fb-list-input').value = (ov.fallbacks_config || []).join(', ');
  if ($('#fb-smart-chk')) $('#fb-smart-chk').checked = Boolean(ov.smart_fallback);
  const box = $('#accounts-list');
  box.innerHTML = !state.accounts.length
    ? '<div class="muted">No provider accounts configured.</div>'
    : state.accounts.map((a) => `
      <div class="list-item">
        <div>
          <div><strong class="mono">${esc(a.Name)}</strong> <span class="badge badge-info">${esc(a.Kind)}</span></div>
          <div class="muted mono">model: ${esc(a.DefaultModel || '-')} • key: ${esc(a.MaskedKey)}${a.BaseURL ? ' • ' + esc(a.BaseURL) : ''}</div>
        </div>
        <div style="display:flex;gap:6px;">
          <button class="btn btn-sm btn-ghost" data-edit-account="${esc(a.Name)}">Edit</button>
          <button class="btn btn-sm btn-danger" data-del-account="${esc(a.Name)}">Delete</button>
        </div>
      </div>`).join('');
  const pList = $('#provider-names-list');
  if (pList) {
    pList.innerHTML = (d.accounts || []).map((a) => `<option value="${esc(a.Name)}">${esc(a.Kind)}</option>`).join('');
  }
  const sel = $('#sel-default-model');
  const opts = d.model_options || (d.models || []).map((m) => ({ ref: m, label: m }));
  sel.innerHTML = opts.map((o) => `<option value="${esc(o.ref)}" ${o.ref === d.default_model ? 'selected' : ''}>${esc(o.label)}</option>`).join('');
  const detailsBox = $('#models-detail-list');
  if (detailsBox) {
    const details = d.model_details || [];
    if (!details.length) {
      detailsBox.innerHTML = '<div class="muted">No models configured.</div>';
      return;
    }
    const groups = new Map();
    for (const m of details) {
      const p = m.provider || 'default';
      if (!groups.has(p)) groups.set(p, []);
      groups.get(p).push(m);
    }
    detailsBox.innerHTML = Array.from(groups.entries()).map(([prov, items]) => `
      <div class="list-item" style="flex-direction:column;align-items:stretch;gap:8px;">
        <div class="card-head" style="margin:0;">
          <strong class="mono">${esc(prov)}</strong>
          <span class="badge badge-info">${items.length} model${items.length > 1 ? 's' : ''}</span>
        </div>
        ${items.map((m) => {
          const isActive = m.ref === d.default_model || (m.alias && m.alias === d.default_model);
          const useTarget = m.alias || m.ref;
          return `
          <div style="display:flex;justify-content:space-between;align-items:center;gap:8px;flex-wrap:wrap;padding-top:4px;border-top:1px solid var(--border);">
            <div>
              <div class="mono"><strong>${esc(m.model)}</strong>${m.alias ? ` <span class="badge badge-info">alias: ${esc(m.alias)}</span>` : ''}${isActive ? ' <span class="badge badge-ok">active</span>' : ''}</div>
              <div class="muted mono">ctx ${fmtTok(m.context)} · max_out ${fmtTok(m.max_output || 4096)}${m.vision ? ' · vision' : ''}</div>
            </div>
            <div style="display:flex;gap:6px;">
              ${!isActive ? `<button class="btn btn-sm btn-ghost" data-use-model="${esc(useTarget)}">Use</button>` : ''}
              <button class="btn btn-sm btn-ghost" data-edit-model="${esc(m.ref)}">Edit</button>
              <button class="btn btn-sm btn-danger" data-del-model="${esc(useTarget)}">Delete</button>
            </div>
          </div>`;
        }).join('')}
      </div>`).join('');
  }
}

export async function loadCronAndSessions() {
  const [c, s] = await Promise.all([api('/api/cron'), api('/api/sessions')]);
  const jobs = c.jobs || [];
  state.jobs = jobs;
  if ($('#cron-count-badge')) $('#cron-count-badge').textContent = String(jobs.length);
  if ($('#cron-section')) $('#cron-section').open = jobs.length > 0;
  $('#cron-body').innerHTML = !jobs.length
    ? '<tr><td colspan="5" class="muted">No cron schedules.</td></tr>'
    : jobs.map((j) => `<tr><td class="mono">#${j.ID}</td><td class="mono">${j.ChatID}</td><td class="mono">${esc(j.Schedule)}</td><td>${esc(j.Prompt)}</td><td><button class="btn btn-sm btn-ghost" data-edit-cron="${j.ID}">Edit</button> <button class="btn btn-sm btn-ghost" data-toggle-cron="${j.ID}" data-cron-next="${!j.Enabled}">${j.Enabled ? 'Disable' : 'Enable'}</button> <button class="btn btn-sm btn-danger" data-del-cron="${j.ID}">Delete</button></td></tr>`).join('');
  const sess = s.sessions || [];
  $('#sessions-body').innerHTML = !sess.length
    ? '<tr><td colspan="5" class="muted">No active sessions.</td></tr>'
    : sess.map((x) => `<tr><td class="mono">${x.chat_id}</td><td class="mono">${esc(x.model || '-')}</td><td>${x.message_count}</td><td class="muted">${esc(x.last_at)}</td><td><button class="btn btn-sm btn-ghost" data-open-chat="${x.chat_id}">Chat</button> <button class="btn btn-sm btn-ghost" data-view-sess="${x.chat_id}">View</button> <button class="btn btn-sm btn-danger" data-clear-sess="${x.chat_id}">Reset</button></td></tr>`).join('');
}

export async function loadChatPlayground() {
  const cid = Number($('#chat-pg-id')?.value || 1) || 1;
  const box = $('#chat-pg-messages');
  if (!box) return;
  const [data, acc, sessData] = await Promise.all([
    api(`/api/sessions/messages?chat_id=${encodeURIComponent(cid)}`),
    api('/api/accounts').catch(() => ({})),
    api('/api/sessions').catch(() => ({ sessions: [] }))
  ]);
  const sel = $('#chat-pg-model');
  if (sel) {
    const prev = sel.value;
    const opts = acc.model_options || (acc.models || []).map((m) => ({ ref: m, label: m }));
    const curSess = (sessData.sessions || []).find((x) => Number(x.chat_id) === cid);
    const preferred = prev || curSess?.model || acc.default_model || '';
    sel.innerHTML = `<option value="">(Default: ${esc(acc.default_model || 'active')})</option>` +
      opts.map((o) => `<option value="${esc(o.ref)}" ${o.ref === preferred ? 'selected' : ''}>${esc(o.label)}</option>`).join('');
  }
  const msgs = (data.messages || []).filter((m) => (m.Role || m.role) === 'user' || (m.Role || m.role) === 'assistant');
  box.innerHTML = !msgs.length
    ? '<div class="muted" style="padding:12px;">Belum ada pesan di sesi ini. Kirim pesan di bawah untuk menguji agent.</div>'
    : msgs.map((m) => {
        const role = (m.Role || m.role || 'user').toUpperCase();
        const isUser = role === 'USER';
        return `<div class="list-item" style="flex-direction:column;align-items:stretch;border-left:3px solid ${isUser ? 'var(--accent)' : 'var(--green)'};">
          <div class="mono muted" style="font-size:11px;">${esc(role)}</div>
          <div style="white-space:pre-wrap;word-break:break-word;">${esc(m.Content || m.content || '')}</div>
        </div>`;
      }).join('');
  box.scrollTop = box.scrollHeight;
}
