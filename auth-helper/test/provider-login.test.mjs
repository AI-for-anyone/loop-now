import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createProviderLogin, safeURL, plainText } from '../provider-login.mjs';

const sleep = ms => new Promise(r => setTimeout(r, ms));
async function eventually(fn) { for (let i = 0; i < 200; i++) { if (fn()) return; await sleep(5); } throw new Error('condition timed out'); }
const secret = { access: 'access-private-marker', refresh: 'refresh-private-marker', expires: 123 };
function harness(oauth) {
 const records = [];
 const helper = createProviderLogin({ send: r => records.push(r), load: async (root, exts, provider) => { assert.equal(provider, 'fake-oauth'); assert.deepEqual(exts, ['/x/index.ts']); return oauth; } });
 return { helper, records, start: (timeoutMs = 1000) => helper.receive({ type: 'login', id: 'op1', provider: 'fake-oauth', piPackageRoot: '/pi', extensions: ['/x/index.ts'], timeoutMs }) };
}
const result = h => h.records.find(r => r.type === 'result');

test('select, auth url, device code and text prompts go out as plain text; the entry only in the result', async () => {
 const h = harness({ async login(cb) {
  const method = await cb.onSelect({ message: 'Pick\u0007 a method', options: [{ id: 'browser', label: 'Browser' }, { id: 'device', label: 'Device' }] });
  assert.equal(method, 'device');
  cb.onAuth({ url: 'https://accounts.example.test/authorize?state=s&client_id=c', instructions: 'secret-instructions' });
  cb.onDeviceCode({ userCode: 'ABCD-EFGH', verificationUri: 'https://accounts.example.test/device', expiresInSeconds: 600 });
  cb.onProgress('progress ' + secret.access);
  assert.equal(await cb.onPrompt({ message: 'Workspace?' }), 'team-a');
  return secret;
 } });
 h.start();
 await eventually(() => h.records.some(r => r.type === 'prompt'));
 const select = h.records.find(r => r.type === 'prompt');
 assert.equal(select.kind, 'select');
 assert.equal(select.message, 'Pick  a method');
 assert.deepEqual(select.options, [{ id: 'browser', label: 'Browser' }, { id: 'device', label: 'Device' }]);
 h.helper.receive({ type: 'prompt_reply', id: 'op1', promptId: select.promptId, value: 'not-an-option' });
 await sleep(10);
 assert(!result(h), '不是选项的回答被忽略');
 h.helper.receive({ type: 'prompt_reply', id: 'op1', promptId: select.promptId, value: 'device' });
 await eventually(() => h.records.filter(r => r.type === 'prompt').length === 2);
 const text = h.records.filter(r => r.type === 'prompt')[1];
 assert.equal(text.kind, 'text');
 h.helper.receive({ type: 'prompt_reply', id: 'op1', promptId: text.promptId, value: 'team-a' });
 await eventually(() => result(h));
 assert.equal(result(h).status, 'succeeded');
 assert.deepEqual(result(h).authEntry, { ...secret, type: 'oauth' });
 const notify = h.records.filter(r => r.type === 'notify').map(r => r.event);
 assert.deepEqual(notify[0], { type: 'auth_url', url: 'https://accounts.example.test/authorize?state=s&client_id=c' });
 assert.deepEqual(notify[1], { type: 'device_code', userCode: 'ABCD-EFGH', verificationUri: 'https://accounts.example.test/device', expiresInSeconds: 600 });
 assert.deepEqual(notify[2], { type: 'progress' });
 const ordinary = JSON.stringify(h.records.filter(r => r.type !== 'result'));
 assert(!ordinary.includes(secret.access) && !ordinary.includes('secret-instructions'));
});

test('unsafe urls end the login at once; provider errors are not echoed', async () => {
 for (const url of ['http://accounts.example.test/a', 'https://u:p@example.test/a', 'https://example.test/a#frag', 'https://example.test/cb?code=abc', 'https://example.test/a?access_token=x']) {
  const h = harness({ async login(cb) { try { cb.onAuth({ url }); } catch {} return new Promise(() => {}); } });
  h.start();
  await eventually(() => result(h));
  assert.equal(result(h).status, 'failed');
  assert.equal(result(h).code, 'invalid_authorization_url');
 }
 const h = harness({ async login() { throw new Error('boom ' + secret.refresh); } });
 h.start();
 await eventually(() => result(h));
 assert.deepEqual([result(h).status, result(h).code], ['failed', 'auth_failed']);
 assert(!JSON.stringify(h.records).includes(secret.refresh));
});

test('cancel, timeout and a missing provider', async () => {
 let started = false;
 const h = harness({ async login(cb) { started = true; await cb.onManualCodeInput(); return secret; } });
 h.start();
 await eventually(() => started && h.records.some(r => r.type === 'prompt'));
 h.helper.receive({ type: 'abort', id: 'op1' });
 await eventually(() => result(h));
 assert.equal(result(h).status, 'cancelled');
 assert(h.records.some(r => r.type === 'prompt_closed'));
 const slow = harness({ async login() { return new Promise(() => {}); } });
 slow.start(20);
 await eventually(() => result(slow));
 assert.equal(result(slow).status, 'expired');
 const records = [];
 const missing = createProviderLogin({ send: r => records.push(r), load: async () => { throw new Error('extension_load_failed'); } });
 missing.receive({ type: 'login', id: 'op1', provider: 'fake-oauth', piPackageRoot: '/pi', extensions: ['/x/index.ts'], timeoutMs: 1000 });
 await eventually(() => records.some(r => r.type === 'result'));
 assert.equal(records.find(r => r.type === 'result').code, 'extension_load_failed');
});

test('a busy local callback port turns a failed login into auth_port_busy', async () => {
 const holder = http.createServer();
 await new Promise(r => holder.listen(0, '127.0.0.1', r));
 const port = holder.address().port;
 try {
  const h = harness({ async login() {
   const server = http.createServer();
   await new Promise(resolve => { server.once('error', resolve); server.listen(port, '127.0.0.1'); });
   throw new Error('callback bind failed');
  } });
  h.start();
  await eventually(() => result(h));
  assert.deepEqual([result(h).status, result(h).code], ['failed', 'auth_port_busy']);
 } finally { holder.close(); }
});

test('url and text guards', () => {
 assert.equal(safeURL('https://a.test/x?state=1'), 'https://a.test/x?state=1');
 assert.equal(safeURL('javascript:alert(1)'), null);
 assert.equal(plainText('a'.repeat(10), 4), 'aaaa…');
 assert.equal(plainText('line1\nline2\u001b[31m', 100), 'line1\nline2 [31m');
});
