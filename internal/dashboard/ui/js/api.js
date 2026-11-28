export const state = {
  tab: 'overview',
  level: 'ALL',
  search: '',
  paused: false,
  logs: [],
  filteredLogs: [],
  skills: [],
  skillFilter: 'all',
  skillPage: 1,
  skillPageSize: 12,
  memTab: 'soul',
  memMeta: { soul: [0, 4000], memory: [0, 2200], user: [0, 1375] },
  cronOpen: false,
  theme: localStorage.getItem('renstra_theme') || 'light',
  token: localStorage.getItem('renstra_dash_token') || ''
};

export const $ = (s) => document.querySelector(s);
export const $$ = (s) => document.querySelectorAll(s);

export function toast(msg) {
  const el = $('#toast');
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(toast._t);
  toast._t = setTimeout(() => el.classList.remove('show'), 2500);
}

export async function api(path, opts = {}) {
  const headers = Object.assign({ 'Content-Type': 'application/json' }, opts.headers || {});
  if (state.token) headers['Authorization'] = 'Bearer ' + state.token;
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (res.status === 401) {
    $('#auth-modal').classList.add('open');
    $('#auth-modal').setAttribute('aria-hidden', 'false');
    setTimeout(() => $('#auth-token-input')?.focus(), 60);
    throw new Error('Unauthorized');
  }
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || 'Request failed');
  return data;
}

export function fmtTok(n) {
  if (!n) return '0';
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
  return String(n);
}

export function fmtNum(n) {
  return Number(n || 0).toLocaleString('en-US');
}

export function fmtDur(sec) {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  return `${h}h ${m}m ${s}s`;
}

export function fmtLocalTime(raw) {
  if (!raw) return '—';
  const s = String(raw).trim();
  const iso = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(s) ? s.replace(' ', 'T') + 'Z' : s;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return s;
  return d.toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}

export function fmtShortTime(raw) {
  if (!raw) return '—';
  const d = new Date(raw);
  if (!Number.isNaN(d.getTime())) {
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
  }
  const idx = String(raw).indexOf('T');
  return idx >= 0 ? String(raw).slice(idx + 1, idx + 9) : String(raw);
}

export function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

export function renderSafeMarkdown(raw) {
  const text = String(raw ?? '');
  const parts = text.split(/(```[\s\S]*?```)/g);
  return parts.map((seg) => {
    if (seg.startsWith('```') && seg.endsWith('```')) {
      const inner = seg.slice(3, -3).replace(/^[a-zA-Z0-9_-]*\n/, '');
      return `<pre><code>${esc(inner)}</code></pre>`;
    }
    let html = esc(seg);
    html = html.replace(/`([^`\n]+)`/g, '<code>$1</code>');
    html = html.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/\n/g, '<br>');
    return html;
  }).join('');
}

export function updateErrBadge(cnt) {
  const b = $('#nav-err-badge');
  b.textContent = cnt;
  b.classList.toggle('hidden', !cnt);
}
