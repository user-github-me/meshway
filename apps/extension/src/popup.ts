export {};
declare const FIREFOX: boolean;
declare const browser: { runtime: { sendMessage(message: unknown): Promise<unknown> } };
type Response = { ok: boolean; enabled?: boolean; controlled?: boolean; host?: string; port?: number; level?: string; error?: string };
const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const form = element<HTMLFormElement>('proxy-form');
const host = element<HTMLInputElement>('host');
const port = element<HTMLInputElement>('port');
const consent = element<HTMLInputElement>('consent');
const toggle = element<HTMLButtonElement>('toggle');
const status = element<HTMLDivElement>('status');
const statusLabel = element<HTMLSpanElement>('status-label');
const error = element<HTMLDivElement>('error');
const help = element<HTMLParagraphElement>('dns-note');
let enabled = false;
async function send(message: unknown): Promise<Response> {
  if (FIREFOX) return await browser.runtime.sendMessage(message) as Response;
  return await new Promise((resolve, reject) => chrome.runtime.sendMessage(message, response => { const e = chrome.runtime.lastError; if (e) reject(new Error(e.message)); else resolve(response); }));
}
function render(response: Response) {
  if (!response.ok) throw new Error(response.error || 'Could not read browser proxy state.');
  enabled = Boolean(response.enabled); host.value = response.host || '127.0.0.1'; port.value = String(response.port || 1080);
  status.classList.toggle('enabled', enabled && Boolean(response.controlled));
  statusLabel.textContent = enabled ? response.controlled ? 'Browser proxy enabled' : 'Saved, but another setting has control' : 'Browser proxy is off';
  toggle.textContent = enabled ? 'Disable & restore settings' : 'Enable browser proxy';
  toggle.classList.toggle('disable', enabled);
  consent.disabled = enabled; host.disabled = enabled; port.disabled = enabled;
  element<HTMLElement>('consent-section').hidden = enabled;
  help.textContent = FIREFOX ? 'Firefox sends DNS through SOCKS. Local destinations bypass the proxy. Device traffic and UDP are not covered.' : 'Chrome does not provide a separate proxy DNS switch. Local destinations bypass the proxy. Device traffic and UDP are not covered.';
}
async function load() {
  toggle.disabled = true;
  try { render(await send({ type: 'status' })); }
  catch (e) { error.hidden = false; error.textContent = (e as Error).message; }
  finally { toggle.disabled = false; }
}
form.addEventListener('submit', async event => {
  event.preventDefault(); error.hidden = true;
  if (!enabled && !consent.checked) { error.textContent = 'Please confirm that you want to change browser proxy settings.'; error.hidden = false; return; }
  toggle.disabled = true;
  try { render(await send(enabled ? { type: 'disable' } : { type: 'enable', host: host.value, port: port.value, consent: true })); if (!enabled) consent.checked = false; }
  catch (e) { error.textContent = (e as Error).message; error.hidden = false; }
  finally { toggle.disabled = false; }
});
void load();
