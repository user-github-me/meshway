import test from 'node:test';
import assert from 'node:assert/strict';
import { localEndpoint, parseStatus, apiRequest, formatBytes } from './api.ts';

test('credentials can only be sent to an unambiguous HTTP loopback origin', () => {
  for (const host of ['127.0.0.1', 'localhost', '[::1]']) assert.equal(localEndpoint(`http://${host}:7777/`), `http://${host}:7777`);
  for (const value of ['https://example.com', 'http://127.0.0.1.evil.test', 'http://user:pass@localhost:7777', 'http://localhost:7777/path', 'file:///tmp/api', 'http://localhost:7777?token=x']) assert.throws(() => localEndpoint(value));
});

test('rejects corrupt or incomplete status before displaying trusted network information', () => {
  const valid = { version: '0.1', peer_id: 'peer1', addresses: [], peers: [], roles: { relay: false, exit: false }, socks_listen: '127.0.0.1:1080', api_listen: '127.0.0.1:7777', upstream_peer: '', started_at: '2026-01-01T00:00:00Z', bytes_sent: 0, bytes_received: 4 };
  assert.equal(parseStatus(valid).peer_id, 'peer1');
  assert.throws(() => parseStatus({ ...valid, peers: [{ id: 'peer2', addresses: [4] }] }));
  assert.throws(() => parseStatus({ ...valid, bytes_received: -1 }));
  assert.throws(() => parseStatus({ ...valid, roles: { relay: 'false', exit: false } }));
});

test('request sends bearer authentication and disallows redirects', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input, init) => {
    assert.equal(input, 'http://127.0.0.1:7777/v1/connect');
    assert.equal((init?.headers as Record<string, string>).Authorization, 'Bearer local-token');
    assert.equal(init?.redirect, 'error');
    assert.equal(init?.credentials, 'omit');
    assert.equal(init?.body, '{"address":"/ip4/127.0.0.1/tcp/4001/p2p/peer"}');
    return new Response('{"id":"peer"}', { status: 200 });
  }) as typeof fetch;
  try { assert.deepEqual(await apiRequest('http://127.0.0.1:7777', 'local-token', '/v1/connect', { address: '/ip4/127.0.0.1/tcp/4001/p2p/peer' }), { id: 'peer' }); }
  finally { globalThis.fetch = originalFetch; }
});

test('reports authorization errors and formats traffic without invented activity', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => new Response('{"error":"unauthorized"}', { status: 401 })) as typeof fetch;
  try { await assert.rejects(apiRequest('http://localhost:7777', 'bad-token', '/v1/status'), /unauthorized/); }
  finally { globalThis.fetch = originalFetch; }
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(1536), '1.5 KB');
});
