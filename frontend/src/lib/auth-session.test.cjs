const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const ts = require('typescript');
function harness(desktop) {
 const events = [];
 let request;
 const window = { dispatchEvent(e) { events.push(e.type); } };
 const module = { exports: {} };
 const source = ts.transpileModule(fs.readFileSync(__dirname + '/auth-session.ts', 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
 let status = 200;
 vm.runInNewContext(source, { module, exports: module.exports, require() { return { desktopRuntime: () => desktop }; }, window, Event, URL, Headers, fetch: async (url, init) => { request = { url, init }; return { status }; } });
 return { api: module.exports, events, request: () => request, status: value => { status = value; } };
}
test('session writes use CSRF and credentials; anonymous invites omit both and supplied bearer', async () => {
 const h = harness();
 h.api.setSession({ mode: 'shared', authenticated: true, csrf_token: 'synthetic-csrf' });
 await h.api.serviceFetch('/api/v1/workflows', { method: 'POST', headers: { Authorization: 'Bearer stale' } });
 assert.equal(h.request().init.credentials, 'include');
 assert.equal(h.request().init.headers.get('X-CSRF-Token'), 'synthetic-csrf');
 assert.equal(h.request().init.headers.get('Authorization'), null);
 await h.api.serviceFetch('/api/v1/auth/invitation', { headers: { 'X-Qraft-Invitation-Token': 'synthetic-invite', 'X-CSRF-Token': 'stale' } }, true);
 assert.equal(h.request().init.credentials, 'omit');
 assert.equal(h.request().init.headers.get('X-CSRF-Token'), null);
 assert.equal(h.request().init.headers.get('X-Qraft-Invitation-Token'), 'synthetic-invite');
});
test('permission denial does not log out; one unauthorized response expires session exactly once', async () => {
 const h = harness();
 h.api.setSession({ mode: 'shared', authenticated: true, csrf_token: 'csrf' });
 h.status(403); await h.api.serviceFetch('/api/v1/admin/users');
 assert.equal(h.api.currentSession().authenticated, true);
 h.status(401); await h.api.serviceFetch('/api/v1/workflows'); await h.api.serviceFetch('/api/v1/workflows');
 assert.equal(h.api.currentSession().authenticated, false);
 assert.equal(h.events.filter(value => value === 'qraft:session-expired').length, 1);
});
test('fresh desktop makes no remote requests, including account discovery', async () => {
 const h = harness({ state: { configured: false, service_url: '', config: { mode: 'remote' } } });
 await assert.rejects(h.api.serviceFetch('/api/v1/auth/session'), /选择并保存/);
 assert.equal(h.request(), undefined);
});
test('legacy authentication fallback is restricted to the selected managed local endpoint', () => {
 for (const [mode, url, configured, allowed] of [
  ['remote', 'http://localhost:18180', true, false],
  ['local', 'https://example.com', true, false],
  ['local', 'http://127.0.0.1:18180', false, false],
  ['local', 'http://127.0.0.1:18180', true, true],
 ]) {
  const h = harness({ state: { configured, service_url: url, config: { mode } } });
  assert.equal(h.api.managedLegacyServiceAllowed(), allowed);
 }
 assert.equal(harness().api.managedLegacyServiceAllowed(), false);
});
