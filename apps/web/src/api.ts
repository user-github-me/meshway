export type Peer = { id: string; addresses: string[] };
export type Status = {
  version: string; peer_id: string; addresses: string[]; peers: Peer[];
  roles: { relay: boolean; exit: boolean }; socks_listen: string; api_listen: string;
  upstream_peer: string; upstream_connected?: boolean; started_at: string; bytes_sent: number; bytes_received: number;
  vpn?: { active?: boolean; peer_id?: string; mode?: string; device_attached?: boolean } | null;
  configuration_summary?: { socks_enabled: boolean; personal_server: boolean; forwards: { name: string; listen: string; service?: string }[] };
  transport?: { kind: string; configured: boolean; running: boolean; ready: boolean; authenticated: boolean; local_address?: string; peer_id?: string; error?: string };
};

// Credentials belong to a local process. Never send them to arbitrary or remote URLs.
export function localEndpoint(input: string): string {
  let url: URL;
  try { url = new URL(input); } catch { throw new Error('Enter a valid loopback API address.'); }
  if (url.protocol !== 'http:' || !['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
    || url.username || url.password || url.search || url.hash || (url.pathname !== '/' && url.pathname !== '')) {
    throw new Error('Use an HTTP loopback address, such as http://127.0.0.1:7777.');
  }
  return url.origin;
}

export function parseStatus(value: unknown): Status {
  if (!value || typeof value !== 'object') throw new Error('Unexpected response from the local daemon.');
  const s = value as Record<string, unknown>;
  if (typeof s.peer_id !== 'string' || !s.peer_id || !Array.isArray(s.addresses) || !s.addresses.every(x => typeof x === 'string')
    || !Array.isArray(s.peers) || !s.peers.every(p => p && typeof p === 'object' && typeof p.id === 'string' && Array.isArray(p.addresses) && p.addresses.every((a: unknown) => typeof a === 'string'))
    || !s.roles || typeof s.roles !== 'object' || typeof (s.roles as Record<string, unknown>).relay !== 'boolean' || typeof (s.roles as Record<string, unknown>).exit !== 'boolean'
    || ['version', 'socks_listen', 'api_listen', 'upstream_peer', 'started_at'].some(k => typeof s[k] !== 'string')
    || (s.upstream_connected !== undefined && typeof s.upstream_connected !== 'boolean')
    || ['bytes_sent', 'bytes_received'].some(k => typeof s[k] !== 'number' || !Number.isFinite(s[k]) || (s[k] as number) < 0)) {
    throw new Error('The daemon status does not match the supported API.');
  }
  if (s.vpn !== undefined && s.vpn !== null) {
    if (typeof s.vpn !== 'object' || (s.vpn as Record<string, unknown>).active !== undefined && typeof (s.vpn as Record<string, unknown>).active !== 'boolean') throw new Error('The VPN status does not match the supported API.');
  }
  if (s.configuration_summary !== undefined) {
    const c = s.configuration_summary as Record<string, unknown>;
    if (!c || typeof c !== 'object' || typeof c.socks_enabled !== 'boolean' || typeof c.personal_server !== 'boolean' || !Array.isArray(c.forwards) || !c.forwards.every(f => f && typeof f.name === 'string' && typeof f.listen === 'string' && (f.service === undefined || typeof f.service === 'string'))) throw new Error('The node configuration summary is invalid.');
  }
  if (s.transport !== undefined && s.transport !== null) {
    const t = s.transport as Record<string, unknown>;
    if (typeof t !== 'object' || typeof t.kind !== 'string' || ['configured', 'running', 'ready', 'authenticated'].some(k => typeof t[k] !== 'boolean') || ['local_address', 'peer_id', 'error'].some(k => t[k] !== undefined && typeof t[k] !== 'string')) throw new Error('The personal transport status is invalid.');
  }
  return s as unknown as Status;
}

export async function apiRequest(base: string, token: string, path: string, body?: unknown, method?: 'GET' | 'POST' | 'DELETE'): Promise<unknown> {
  const safeBase = localEndpoint(base);
  if (!token.trim()) throw new Error('Enter the API token from your local configuration.');
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), body === undefined ? 8_000 : 18_000);
  try {
    const response = await fetch(`${safeBase}${path}`, {
      method: method ?? (body === undefined ? 'GET' : 'POST'),
      headers: { Authorization: `Bearer ${token.trim()}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal,
      credentials: 'omit', redirect: 'error', cache: 'no-store',
    });
    const data = await response.json() as { error?: string };
    if (!response.ok) throw new Error(data.error || `The local API returned ${response.status}.`);
    return data;
  } catch (error) {
    if (error instanceof TypeError) throw new Error('Could not reach Meshway. Start the daemon and allow this page’s origin in allowed_origins.');
    if (error instanceof DOMException && error.name === 'AbortError') throw new Error('The local daemon did not respond in time.');
    throw error;
  } finally { clearTimeout(timeout); }
}

export function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const unit = value < 1024 ** 2 ? 'KB' : value < 1024 ** 3 ? 'MB' : 'GB';
  const divisor = unit === 'KB' ? 1024 : unit === 'MB' ? 1024 ** 2 : 1024 ** 3;
  return `${(value / divisor).toFixed(1)} ${unit}`;
}
