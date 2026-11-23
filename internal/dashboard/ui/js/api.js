export const state = {
  tab: 'overview',
  level: 'ALL',
  search: '',
  paused: false,
  logs: [],
  skills: [],
  skillFilter: 'all',
  skillPage: 1,
  skillPageSize: 12,
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

export function fmtDur(sec) {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  return `${h}h ${m}m ${s}s`;
}

export function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

export function updateErrBadge(cnt) {
  const b = $('#nav-err-badge');
  b.textContent = cnt;
  b.classList.toggle('hidden', !cnt);
}
