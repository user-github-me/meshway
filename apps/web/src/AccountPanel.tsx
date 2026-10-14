import { useEffect, useState, type FormEvent } from 'react';
import { ArrowUpRight, Check, CircleHelp, Fingerprint, History, Laptop, LockKeyhole, RefreshCw, ShieldCheck } from 'lucide-react';
import { apiRequest } from './api';

type Account = { account_id: string; device_id: string; created_at: string; sync_enabled: boolean };
type Device = { peer_id: string; label: string; created_at: string; last_seen: string; revoked: boolean };
type Event = { id: string; type: string; peer_id: string; time: string; detail: string };

function timestamp(value: string) { const date = new Date(value); return Number.isFinite(date.getTime()) ? date.toLocaleString() : value; }
function parseAccount(data: unknown): Account {
  if (!data || typeof data !== 'object') throw new Error('The account response is not supported.');
  const value = data as Record<string, unknown>;
  if (['account_id', 'device_id', 'created_at'].some(key => typeof value[key] !== 'string') || typeof value.sync_enabled !== 'boolean') throw new Error('The account response is not supported.');
  return value as unknown as Account;
}

export function AccountPanel({ endpoint, token, connected }: { endpoint: string; token: string; connected: boolean }) {
  const [account, setAccount] = useState<Account | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [loading, setLoading] = useState(false);
  const [syncPeer, setSyncPeer] = useState('');
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    setAccount(null); setDevices([]); setEvents([]); setError(''); setMessage('');
    if (!connected) return;
    let active = true;
    setLoading(true);
    void (async () => {
      const responses = await Promise.all([
        apiRequest(endpoint, token, '/v1/account/status'),
        apiRequest(endpoint, token, '/v1/account/devices'),
        apiRequest(endpoint, token, '/v1/account/history'),
      ]);
      const parsed = parseAccount(responses[0]);
      const devicesData = responses[1] as { devices?: unknown };
      const eventsData = responses[2] as { events?: unknown };
      if (!Array.isArray(devicesData.devices) || !devicesData.devices.every(d => d && typeof d.peer_id === 'string' && typeof d.label === 'string' && typeof d.created_at === 'string' && typeof d.last_seen === 'string' && typeof d.revoked === 'boolean')
        || !Array.isArray(eventsData.events) || !eventsData.events.every(e => e && ['id', 'type', 'peer_id', 'time', 'detail'].every(key => typeof e[key] === 'string'))) throw new Error('The account records are not supported by this dashboard.');
      if (active) { setAccount(parsed); setDevices(devicesData.devices); setEvents(eventsData.events); }
    })().catch(e => { if (active) setError((e as Error).message); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [connected, endpoint, token, revision]);
  const sync = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError(''); setMessage('');
    try { await apiRequest(endpoint, token, '/v1/account/sync', { peer_id: syncPeer.trim() }); setSyncPeer(''); setRevision(value => value + 1); setMessage('Account synchronization completed with the selected peer.'); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  const revoke = async (peerID: string) => {
    if (!window.confirm('Revoke this device? Connected private servers receive the signed revocation. Offline servers receive it after reconnecting.')) return;
    setBusy(true); setError('');
    try { await apiRequest(endpoint, token, '/v1/account/revoke', { peer_id: peerID }); setRevision(value => value + 1); setMessage('Device revoked. Revoking a device cannot invalidate a copied recovery key.'); }
    catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  const clearHistory = async () => {
    if (!window.confirm('Delete this account history? A signed deletion marker will prevent old records returning on your next optional sync.')) return;
    setBusy(true); setError('');
    try { await apiRequest(endpoint, token, '/v1/account/history', { synchronize: true }, 'DELETE'); setRevision(value => value + 1); setMessage('History deleted. Sync with your other devices to share the deletion.'); }
    catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  return <section className="dash-card account-card" id="account"><div className="card-heading"><h2><Fingerprint size={16} />Your account & devices</h2>{connected && <button className="text-button" onClick={() => setRevision(value => value + 1)} disabled={loading}><RefreshCw size={13} className={loading ? 'spin' : ''} />Refresh account</button>}</div><div className="account-intro"><div className="account-mark"><Fingerprint size={25} /></div><div><h3>One private key. Your own account.</h3><p>Your recovery key proves account ownership. Every device has its own peer identity. Your first account is created locally. Export or import its recovery key through the CLI; the dashboard never handles that key.</p></div></div>{error && <div className="account-error" role="alert"><CircleHelp size={14} /><span>{error}</span></div>}{message && <div className="account-success" role="status"><Check size={14} />{message}</div>}{account?.account_id ? <><div className="account-details"><div><span className="muted-label">ACCOUNT ID</span><code>{account.account_id}</code></div><div><span className="muted-label">THIS DEVICE</span><code>{account.device_id}</code></div><div><span className="muted-label">RECORD SYNC</span><span className={`stage ${account.sync_enabled ? 'stage-lime' : ''}`}>{account.sync_enabled ? 'Opted in' : 'Off · local only'}</span></div></div><div className="account-subheading"><h3><Laptop size={14} />Account devices <span className="count-badge">{devices.length}</span></h3><span>Signed device records</span></div>{devices.length ? <div className="device-list">{devices.map(device => <div className="device-row" key={device.peer_id}><span className="device-icon"><Laptop size={16} /></span><div><strong>{device.label || 'Unnamed device'}{device.peer_id === account.device_id && <small className="current-device">This device</small>}</strong><code>{device.peer_id}</code><small>Last seen {timestamp(device.last_seen)}</small></div><span className={`stage ${device.revoked ? '' : 'stage-lime'}`}>{device.revoked ? 'Revoked' : 'Authorized'}</span>{!device.revoked && device.peer_id !== account.device_id && <button className="text-button" disabled={busy} onClick={() => void revoke(device.peer_id)}>Revoke</button>}</div>)}</div> : <div className="account-empty">No device records returned by the daemon.</div>}<div className="account-subheading"><h3><History size={14} />Local history <span className="count-badge">{events.length}</span></h3><button className="text-button" disabled={busy || !events.length} onClick={() => void clearHistory()}>Delete history</button></div>{events.length ? <div className="history-list">{[...events].reverse().slice(0, 30).map(event => <div key={event.id}><span className="history-marker" /><div><strong>{event.type.replaceAll('_', ' ')}</strong><p>{event.detail || event.peer_id || 'Account event'}</p></div><time>{timestamp(event.time)}</time></div>)}</div> : <div className="account-empty">Your local history is empty. No activity is invented.</div>}<div className="account-sync"><div><h3><RefreshCw size={14} />Sync with another account device</h3><p>Explicitly exchange signed account records over an encrypted connection. The other device must belong to this account and be connected.</p></div><form onSubmit={sync}><label className="sr-only" htmlFor="sync-peer">Account device peer ID</label><input id="sync-peer" value={syncPeer} onChange={e => setSyncPeer(e.target.value)} placeholder="Connected account device peer ID" required disabled={!account.sync_enabled || busy} /><button className="button button-outline" disabled={!account.sync_enabled || busy}>{busy ? <RefreshCw size={13} className="spin" /> : <RefreshCw size={13} />}{busy ? 'Syncing…' : 'Sync records'}</button></form>{!account.sync_enabled && <p className="sync-disabled">Enable account sync explicitly in the CLI/configuration before synchronizing.</p>}</div></> : <div className="account-empty account-onboarding">{loading ? 'Loading your local account…' : connected ? 'Your daemon creates a local account on first start. Refresh to see its devices and local history.' : 'Pair your local daemon to view account identity, device records, and local history.'}<a className="text-link" href="#/docs">Account and recovery guide <ArrowUpRight size={13} /></a></div>}<div className="account-personal"><ShieldCheck size={18} /><div><h3>Your own VPS, in the same account.</h3><p>Import a secure recovery backup on your personal server. Your devices authorize through signed account certificates, without a manual peer-ID list. Choose it from Available connections once its invitation has been added.</p></div></div><div className="account-footnote"><LockKeyhole size={13} /><p>Keep the recovery key in a secure backup. Possession grants account access. There is no central password-reset service. History stays local unless you explicitly enable and request synchronization.</p></div></section>;
}
