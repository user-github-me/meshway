import { ProxyController, type ProxyAdapter, type SavedState, type Setting } from './proxy';

declare const FIREFOX: boolean;
declare const browser: {
  proxy: { settings: { get(details: Record<string, unknown>): Promise<Setting>; set(details: { value: Record<string, unknown> }): Promise<void>; clear(details: Record<string, unknown>): Promise<void> } };
  storage: { local: { get(key: string): Promise<Record<string, SavedState>>; set(data: Record<string, unknown>): Promise<void>; remove(key: string): Promise<void> } };
  runtime: { onMessage: { addListener(callback: (message: unknown, sender: { id?: string }) => Promise<unknown> | undefined): void }; id: string };
};

const KEY = 'meshwayProxyRecovery';
function chromeCall<T>(invoke: (callback: (value: T) => void) => void): Promise<T> {
  return new Promise((resolve, reject) => invoke(value => { const error = chrome.runtime.lastError; if (error) reject(new Error(error.message)); else resolve(value); }));
}
const adapter: ProxyAdapter = FIREFOX ? {
  get: () => browser.proxy.settings.get({}),
  set: value => browser.proxy.settings.set({ value }), clear: () => browser.proxy.settings.clear({}),
  load: async () => (await browser.storage.local.get(KEY))[KEY],
  save: state => browser.storage.local.set({ [KEY]: state }), remove: () => browser.storage.local.remove(KEY),
} : {
  get: async () => {
    const result = await chromeCall<chrome.types.ChromeSettingGetResult<chrome.proxy.ProxyConfig>>(callback => chrome.proxy.settings.get({ incognito: false }, callback));
    return { value: result.value as unknown as Record<string, unknown>, levelOfControl: result.levelOfControl };
  },
  set: value => chromeCall<void>(callback => chrome.proxy.settings.set({ value: value as unknown as chrome.proxy.ProxyConfig, scope: 'regular' }, callback)),
  clear: () => chromeCall<void>(callback => chrome.proxy.settings.clear({ scope: 'regular' }, callback)),
  load: async () => (await chromeCall<Record<string, SavedState>>(callback => chrome.storage.local.get(KEY, callback)))[KEY],
  save: state => chromeCall<void>(callback => chrome.storage.local.set({ [KEY]: state }, callback)),
  remove: () => chromeCall<void>(callback => chrome.storage.local.remove(KEY, callback)),
};
const controller = new ProxyController(adapter, FIREFOX);
// Serialize mutations so two popups cannot overwrite recovery state while changing settings.
let queue: Promise<unknown> = Promise.resolve();
function handle(message: unknown): Promise<unknown> {
  const result = queue.then(async () => {
    if (!message || typeof message !== 'object') throw new Error('Invalid extension request.');
    const m = message as Record<string, unknown>;
    if (m.type === 'enable') {
      if (m.consent !== true || typeof m.host !== 'string' || typeof m.port !== 'string') throw new Error('Explicit consent and a local proxy are required.');
      await controller.enable(m.host, m.port);
    } else if (m.type === 'disable') await controller.disable();
    else if (m.type !== 'status') throw new Error('Unknown extension request.');
    return { ok: true, ...await controller.status(), firefox: FIREFOX };
  });
  queue = result.catch(() => {});
  return result.catch(error => ({ ok: false, error: error instanceof Error ? error.message : 'Browser proxy operation failed.' }));
}
if (FIREFOX) browser.runtime.onMessage.addListener((message, sender) => sender.id === browser.runtime.id ? handle(message) : undefined);
else chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (sender.id !== chrome.runtime.id) return false;
  void handle(message).then(sendResponse); return true;
});
