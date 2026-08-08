const _tokenCache = new Map();
export function fmtTokens(n) {
  if (!n) return '0';
  if (_tokenCache.has(n)) return _tokenCache.get(n);
  const res = String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  _tokenCache.set(n, res);
  return res;
}

const _costCache = new Map();
export function fmtCost(cents) {
  if (_costCache.has(cents)) return _costCache.get(cents);
  const val = cents || 0;
  const dollars = Math.floor(val / 100);
  const frac = Math.round(val % 100).toString().padStart(2, '0');
  const res = '$' + String(dollars).replace(/\B(?=(\d{3})+(?!\d))/g, ",") + '.' + frac;
  _costCache.set(cents, res);
  return res;
}

export function timeAgo(dateString) {
  if (!dateString) return "just now";
  let dStr = dateString;
  if (/^\d{2}:\d{2}:\d{2}$/.test(dateString)) {
    const today = new Date().toISOString().split('T')[0];
    dStr = `${today}T${dateString}`;
  }
  const date = new Date(dStr);
  const seconds = Math.floor((new Date() - date) / 1000);
  if (isNaN(seconds) || seconds < 0 || seconds > 86400) return dateString;
  if (seconds < 60) return `just now`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
}

export function formatRunDuration(ms) {
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return rest ? `${minutes}m ${rest}s` : `${minutes}m`;
}
