export async function readJsonOrText(r) {
  const t = await r.text();
  try {
    return JSON.parse(t);
  } catch {
    return {
      error: t.includes('<html') || t.includes('Traceback') ? 'Internal Server Error' : t || r.statusText
    };
  }
}

export async function fetchJson(url, options = {}) {
  options.headers = options.headers || {};
  if (options.body && typeof options.body !== 'string' && !(options.body instanceof FormData)) {
    options.body = JSON.stringify(options.body);
    options.headers['Content-Type'] = 'application/json';
  }
  const r = await fetch(url, options);
  const data = await readJsonOrText(r);
  return { res: r, data };
}

// Wrapper for streaming
export function connectSSE(url, onMessage, onError) {
  try {
    const source = new EventSource(url);
    if (onMessage) source.onmessage = onMessage;
    if (onError) source.onerror = onError;
    return source;
  } catch (ex) {
    console.warn('SSE not available:', ex);
    return null;
  }
}
