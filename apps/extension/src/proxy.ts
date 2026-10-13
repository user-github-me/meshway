export type Setting = { value: Record<string, unknown>; levelOfControl: string };
export type SavedState = { enabled: true; host: string; port: number; previous: Setting; applied: Record<string, unknown> };
export type ProxyAdapter = {
  get(): Promise<Setting>; set(value: Record<string, unknown>): Promise<void>; clear(): Promise<void>;
  load(): Promise<SavedState | undefined>; save(state: SavedState): Promise<void>; remove(): Promise<void>;
};

export function parseProxy(host: string, rawPort: string): { host: string; port: number } {
  const normalized = host.trim().toLowerCase();
  if (!['127.0.0.1', 'localhost', '::1', '[::1]'].includes(normalized)) throw new Error('Only a local loopback SOCKS proxy is supported.');
  if (!/^\d{1,5}$/.test(rawPort)) throw new Error('Use a port from 1 to 65535.');
  const port = Number(rawPort);
  if (port < 1 || port > 65535) throw new Error('Use a port from 1 to 65535.');
  return { host: normalized === '[::1]' ? '::1' : normalized, port };
}

export function proxyValue(host: string, port: number, firefox: boolean): Record<string, unknown> {
  if (firefox) return { proxyType: 'manual', socks: `${host.includes(':') ? `[${host}]` : host}:${port}`, socksVersion: 5, proxyDNS: true, passthrough: 'localhost, 127.0.0.1, ::1', http: '', ssl: '', ftp: '' };
  return { mode: 'fixed_servers', rules: { singleProxy: { scheme: 'socks5', host, port }, bypassList: ['localhost', '127.0.0.1', '[::1]'] } };
}

export class ProxyController {
  constructor(private adapter: ProxyAdapter, private firefox: boolean) {}
  async status(): Promise<{ enabled: boolean; host: string; port: number; controlled: boolean; level: string }> {
    const [state, current] = await Promise.all([this.adapter.load(), this.adapter.get()]);
    return { enabled: Boolean(state), host: state?.host || '127.0.0.1', port: state?.port || 1080, controlled: current.levelOfControl === 'controlled_by_this_extension', level: current.levelOfControl };
  }
  async enable(host: string, rawPort: string): Promise<void> {
    const proxy = parseProxy(host, rawPort);
    const priorState = await this.adapter.load();
    const current = await this.adapter.get();
    if (['controlled_by_other_extensions', 'not_controllable'].includes(current.levelOfControl)) throw new Error('Another extension or browser policy controls your proxy. Disable it before enabling Meshway.');
    const state: SavedState = { enabled: true, ...proxy, previous: priorState?.previous || current, applied: proxyValue(proxy.host, proxy.port, this.firefox) };
    // Persist recovery information before touching a browser-wide setting. MV3 workers may unload.
    await this.adapter.save(state);
    try { await this.adapter.set(state.applied); }
    catch (error) {
      if (priorState) await this.adapter.save(priorState); else await this.adapter.remove();
      throw error;
    }
  }
  async disable(): Promise<void> {
    const state = await this.adapter.load();
    if (!state) return;
    // Clear our override to reveal the browser's prior preference/policy/other extension.
    // If our extension already owned a setting before enable, restore exactly that owned setting.
    if (state.previous.levelOfControl === 'controlled_by_this_extension') await this.adapter.set(state.previous.value);
    else await this.adapter.clear();
    await this.adapter.remove();
  }
}
