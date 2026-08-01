export function fmtTokens(n) {
  return (n || 0).toLocaleString();
}

export function fmtCost(cents) {
  return '$' + (cents || 0).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
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
