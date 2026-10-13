import test from 'node:test';
import assert from 'node:assert/strict';
import { parseProxy, proxyValue, ProxyController, type ProxyAdapter, type SavedState, type Setting } from './proxy';

function adapter(previous: Setting = { value: { mode: 'system' }, levelOfControl: 'controllable_by_this_extension' }) {
  let state: SavedState | undefined;
  let current = previous;
  const operations: string[] = [];
  const fake: ProxyAdapter = {
    get: async () => current, load: async () => state,
    set: async value => { operations.push('set'); current = { value, levelOfControl: 'controlled_by_this_extension' }; },
    clear: async () => { operations.push('clear'); current = previous; },
    save: async value => { operations.push('save'); state = structuredClone(value); },
    remove: async () => { operations.push('remove'); state = undefined; },
  };
  return { fake, operations, state: () => state, current: () => current };
}

test('accepts only explicit loopback hosts and valid SOCKS port numbers', () => {
  assert.deepEqual(parseProxy(' [::1] ', '1080'), { host: '::1', port: 1080 });
  for (const host of ['example.com', '127.0.0.1.example', '192.168.0.2', 'user@localhost', 'http://localhost']) assert.throws(() => parseProxy(host, '1080'));
  for (const port of ['', '0', '65536', '1.5', '-1', '80abc']) assert.throws(() => parseProxy('localhost', port));
});
test('Chromium uses SOCKS5; Firefox includes remote DNS and local bypasses', () => {
  const chrome = proxyValue('127.0.0.1', 1080, false);
  assert.equal(chrome.mode, 'fixed_servers');
  assert.deepEqual((chrome.rules as any).singleProxy, { scheme: 'socks5', host: '127.0.0.1', port: 1080 });
  const firefox = proxyValue('::1', 1080, true);
  assert.equal(firefox.socks, '[::1]:1080'); assert.equal(firefox.proxyDNS, true);
});
test('persists recovery before applying, survives a new worker, then restores inherited settings', async () => {
  const a = adapter();
  await new ProxyController(a.fake, false).enable('127.0.0.1', '1080');
  assert.deepEqual(a.operations, ['save', 'set']); assert.equal(a.state()?.previous.value.mode, 'system');
  const restarted = new ProxyController(a.fake, false);
  assert.equal((await restarted.status()).enabled, true);
  await restarted.disable();
  assert.deepEqual(a.operations.slice(-2), ['clear', 'remove']); assert.equal(a.current().value.mode, 'system');
});
test('changing proxy retains original backup and restores an already-owned prior setting', async () => {
  const previous = { value: { mode: 'direct' }, levelOfControl: 'controlled_by_this_extension' };
  const a = adapter(previous), controller = new ProxyController(a.fake, false);
  await controller.enable('localhost', '1080'); await controller.enable('localhost', '1081');
  assert.deepEqual(a.state()?.previous, previous);
  await controller.disable(); assert.deepEqual(a.current(), previous);
});
test('does not overwrite policy or another extension; persistence failure never changes proxy', async () => {
  for (const levelOfControl of ['not_controllable', 'controlled_by_other_extensions']) {
    const a = adapter({ value: { mode: 'system' }, levelOfControl });
    await assert.rejects(new ProxyController(a.fake, false).enable('localhost', '1080'), /controls your proxy/);
    assert.deepEqual(a.operations, []);
  }
  const a = adapter(); a.fake.save = async () => { throw new Error('storage failed'); };
  await assert.rejects(new ProxyController(a.fake, false).enable('localhost', '1080'), /storage failed/);
  assert.deepEqual(a.operations, []);
});
test('failed set removes pending recovery; failed clear keeps recovery for retry', async () => {
  const a = adapter(); a.fake.set = async () => { throw new Error('set denied'); };
  await assert.rejects(new ProxyController(a.fake, false).enable('localhost', '1080'), /set denied/); assert.equal(a.state(), undefined);
  const b = adapter(), controller = new ProxyController(b.fake, false);
  await controller.enable('localhost', '1080'); b.fake.clear = async () => { throw new Error('clear denied'); };
  await assert.rejects(controller.disable(), /clear denied/); assert.equal(b.state()?.enabled, true);
});
