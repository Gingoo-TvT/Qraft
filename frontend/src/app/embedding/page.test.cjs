const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');

const versionID = '11111111-1111-4111-8111-111111111111';
const blockedReason = 'current API/worker embedding runtime does not match this endpoint; save the three-field configuration and restart runtime before activation';
const oldRuntime = { base_url: 'https://old.example/v1', model: 'old-model', dimensions: 1536, timeout_sec: 30, api_key_configured: false };
const newRuntime = { ...oldRuntime, base_url: 'https://new.example/v1', model: 'new-model', statement_model_version_id: versionID, api_key_configured: true };
const deployed = () => ({
  model_version: { id: versionID }, runtime_settings_saved: true, runtime_matches: false,
  requires_runtime_restart: true, activation_blocked_reason: blockedReason, backfill_plans: [],
  test: { ok: true, dimensions: 1536, latency_ms: 1, vector_norm: 1, sample_sha256: 'synthetic-only' },
});
const activated = oldID => ({
  runtime_matches: true, requires_runtime_restart: false,
  reports: [{ embedding_kind: 'statement', generated_at: '2026-01-01T00:00:00Z',
    decision: 'go', committed: true, operation: 'cutover', old_model_version_id: oldID,
    new_model_version_id: versionID }],
});

// Execute the real page handlers with a small hook host; network and layout
// components are synthetic, and React renders the real result badges.
function page() {
  const states = [], effects = [], calls = [];
  let cursor = 0, started = false;
  const api = {
    runtime: oldRuntime, saved: { configured: false }, activation: activated('previous-version'),
    async getEmbeddingStatus() { return { data: [] }; },
    async getEmbeddingRuntimeSettings() { return { data: await api.runtime }; },
    async getSavedEmbeddingRuntimeSettings() { return { data: api.saved }; },
    async deployLocalEmbeddingModel(payload) { calls.push(['deploy', payload]); return { data: deployed() }; },
    async activateLocalEmbeddingModel(payload) { calls.push(['activate', payload]); return { data: api.activation }; },
  };
  const file = path.join(__dirname, 'page.tsx');
  const loaded = new Module(file, module);
  loaded.filename = file;
  loaded.paths = module.paths;
  loaded.require = request => {
    if (request === 'react') return {
      ...React,
      useState(initial) {
        const index = cursor++;
        if (!(index in states)) states[index] = initial;
        return [states[index], value => { states[index] = typeof value === 'function' ? value(states[index]) : value; }];
      },
      useCallback: callback => callback,
      useMemo: callback => callback(),
      useEffect: callback => { if (!started) effects.push(callback); },
    };
    if (request === '@/lib/api') return api;
    if (request === '@/lib/utils') return { cn: (...classes) => classes.filter(Boolean).join(' ') };
    if (request === '@/components/ui/Workspace') return {
      PageHeader: props => React.createElement('header', null, props.title, props.actions),
      SectionHeading: props => React.createElement('h2', null, props.title, props.description),
    };
    return module.require(request);
  };
  loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: {
    module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
    jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true,
  } }).outputText, file);
  function render() { cursor = 0; return loaded.exports.default(); }
  function elements(node, predicate, result = []) {
    if (Array.isArray(node)) node.forEach(value => elements(value, predicate, result));
    else if (React.isValidElement(node)) {
      if (predicate(node)) result.push(node);
      elements(node.props.children, predicate, result);
      elements(node.props.actions, predicate, result);
    }
    return result;
  }
  function find(predicate) {
    const match = elements(render(), predicate)[0];
    assert.ok(match, 'expected page control');
    return match;
  }
  return {
    api, calls,
    begin() { render(); started = true; effects.splice(0).forEach(effect => effect()); },
    settle: () => new Promise(resolve => setImmediate(resolve)),
    input: id => find(node => node.type === 'input' && node.props.id === id),
    change(id, value) { this.input(id).props.onChange({ target: { value } }); },
    async click(label) {
      const button = find(node => node.props.label === label || (node.type === 'button' && String(node.props.children).includes(label)));
      assert.ok(!button.props.disabled, label + ' should be available');
      await button.props.onClick();
    },
    html: () => renderToStaticMarkup(render()),
  };
}

test('saving a different endpoint preserves the saved form while the API still reports the old runtime', async () => {
  const h = page(); h.begin(); await h.settle();
  h.change('base-url', newRuntime.base_url);
  h.change('endpoint-model', newRuntime.model);
  h.change('api-key', 'synthetic-only-key');
  await h.click('部署并启用');
  assert.equal(h.input('base-url').props.value, newRuntime.base_url);
  assert.equal(h.input('endpoint-model').props.value, newRuntime.model);
  assert.equal(h.input('api-key').props.value, '');
  assert.equal(h.calls[0][1].endpoint.model, newRuntime.model);
  assert.match(h.html(), /配置已保存，但 API 的运行配置尚未同步/);
  assert.match(h.html(), /重新创建 API 与 worker/);
  assert.doesNotMatch(h.html().split('部署响应')[0], /current API\/worker embedding runtime/);
});

test('status refresh preserves unsubmitted edits, clears obsolete warnings and retains the registered version', async () => {
  const h = page(); h.begin(); await h.settle();
  await h.click('部署并启用');
  h.change('base-url', 'https://draft.example/v1');
  h.change('endpoint-model', 'draft-model');
  await h.click('刷新状态');
  assert.equal(h.input('base-url').props.value, 'https://draft.example/v1');
  assert.equal(h.input('endpoint-model').props.value, 'draft-model');
  assert.doesNotMatch(h.html(), /运行配置尚未同步|需要同步运行配置/);
  await h.click('正式启用');
  assert.equal(h.calls.at(-1)[1].model_version_id, versionID);
});

test('successful activation clears the earlier deployment warning and labels first activation', async () => {
  for (const oldID of [null, '', '00000000-0000-0000-0000-000000000000']) {
    const h = page(); h.begin(); await h.settle();
    await h.click('部署并启用');
    h.api.runtime = newRuntime;
    h.api.activation = activated(oldID);
    await h.click('正式启用');
    assert.doesNotMatch(h.html(), /运行配置尚未同步|需要同步运行配置/);
    assert.match(h.html(), /首次启用/);
    assert.match(h.html(), /已生效/);
  }
});

test('an empty activation report is not displayed as a successful check', async () => {
  const h = page(); h.api.runtime = newRuntime; h.begin(); await h.settle();
  h.api.activation = { runtime_matches: true, requires_runtime_restart: false, reports: [] };
  await h.click('预演启用');
  assert.match(h.html(), /bg-warning[^<]*">尚未完成启用检查/);
  assert.doesNotMatch(h.html(), /bg-success[^<]*">尚未完成启用检查/);
});

test('initial status response cannot overwrite edits made while it was loading', async () => {
  let finish;
  const h = page();
  h.api.runtime = new Promise(resolve => { finish = resolve; });
  h.begin();
  h.change('base-url', 'https://typed.example/v1');
  h.change('endpoint-model', 'typed-model');
  finish(oldRuntime); await h.settle();
  assert.equal(h.input('base-url').props.value, 'https://typed.example/v1');
  assert.equal(h.input('endpoint-model').props.value, 'typed-model');
});

test('a full page load restores saved settings before runtime synchronization, without asking for the key again', async () => {
  const h = page();
  h.api.saved = { configured: true, base_url: newRuntime.base_url, model: newRuntime.model, model_version_id: versionID };
  h.begin(); await h.settle();
  assert.equal(h.input('base-url').props.value, newRuntime.base_url);
  assert.equal(h.input('endpoint-model').props.value, newRuntime.model);
  assert.match(h.html(), /如已保存密钥，无需重复填写/);
  await h.click('正式启用');
  assert.equal(h.calls.at(-1)[1].model_version_id, versionID);
});
