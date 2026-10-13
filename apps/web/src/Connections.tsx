import { useEffect, useState, type FormEvent } from 'react';
import { ArrowRight, ChevronDown, CircleHelp, Globe2, List, Map, Radio, RefreshCw, Server, ShieldCheck, Zap } from 'lucide-react';
import { apiRequest } from './api';

type Connection = { peer_id: string; label: string; country?: string; address: string; personal?: boolean; roles: { relay: boolean; exit: boolean }; available: boolean; can_connect: boolean; last_seen: string };
// Country markers only appear when a peer explicitly supplies a recognizable country code.
const countries: Record<string, [number, number]> = { US: [175, 128], CA: [157, 80], GB: [370, 86], DE: [403, 96], FR: [386, 113], NL: [398, 87], SE: [421, 64], FI: [445, 65], NO: [406, 65], ES: [367, 137], IT: [411, 128], CH: [401, 112], PL: [428, 98], TR: [460, 140], UA: [452, 105], RU: [535, 70], CN: [580, 135], JP: [659, 140], KR: [635, 145], IN: [534, 192], SG: [580, 250], AU: [651, 310], NZ: [705, 346], BR: [274, 286], AR: [241, 340], MX: [146, 187], ZA: [437, 321], EG: [440, 187], AE: [495, 197], HK: [601, 191], TW: [625, 183], ID: [604, 274] };
const paths = ['M63 82 86 61 113 65 128 52 164 45 194 47 219 35 251 52 274 51 282 72 267 93 240 106 223 121 215 145 194 153 182 176 159 172 151 192 136 174 129 155 110 147 102 127 79 113Z', 'M228 207 251 201 269 215 291 222 310 249 300 275 282 288 273 316 258 342 243 355 235 333 229 306 217 283 213 256 207 230Z', 'M301 30 330 28 351 44 344 61 320 77 300 72 289 49Z', 'M363 86 383 72 407 75 423 60 445 66 456 84 450 103 466 117 468 136 450 146 431 139 418 125 402 127 387 116 376 131 356 117Z', 'M383 152 416 140 443 158 459 183 476 207 464 226 463 253 451 279 433 301 415 292 401 269 392 243 376 221 364 194 368 173Z', 'M457 80 486 61 520 55 555 45 584 55 616 51 661 64 699 79 713 100 690 113 669 132 654 156 632 160 610 182 604 205 583 209 571 188 550 182 536 207 523 216 510 197 504 173 484 166 472 144 475 120 455 100Z', 'M565 236 583 228 599 239 610 252 624 248 637 260 639 276 617 279 594 268 577 263Z', 'M623 295 650 280 678 289 690 310 681 333 654 342 631 329 613 319Z', 'M714 331 720 326 725 340 718 350 710 350Z', 'M682 155 689 152 693 170 687 184 680 181Z', 'M470 277 476 273 481 290 477 306 469 298Z'];
function role(server: Connection) { return server.personal ? 'Your personal server' : server.roles.exit ? 'Internet access' : server.roles.relay ? 'Relay' : 'Private network'; }

export function Connections({ endpoint, token, connected, upstream, upstreamConnected, onSelected }: { endpoint: string; token: string; connected: boolean; upstream: string; upstreamConnected: boolean; onSelected: () => Promise<void> }) {
  const [servers, setServers] = useState<Connection[]>([]);
  const [view, setView] = useState<'list' | 'map'>('list');
  const [error, setError] = useState('');
  const [selected, setSelected] = useState('');
  const [busy, setBusy] = useState('');
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const [invite, setInvite] = useState('');
  useEffect(() => {
    setServers([]); setError('');
    if (!connected) return;
    let active = true; setLoading(true);
    void apiRequest(endpoint, token, '/v1/servers').then(data => {
      const list = (data as { servers?: unknown }).servers;
      if (!Array.isArray(list) || !list.every(s => s && typeof s.peer_id === 'string' && typeof s.label === 'string' && typeof s.address === 'string' && typeof s.available === 'boolean' && typeof s.can_connect === 'boolean' && typeof s.last_seen === 'string' && s.roles && typeof s.roles.exit === 'boolean' && typeof s.roles.relay === 'boolean' && (s.country === undefined || typeof s.country === 'string') && (s.personal === undefined || typeof s.personal === 'boolean'))) throw new Error('The connection catalog is not supported by this dashboard.');
      if (active) { setServers(list); if (!selected && list.length) setSelected(list[0].peer_id); }
    }).catch(e => { if (active) setError((e as Error).message); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [endpoint, token, connected, revision]);
  const select = async (server: Connection) => {
    setBusy(server.peer_id); setError('');
    try { await apiRequest(endpoint, token, '/v1/select', { peer_id: server.peer_id, address: server.address }); await onSelected(); setRevision(x => x + 1); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(''); }
  };
  const disconnect = async () => {
    setBusy('disconnect'); setError('');
    try { await apiRequest(endpoint, token, '/v1/disconnect', {}); await onSelected(); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(''); }
  };
  const join = async (event: FormEvent) => {
    event.preventDefault(); setBusy('invitation'); setError('');
    try { await apiRequest(endpoint, token, '/v1/connect', { address: invite.trim() }); setInvite(''); await onSelected(); setRevision(x => x + 1); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(''); }
  };
  const mapped = servers.filter(server => server.country && countries[server.country.toUpperCase()]);
  const active = servers.find(server => server.peer_id === selected) || servers[0];
  function card(server: Connection) {
    const current = upstream === server.peer_id;
    return <article className={`server-choice ${current && upstreamConnected ? 'server-current' : ''}`} key={server.peer_id}><div className="server-choice-top"><span className={`server-symbol ${server.roles.exit ? 'exit-symbol' : ''}`}>{server.roles.exit ? <Globe2 size={23} /> : server.roles.relay ? <Radio size={23} /> : <Server size={23} />}</span><span className={`server-availability ${server.available ? 'available' : ''}`}><i />{server.available ? 'Available' : 'Not connected'}</span></div><h3>{server.label || 'Peer connection'}{server.personal && <span className="personal-badge"><ShieldCheck size={9} />Private</span>}</h3><p>{role(server)}{server.country ? ` · ${server.country}` : ''}</p><div className="server-choice-bottom"><span>{current ? <><ShieldCheck size={12} />{upstreamConnected ? 'Connected to this server' : 'Selected · connection offline'}</> : server.personal ? 'Only devices in your account' : server.roles.exit ? 'Through this peer’s public IP' : 'Encrypted peer connection'}</span><button className={`button ${current ? 'button-outline' : 'button-lime'}`} onClick={() => void (current ? disconnect() : select(server))} disabled={Boolean(busy) || (!current && (!server.address || !server.can_connect || !server.available))}>{busy === server.peer_id ? <RefreshCw size={12} className="spin" /> : current ? <ShieldCheck size={12} /> : <Zap size={12} />}{busy === server.peer_id ? 'Connecting…' : current ? 'Disconnect' : !server.available ? 'Unavailable' : !server.can_connect ? server.roles.exit ? 'Approval required' : 'Relay only' : 'Connect'}</button></div></article>;
  }
  return <section className="connections-section" id="available"><div className="connections-heading"><div><div className="eyebrow subdued">YOUR NEXT CONNECTION</div><h2>Choose where to connect<span>.</span></h2><p>Pick a peer you trust. Your node handles the encrypted connection.</p></div><div className="connections-tools"><button className="icon-button" aria-label="Refresh available connections" disabled={!connected || loading} onClick={() => setRevision(value => value + 1)}><RefreshCw size={15} className={loading ? 'spin' : ''} /></button><div className="view-switch" role="group" aria-label="Connection view"><button className={view === 'list' ? 'active' : ''} onClick={() => setView('list')} aria-pressed={view === 'list'}><List size={13} />List</button><button className={view === 'map' ? 'active' : ''} onClick={() => setView('map')} aria-pressed={view === 'map'}><Map size={13} />Map</button></div></div></div>{error && <div className="alert alert-error" role="alert"><CircleHelp size={15} />{error}</div>}{view === 'map' && <div className="world-view"><div className="world-view-label"><Globe2 size={13} />YOUR PEER NETWORK<span>Opt-in country locations only</span></div><svg viewBox="0 0 780 400" role="img" aria-label={mapped.length ? 'Map with country locations shared by available peers' : 'World map with no location markers; no peers have shared a country'}><defs><pattern id="world-dots" width="6" height="6" patternUnits="userSpaceOnUse"><circle cx="2" cy="2" r="1" fill="#745184" /></pattern></defs><g fill="url(#world-dots)" stroke="#674473" strokeWidth=".5">{paths.map((path, index) => <path key={index} d={path} />)}</g>{mapped.map(server => { const point = countries[server.country!.toUpperCase()]; return <g key={server.peer_id} role="button" tabIndex={0} aria-label={`Select ${server.label || server.country}`} onClick={() => setSelected(server.peer_id)} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') setSelected(server.peer_id); }} className="world-marker"><circle cx={point[0]} cy={point[1]} r={selected === server.peer_id ? 13 : 10} fill="#d3ff7325" /><circle cx={point[0]} cy={point[1]} r={selected === server.peer_id ? 5 : 4} fill="#d3ff73" /></g>; })}</svg><div className="world-view-footnote">{mapped.length ? `${mapped.length} peer location${mapped.length > 1 ? 's' : ''} shared voluntarily. Countries are self-reported.` : 'No peers have shared a location. Connection choices remain available in the list.'}</div>{active && <div className="map-selected">{card(active)}</div>}</div>}{view === 'list' && servers.length > 0 && <div className="server-grid">{servers.map(card)}</div>}{!servers.length && <div className="connections-empty"><div><Globe2 size={27} /></div><h3>{connected ? 'No servers yet.' : 'Your network is ready to begin.'}</h3><p>{connected ? 'Add a trusted invitation or configure a self-hosted server to see real connection choices here.' : 'Start your local node below. Available connections will appear after you add a trusted peer.'}</p><a href="#/docs" className="text-link">Set up your first connection <ArrowRight size={13} /></a></div>}{connected && <details className="invite-details"><summary><span>Add a trusted peer invitation</span><ChevronDown size={14} /></summary><p>Paste the full peer address shared by someone you trust. Connecting adds it to your local peer list.</p><form onSubmit={join}><label className="sr-only" htmlFor="invite-address">Trusted peer invitation</label><input id="invite-address" value={invite} onChange={e => setInvite(e.target.value)} placeholder="/ip4/…/tcp/4001/p2p/PEER_ID" required /><button className="button button-outline" disabled={Boolean(busy)}>{busy === 'invitation' ? 'Adding…' : 'Add peer'}<ArrowRight size={12} /></button></form></details>}<div className="connections-trust"><ShieldCheck size={12} /><p>Sharing internet access uses the exit peer’s public IP. Only trusted, permitted peers can forward traffic.</p></div></section>;
}
