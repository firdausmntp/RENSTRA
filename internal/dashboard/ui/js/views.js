import {
  state, $, api, fmtTok, fmtNum, fmtDur, fmtLocalTime, fmtShortTime, esc, renderSafeMarkdown, updateErrBadge
} from './api.js?v=1.4.6';

export async function loadOverview() {
  const [ov, turnsData] = await Promise.all([api('/api/overview'), api('/api/turns?limit=10')]);
  $('#sb-version').textContent = ov.version || 'v1.4.5';
  $('#sb-uptime').textContent = 'uptime ' + fmtDur(ov.uptime_sec || 0);
  if ($('#sb-ui-src')) {
    $('#sb-ui-src').textContent = `ui: ${ov.ui_source || 'embedded'}${ov.ui_version ? ' (' + ov.ui_version + ')' : ''}`;
  }
  $('#top-model').textContent = 'model: ' + (ov.active_model || '—');
  $('#ov-model').textContent = ov.active_model || '—';
  $('#ov-provider').textContent = `${ov.provider || '—'} · ctx ${fmtTok(ov.context_window)}`;

  const todayIn = ov.usage_today?.TokensIn || 0;
  const todayOut = ov.usage_today?.TokensOut || 0;
  const todayCost = ov.usage_today?.CostUSD || 0;
  $('#ov-cost-today').textContent = todayCost > 0 ? '$' + todayCost.toFixed(4) : fmtTok(todayIn + todayOut) + ' tok';
  $('#ov-tok-today').textContent = todayCost > 0
    ? `↑${fmtTok(todayIn)} ↓${fmtTok(todayOut)}`
    : `↑${fmtTok(todayIn)} ↓${fmtTok(todayOut)} · $0.0000`;

  const mIn = ov.usage_30d?.TokensIn || 0;
  const mOut = ov.usage_30d?.TokensOut || 0;
  const mCost = ov.usage_30d?.CostUSD || 0;
  $('#ov-cost-30d').textContent = mCost > 0 ? '$' + mCost.toFixed(4) : fmtTok(mIn + mOut) + ' tok';
  $('#ov-tok-30d').textContent = mCost > 0
    ? `↑${fmtTok(mIn)} ↓${fmtTok(mOut)}`
    : `↑${fmtTok(mIn)} ↓${fmtTok(mOut)} · $0.0000`;

  const errCnt = ov.error_count || 0;
  const warnCnt = ov.warn_count || 0;
  $('#ov-health').innerHTML = `<span class="${errCnt ? 'text-red' : 'text-green'}">${errCnt} err</span> <span class="muted">·</span> <span class="${warnCnt ? 'text-amber' : 'muted'}">${warnCnt} warn</span>`;
  $('#ov-skills').textContent = `${ov.skills_count || 0} skills loaded`;
  $('#btn-logout')?.classList.toggle('hidden', !ov.auth_required);
  updateErrBadge(errCnt);

  // Fill complete 14-day calendar window so missing days don't skip visually
  const chart = $('#daily-chart');
  const rawDays = ov.daily_usage || [];
  const byDay = new Map(rawDays.map((d) => [d.day, d]));
  const filled = [];
  const now = new Date();
  for (let i = 13; i >= 0; i--) {
    const dt = new Date(now.getFullYear(), now.getMonth(), now.getDate() - i);
    const key = `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, '0')}-${String(dt.getDate()).padStart(2, '0')}`;
    const rec = byDay.get(key) || { day: key, tokens_in: 0, tokens_out: 0, cost_usd: 0 };
    filled.push(rec);
  }
  const maxTot = Math.max(...filled.map((d) => d.tokens_in + d.tokens_out), 0);
  if ($('#daily-chart-peak')) {
    $('#daily-chart-peak').textContent = maxTot > 0 ? `peak ${fmtTok(maxTot)}/day` : 'no activity';
  }
  if (maxTot === 0) {
    chart.innerHTML = '<div class="muted" style="margin:auto;">No token throughput recorded in the last 14 days.</div>';
  } else {
    chart.innerHTML = filled.map((d) => {
      const tot = d.tokens_in + d.tokens_out;
      const pct = tot > 0 ? Math.max(5, Math.round((tot / maxTot) * 100)) : 2;
      const tip = `${d.day}: ${fmtTok(tot)} tok (↑${fmtTok(d.tokens_in)} ↓${fmtTok(d.tokens_out)}, $${(d.cost_usd || 0).toFixed(4)})`;
      return `<div class="bar-col" title="${esc(tip)}"><div class="bar ${tot === 0 ? 'is-zero' : ''}" style="height:${pct}%"></div><div class="bar-lbl">${d.day.slice(8)}</div></div>`;
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
        return `<div class="list-item"><div style="min-width:0;flex:1;"><div class="mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">${esc(f.provider)}:${esc(f.model)}</div><div class="muted">${esc(f.last_error || 'Ready')}</div></div>${st}</div>`;
      }).join('');

  const turns = turnsData.turns || [];
  $('#ov-turns-body').innerHTML = !turns.length
    ? '<tr><td colspan="6" class="muted">No turns recorded yet.</td></tr>'
    : turns.map((t) => {
        const cls = t.status === 'error' ? 'badge-err' : t.status === 'done' ? 'badge-ok' : 'badge-warn';
        const hasErr = Boolean(t.error && String(t.error).trim());
        const errCell = hasErr
          ? `<td class="col-clip text-red mono" title="${esc(t.error)}">${esc(t.error)}</td>`
          : `<td class="muted">—</td>`;
        return `<tr><td class="mono">#${t.id}</td><td class="mono">${t.chat_id}</td><td><span class="badge ${cls}">${esc(t.status)}</span></td><td class="muted mono">${esc(fmtLocalTime(t.started_at))}</td>${errCell}<td class="col-actions"><button class="btn btn-sm btn-danger" data-del-turn="${t.id}" type="button">Delete</button></td></tr>`;
      }).join('');

  const mUsage = ov.usage_by_model || [];
  if ($('#ov-model-usage-body')) {
    $('#ov-model-usage-body').innerHTML = !mUsage.length
      ? '<tr><td colspan="6" class="muted">No per-model token usage recorded yet.</td></tr>'
      : mUsage.map((m) => `<tr><td class="mono"><strong>${esc(m.model)}</strong></td><td class="mono">${fmtNum(m.requests)}</td><td class="mono">${fmtTok(m.tokens_in)}</td><td class="mono">${fmtTok(m.tokens_out)}</td><td class="mono">${fmtTok((m.tokens_in || 0) + (m.tokens_out || 0))}</td><td class="mono">$${(m.cost_usd || 0).toFixed(4)}</td></tr>`).join('');
  }
}

export async function loadLogs() {
  const data = await api(`/api/logs?level=${encodeURIComponent(state.level)}&q=${encodeURIComponent(state.search)}&limit=250`);
  state.logs = data.entries || [];
  updateErrBadge(data.error_count || 0);
  renderLogs();
}

export function logMatchesFilter(e) {
  if (state.level !== 'ALL' && e.level !== state.level) return false;
  if (state.search) {
    const q = state.search.toLowerCase();
    const msg = (e.message || '').toLowerCase();
    if (msg.includes(q)) return true;
    const attrs = e.attrs;
    if (!attrs) return false;
    for (const [k, v] of Object.entries(attrs)) {
      if (k.toLowerCase().includes(q) || String(v ?? '').toLowerCase().includes(q)) return true;
    }
    return false;
  }
  return true;
}

export function renderLogRowHTML(e, logId) {
  const lvl = e.level || 'INFO';
  const cls = lvl === 'ERROR' ? 'badge-err' : lvl === 'WARN' ? 'badge-warn' : lvl === 'DEBUG' ? 'badge' : 'badge-info';
  const a = e.attrs || {};

  // Build concise inline context (model / tool / status badge)
  const ctxParts = [];
  if (a.tool) ctxParts.push(`<span class="mono log-ctx-name">${esc(a.tool)}</span>`);
  else if (a.model) ctxParts.push(`<span class="mono log-ctx-name">${esc(a.model)}</span>`);
  else if (a.addr) ctxParts.push(`<span class="mono log-ctx-name">${esc(a.addr)}</span>`);
  if (a.status) {
    const sCls = a.status === 'done' ? 'badge-ok' : a.status === 'error' ? 'badge-err' : 'badge-warn';
    ctxParts.push(`<span class="badge ${sCls}">${esc(a.status)}</span>`);
  } else if (a.ok === false) {
    ctxParts.push('<span class="badge badge-err">fail</span>');
  } else if (a.ok === true) {
    ctxParts.push('<span class="badge badge-ok">ok</span>');
  }
  if (a.tools) ctxParts.push(`<span class="badge">${esc(a.tools)}</span>`);

  // Build right-aligned clean tabular metrics (skip zero-value noise)
  const met = [];
  if (a.tokens_in || a.tokens_out) met.push(`${fmtTok(a.tokens_in || 0)}↑ ${fmtTok(a.tokens_out || 0)}↓`);
  if (a.context_used && a.context_window) {
    const pct = Math.round((Number(a.context_used) / Math.max(1, Number(a.context_window))) * 100);
    met.push(`ctx ${pct}%`);
  }
  if (a.duration_ms !== undefined && a.duration_ms !== null) {
    const ms = Number(a.duration_ms);
    met.push(ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`);
  }
  if (a.output_bytes) {
    const b = Number(a.output_bytes);
    met.push(b >= 1024 ? `${(b / 1024).toFixed(1)}KB` : `${b}B`);
  }
  if (a.cost_usd && Number(a.cost_usd) > 0) met.push(`$${a.cost_usd}`);
  if (a.iter) met.push(`iter ${a.iter}`);
  else if (a.iterations && Number(a.iterations) > 1) met.push(`${a.iterations} iters`);
  if (a.masked_tools) met.push(`${a.masked_tools} masked`);
  if (a.turn_id) met.push(`#${a.turn_id}`);
  else if (a.chat_id) met.push(`chat ${a.chat_id}`);

  // Meaningful readable subtitle line (error > arg+output > prompt/text preview)
  let subText = '';
  let subErr = false;
  if (a.error || a.err) {
    subText = String(a.error || a.err);
    subErr = true;
  } else if (a.arg && a.output_preview) {
    subText = `${a.arg}  →  ${a.output_preview}`;
  } else if (a.arg) {
    subText = String(a.arg);
  } else if (a.prompt_preview) {
    subText = `Prompt: ${a.prompt_preview}`;
  } else if (a.text_preview) {
    subText = String(a.text_preview);
  } else if (a.output_preview) {
    subText = String(a.output_preview);
  } else if (a.reason) {
    subText = String(a.reason);
  } else if (a.version && a.home) {
    subText = `${a.version} · ${a.home}`;
  }

  return `<div class="log-row" role="listitem" tabindex="0" data-log-id="${esc(logId)}">
    <div class="log-main">
      <span class="mono log-time" title="${esc(e.time)}">${esc(fmtShortTime(e.time))}</span>
      <span class="badge badge-lvl ${cls}">${esc(lvl)}</span>
      <span class="mono log-msg">${esc(e.message)}</span>
      <div class="log-ctx">${ctxParts.join('')}</div>
      <div class="log-metrics mono">${esc(met.join(' · '))}</div>
    </div>
    ${subText ? `<div class="log-sub mono ${subErr ? 'is-err' : ''}">${esc(subText)}</div>` : ''}
  </div>`;
}

export function renderLogs() {
  const box = $('#log-list');
  const rows = state.logs.filter(logMatchesFilter);
  state.filteredLogs = rows;
  if ($('#log-count')) $('#log-count').textContent = `${rows.length} ${rows.length === 1 ? 'entry' : 'entries'}`;
  box.innerHTML = !rows.length
    ? '<div class="muted" style="padding:18px;text-align:center;">No matching log entries.</div>'
    : rows.map((e, idx) => renderLogRowHTML(e, e.id ?? `i-${idx}`)).join('');
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
      : servers.map((s) => `<div class="list-item"><div style="min-width:0;flex:1;"><div class="mono"><strong>${esc(s.name)}</strong></div><div class="muted mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">${esc(s.url ? 'url: ' + s.url : s.command + ' ' + (s.args || []).join(' '))}</div></div><button class="btn btn-sm btn-danger" data-del-mcp="${esc(s.name)}" type="button">Delete</button></div>`).join('');
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
        <div class="card-head" style="margin-bottom:0;">
          <strong class="mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">${esc(s.name)}</strong>
          <span class="badge ${s.writable ? 'badge-ok' : 'badge-info'}">${s.writable ? 'Writable' : 'Read-Only'}</span>
        </div>
        <div class="skill-desc" title="${esc(s.description)}">${esc(s.description)}</div>
        <div class="card-actions">
          <button class="btn btn-sm btn-ghost" data-edit-skill="${esc(s.name)}" type="button">Edit</button>
          ${s.writable ? `<button class="btn btn-sm btn-danger" data-del-skill="${esc(s.name)}" type="button">Delete</button>` : ''}
        </div>
      </div>`).join('');
}

export function updateMemoryQuotaUI() {
  const tab = state.memTab || 'soul';
  const [cur, lim] = state.memMeta[tab] || [0, 1];
  const pct = lim > 0 ? Math.min(100, Math.round((cur / lim) * 100)) : 0;
  const badge = $('#mem-active-count');
  if (badge) {
    badge.textContent = `${fmtNum(cur)} / ${fmtNum(lim)} chars (${pct}%)`;
    badge.className = 'mono badge ' + (pct >= 92 ? 'badge-err' : pct >= 80 ? 'badge-warn' : 'badge-info');
  }
  const fill = $('#mem-quota-fill');
  if (fill) {
    fill.style.width = `${pct}%`;
    fill.className = 'quota-fill' + (pct >= 92 ? ' is-danger' : pct >= 80 ? ' is-warn' : '');
  }
  const inlineQuota = $('#mem-quota-inline');
  if (inlineQuota) inlineQuota.classList.toggle('hidden', tab !== 'memory');
  ['soul', 'memory', 'user'].forEach((k) => {
    $('#mem-pane-' + k)?.classList.toggle('hidden', k !== tab);
  });
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
  state.memMeta = {
    soul: [d.soul_chars || 0, d.soul_limit || 4000],
    memory: [d.memory_chars || 0, d.memory_limit || 2200],
    user: [d.user_chars || 0, d.user_limit || 1375]
  };
  if ($('#mem-limit-input')) $('#mem-limit-input').value = d.memory_limit || 2200;
  updateMemoryQuotaUI();

  const fBox = $('#memory-facts-list');
  if (fBox) {
    const facts = fData.facts || [];
    fBox.innerHTML = !facts.length
      ? '<div class="muted">No episodic memory facts stored yet.</div>'
      : facts.map((f) => `<div class="list-item"><div style="min-width:0;flex:1;"><div class="mono"><strong>#${f.id}</strong> <span class="badge badge-info">${esc(f.scope)}</span>${f.tags ? ` <span class="badge">${esc(f.tags)}</span>` : ''}</div><div style="white-space:pre-wrap;word-break:break-word;margin-top:4px;">${esc(f.content)}</div></div><button class="btn btn-sm btn-danger" data-del-fact="${f.id}" type="button">Delete</button></div>`).join('');
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
        <div style="min-width:0;flex:1;">
          <div style="display:flex;align-items:center;gap:6px;"><strong class="mono">${esc(a.Name)}</strong>${a.Kind && a.Kind !== a.Name ? `<span class="badge badge-info">${esc(a.Kind)}</span>` : ''}</div>
          <div class="muted mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">model: ${esc(a.DefaultModel || '—')} · key: ${esc(a.MaskedKey)}${a.BaseURL ? ' · ' + esc(a.BaseURL) : ''}</div>
        </div>
        <div style="display:flex;gap:6px;flex-shrink:0;">
          <button class="btn btn-sm btn-ghost" data-edit-account="${esc(a.Name)}" type="button">Edit</button>
          <button class="btn btn-sm btn-danger" data-del-account="${esc(a.Name)}" type="button">Delete</button>
        </div>
      </div>`).join('');

  const pList = $('#provider-names-list');
  if (pList) {
    pList.innerHTML = state.accounts.map((a) => `<option value="${esc(a.Name)}">${esc(a.Kind)}</option>`).join('');
  }

  // Deduplicate dropdown options when alias target already matches a ref
  const rawOpts = d.model_options || (d.models || []).map((m) => ({ ref: m, label: m }));
  const aliasTargets = new Set();
  for (const m of state.modelDetails) {
    if (m.alias && m.ref) aliasTargets.add(m.ref);
  }
  const cleanOpts = rawOpts.filter((o) => !(aliasTargets.has(o.ref) && o.label === o.ref));
  const sel = $('#sel-default-model');
  sel.innerHTML = cleanOpts.map((o) => `<option value="${esc(o.ref)}" ${o.ref === d.default_model ? 'selected' : ''}>${esc(o.label)}</option>`).join('');

  const detailsBox = $('#models-detail-list');
  if (detailsBox) {
    const details = state.modelDetails;
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
      <div class="model-group">
        <div class="card-head" style="margin-bottom:0;">
          <strong class="mono">${esc(prov)}</strong>
          <span class="badge badge-info">${items.length} model${items.length > 1 ? 's' : ''}</span>
        </div>
        ${items.map((m) => {
          const isActive = m.ref === d.default_model || (m.alias && m.alias === d.default_model);
          const useTarget = m.alias || m.ref;
          const showAlias = m.alias && m.alias !== m.model;
          return `
          <div class="model-row">
            <div class="model-info">
              <div class="model-name mono" title="${esc(m.ref)}">
                <strong>${esc(m.model)}</strong>
                ${showAlias ? `<span class="badge badge-info">${esc(m.alias)}</span>` : ''}
                ${isActive ? '<span class="badge badge-ok">active</span>' : ''}
              </div>
              <div class="muted mono">ctx ${fmtTok(m.context)} · max_out ${fmtTok(m.max_output || 4096)}${m.vision ? ' · vision' : ''}</div>
            </div>
            <div class="model-actions">
              ${!isActive ? `<button class="btn btn-sm btn-ghost" data-use-model="${esc(useTarget)}" type="button">Use</button>` : ''}
              <button class="btn btn-sm btn-ghost" data-edit-model="${esc(m.ref)}" type="button">Edit</button>
              <button class="btn btn-sm btn-danger" data-del-model="${esc(useTarget)}" type="button">Delete</button>
            </div>
          </div>`;
        }).join('')}
      </div>`).join('');
  }
}

export function setCronPanelOpen(open) {
  state.cronOpen = Boolean(open);
  $('#cron-panel-body')?.classList.toggle('hidden', !state.cronOpen);
  $('#btn-cron-toggle')?.setAttribute('aria-expanded', state.cronOpen ? 'true' : 'false');
  if ($('#cron-chevron')) $('#cron-chevron').textContent = state.cronOpen ? '▾' : '▸';
}

export async function loadCronAndSessions() {
  const [c, s] = await Promise.all([api('/api/cron'), api('/api/sessions')]);
  const jobs = c.jobs || [];
  state.jobs = jobs;
  if ($('#cron-count-badge')) $('#cron-count-badge').textContent = String(jobs.length);
  setCronPanelOpen(jobs.length > 0);
  $('#cron-body').innerHTML = !jobs.length
    ? '<tr><td colspan="6" class="muted">No cron schedules configured.</td></tr>'
    : jobs.map((j) => `<tr>
        <td class="mono">#${j.ID}</td>
        <td><span class="badge ${j.Enabled ? 'badge-ok' : 'badge-warn'}">${j.Enabled ? 'Active' : 'Paused'}</span></td>
        <td class="mono">${j.ChatID}</td>
        <td class="mono">${esc(j.Schedule)}</td>
        <td class="col-clip" title="${esc(j.Prompt)}">${esc(j.Prompt)}</td>
        <td class="col-actions">
          <button class="btn btn-sm btn-ghost" data-edit-cron="${j.ID}" type="button">Edit</button>
          <button class="btn btn-sm btn-ghost" data-toggle-cron="${j.ID}" data-cron-next="${!j.Enabled}" type="button">${j.Enabled ? 'Pause' : 'Enable'}</button>
          <button class="btn btn-sm btn-danger" data-del-cron="${j.ID}" type="button">Delete</button>
        </td>
      </tr>`).join('');

  const sess = s.sessions || [];
  $('#sessions-body').innerHTML = !sess.length
    ? '<tr><td colspan="5" class="muted">No active sessions.</td></tr>'
    : sess.map((x) => {
        const isGroup = Number(x.chat_id) < 0;
        const kindBadge = isGroup ? '<span class="badge badge-info">Group</span>' : '<span class="badge">DM</span>';
        return `<tr>
          <td class="mono">${kindBadge} ${x.chat_id}</td>
          <td class="mono">${esc(x.model || '—')}</td>
          <td class="mono">${fmtNum(x.message_count)}</td>
          <td class="muted mono">${esc(fmtLocalTime(x.last_at))}</td>
          <td class="col-actions">
            <button class="btn btn-sm btn-ghost" data-open-chat="${x.chat_id}" type="button">Chat</button>
            <button class="btn btn-sm btn-ghost" data-view-sess="${x.chat_id}" type="button">View</button>
            <button class="btn btn-sm btn-danger" data-clear-sess="${x.chat_id}" type="button">Reset</button>
          </td>
        </tr>`;
      }).join('');
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
    sel.innerHTML = `<option value="">Default (${esc(acc.default_model || 'active')})</option>` +
      opts.map((o) => `<option value="${esc(o.ref)}" ${o.ref === preferred ? 'selected' : ''}>${esc(o.label)}</option>`).join('');
  }
  const msgs = (data.messages || []).filter((m) => (m.Role || m.role) === 'user' || (m.Role || m.role) === 'assistant');
  box.innerHTML = !msgs.length
    ? '<div class="muted" style="margin:auto;text-align:center;">No messages in this session yet. Send a prompt below to test the agent.</div>'
    : msgs.map((m) => {
        const role = (m.Role || m.role || 'user').toLowerCase();
        const isUser = role === 'user';
        const content = m.Content || m.content || '';
        return `<div class="chat-bubble ${isUser ? 'is-user' : 'is-assistant'}">
          <div class="chat-bubble-head mono">${isUser ? 'User' : 'Assistant'}</div>
          <div>${isUser ? esc(content).replace(/\n/g, '<br>') : renderSafeMarkdown(content)}</div>
        </div>`;
      }).join('');
  box.scrollTop = box.scrollHeight;
}
