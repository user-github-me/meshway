import { useEffect, useRef, useState } from 'react';
import { Activity, ArrowDown, ArrowUp, Clock3, Globe2, LockKeyhole, Network, ShieldCheck, Waypoints } from 'lucide-react';
import { apiRequest, formatBytes, type Status } from './api';
import './control-dashboard.css';

type Sample = { sent: number; received: number; time: number };
type Props = { status: Status | null; endpoint: string; token: string; healthy: boolean; refresh: () => Promise<void> };

export function ConnectionDashboard({ status, endpoint, token, healthy, refresh }: Props) {
  const [mode, setMode] = useState<'socks' | 'vpn' | 'personal'>('socks');
  const [rates, setRates] = useState<{ up: number; down: number }[]>([]);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const sample = useRef<Sample | null>(null);
  const currentPeer = useRef('');
  useEffect(() => {
    if (!status || !healthy) { sample.current = null; setRates([]); return; }
    const next = { sent: status.bytes_sent, received: status.bytes_received, time: Date.now() };
    const previous = sample.current;
    if (previous && currentPeer.current === status.peer_id && next.sent >= previous.sent && next.received >= previous.received) {
      const seconds = (next.time - previous.time) / 1000;
      if (seconds > .1) setRates(values => [...values.slice(-29), { up: (next.sent - previous.sent) / seconds, down: (next.received - previous.received) / seconds }]);
    } else setRates([]);
    sample.current = next; currentPeer.current = status.peer_id;
  }, [status, healthy]);
  const connected = healthy && Boolean(status?.upstream_connected);
  const speed = rates.at(-1);
  const ssh = status?.transport;
  const activeVPN = healthy && Boolean(status?.vpn?.active);
  const socks = healthy && Boolean(status?.socks_listen);
  const personal = connected && ssh?.kind === 'ssh' && ssh.ready && ssh.authenticated;
  const elapsed = status && healthy ? Math.max(0, Math.floor((Date.now() - Date.parse(status.started_at)) / 1000)) : 0;
  const age = `${Math.floor(elapsed / 3600).toString().padStart(2, '0')}:${Math.floor(elapsed / 60 % 60).toString().padStart(2, '0')}:${(elapsed % 60).toString().padStart(2, '0')}`;
  const limit = Math.max(1, ...rates.map(rate => Math.max(rate.up, rate.down)));
  const line = (direction: 'up' | 'down') => rates.map((rate, i) => `${i * 600 / Math.max(1, rates.length - 1)},${140 - rate[direction] / limit * 110}`).join(' ');
  async function disconnect() {
    setBusy(true); setMessage('');
    try { await apiRequest(endpoint, token, '/v1/disconnect', {}); await refresh(); }
    catch (error) { setMessage((error as Error).message); }
    finally { setBusy(false); }
  }
  async function stopVPN() {
    setBusy(true); setMessage('');
    try { await apiRequest(endpoint, token, '/v1/vpn/stop', {}); await refresh(); setMessage('Packet forwarding stopped. Use the native VPN controls to restore device routing.'); }
    catch (error) { setMessage((error as Error).message); }
    finally { setBusy(false); }
  }
  const descriptions = {
    socks: 'Send selected applications through your chosen Meshway server. Use the browser extension or the local SOCKS address.',
    vpn: 'Capture all device traffic with the native TUN/VPN interface. Start it in the platform app or Linux VPN controls.',
    personal: 'Use your own Meshway server through a private encrypted tunnel. SSH transport is configured on your device; your recovery account authorizes the exit.',
  };
  return <section className="control-dashboard" aria-label="Connection dashboard">
    <div className="connection-hero">
      <div className={`connection-orb ${connected ? 'is-connected' : ''}`}><ShieldCheck size={35} /></div>
      <div className="connection-hero-copy"><span className="eyebrow subdued">YOUR CONNECTION</span><h2>{connected ? 'Server connection ready' : healthy ? 'Choose a server to connect' : 'Start your Meshway node'}</h2><p>{connected ? 'Use System proxy in the native app or enable the browser extension to route browser traffic.' : 'Your connection status appears here when the local node is ready.'}</p></div>
      {connected ? <button className="button button-outline" disabled={busy} onClick={() => void disconnect()}>Disconnect server</button> : <a className="button button-lime" href={healthy ? '#/dashboard/available' : '#/dashboard/pair'}>{healthy ? 'Choose server' : 'Set up connection'}</a>}
    </div>
    <div className="primary-mode-picker" role="group" aria-label="Connection mode">
      {[['socks', 'SOCKS proxy', Network], ['vpn', 'VPN / TUN', ShieldCheck], ['personal', 'Personal tunnel', LockKeyhole]].map(([value, title, Icon]) => {
        const ModeIcon = Icon as typeof Network;
        return <button key={String(value)} className={mode === value ? 'selected' : ''} aria-pressed={mode === value} onClick={() => setMode(value as typeof mode)}><ModeIcon size={20} /><strong>{String(title)}</strong><span>{value === 'socks' ? 'Selected applications' : value === 'vpn' ? 'All device traffic' : 'Your own private server'}</span></button>;
      })}
    </div>
    <div className="mode-explanation"><p>{descriptions[mode]}</p>{mode === 'vpn' && <span>Use the native app for device VPN. The macOS administrator helper requests network authorization; iPhone’s Network Extension requires Apple signing. This browser dashboard cannot create a device VPN.</span>}{mode === 'personal' && <span>{personal ? 'Managed private SSH transport is connected.' : ssh ? 'Private transport is configured; check its connection below.' : 'For SSH, configure your personal tunnel in the native app. Direct TCP/QUIC peers also use authenticated encryption.'}</span>}</div>
    {message && <p className="alert" role="status">{message}</p>}
    <div className="live-control-grid">
      <article className="control-card speed-card"><div className="control-card-title"><h3><Activity size={18} />Network speed</h3><span><ArrowUp size={13} />{speed ? formatBytes(speed.up) : '—'}/s <ArrowDown size={13} />{speed ? formatBytes(speed.down) : '—'}/s</span></div>
        <div className="traffic-chart">{rates.length > 1 ? <svg viewBox="0 0 600 160" preserveAspectRatio="none" role="img" aria-label="Measured upload and download rates"><line x1="0" y1="140" x2="600" y2="140" stroke="currentColor" opacity=".15" /><polyline points={line('up')} fill="none" stroke="#c4a4ff" strokeWidth="3" /><polyline points={line('down')} fill="none" stroke="#d3ff73" strokeWidth="3" /></svg> : <div className="traffic-empty"><Activity size={27} /><span>{healthy ? 'Collecting live traffic samples' : 'Connect your node to see live traffic'}</span></div>}</div><p>SOCKS payload + VPN packets · transport overhead excluded</p>
      </article>
      <article className="control-card"><h3><Network size={18} />SOCKS proxy</h3><strong className={socks ? 'state-on' : ''}>{socks ? 'Listening' : 'Off'}</strong><code>{socks ? status?.socks_listen : 'Start SOCKS mode in the app'}</code><small>Applications use this address explicitly.</small></article>
      <article className="control-card"><h3><ShieldCheck size={18} />VPN / TUN</h3><strong className={activeVPN ? 'state-on' : ''}>{activeVPN ? 'Packet session active' : 'Off'}</strong><small>{activeVPN ? 'Device capture is controlled by the native app.' : 'Use your native VPN controls to capture all traffic.'}</small>{activeVPN && <button className="text-button" disabled={busy} onClick={() => void stopVPN()}>Stop packet forwarding</button>}</article>
      <article className="control-card"><h3><ArrowUp size={18} />Traffic usage</h3><div className="usage-row"><span>Upload</span><strong>{healthy && status ? formatBytes(status.bytes_sent) : '—'}</strong></div><div className="usage-row"><span>Download</span><strong>{healthy && status ? formatBytes(status.bytes_received) : '—'}</strong></div><small>Since node startup · forwarded payloads</small></article>
      <article className="control-card"><h3><LockKeyhole size={18} />Personal transport</h3><strong className={personal ? 'state-on' : ''}>{ssh?.kind === 'ssh' ? ssh.running ? 'SSH tunnel running' : 'SSH tunnel offline' : connected ? 'Encrypted peer link' : 'Not connected'}</strong><small>{ssh?.error || (ssh?.kind === 'ssh' ? 'Meshway runs through your private SSH connection.' : 'Meshway authenticates and encrypts direct and relayed peer traffic.')}</small></article>
      <article className="control-card"><h3><Waypoints size={18} />Connected peers</h3><strong>{healthy && status ? status.peers.length : '—'}</strong><small>Authenticated devices currently reachable</small></article>
      <article className="control-card"><h3><Clock3 size={18} />Node uptime</h3><strong>{healthy ? age : '—'}</strong><small>Your local node session</small></article>
      <article className="control-card"><h3><Globe2 size={18} />Service tunnels</h3><strong>{status?.configuration_summary ? status.configuration_summary.forwards.length : '—'}</strong><small>Named private service forwards configured on this node</small></article>
    </div>
  </section>;
}
