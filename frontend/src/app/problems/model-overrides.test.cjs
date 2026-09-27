const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');
const src = path.resolve(__dirname, '../..');

// The real components and submit handlers run with synthetic hooks; no browser
// account, password manager or model service is touched by these tests.
function page(kind, admin = true) {
  const states = [], submitted = [];
  let cursor = 0;
  const auth = { isAdmin: admin };
  const router = { push() {} };
  const generation = {
    async generate(payload) { submitted.push(JSON.parse(JSON.stringify(payload))); return { workflow_id: 'synthetic-workflow' }; },
    loading: false, error: null, reset() {},
  };
  const cache = new Map();
  function load(file) {
    if (cache.has(file)) return cache.get(file);
    const loaded = new Module(file, module);
    loaded.filename = file;
    loaded.paths = module.paths;
    loaded.require = request => {
      if (request === 'react') return {
        ...React,
        useState(initial) {
          const index = cursor++;
          if (!(index in states)) states[index] = typeof initial === 'function' ? initial() : initial;
          return [states[index], value => { states[index] = typeof value === 'function' ? value(states[index]) : value; }];
        },
        useCallback: callback => callback,
        useMemo: callback => callback(),
        useEffect() {},
      };
      if (request === 'next/navigation') return { useRouter: () => router };
      if (request === 'next/link') return { __esModule: true, default: props => React.createElement('a', props) };
      if (request === '@/components/auth/AuthProvider') return { useAuth: () => auth };
      if (request === '@/hooks/useProblems') return {
        useGenerateProblem: () => generation, useGenerateGPLTBatch: () => generation,
        useTags: () => ({ tags: [], loading: false, error: null, refetch() {} }),
      };
      if (request === '@/stores/appStore') return { useAppStore: () => ({
        selectedLevel: 'algorithm', setLevel() {}, testDataConfig: {},
      }) };
      if (request === '@/components/ui/Workspace') return {
        PageHeader: props => React.createElement('header', null, props.children, props.actions),
        FormSection: props => React.createElement('section', null, props.children),
      };
      if (request === '@/components/ApiHealthBanner' || request === '@/components/auth/GenerationReadiness') return { __esModule: true, default: () => null };
      if (request === '@/lib/utils') return { cn: (...values) => values.filter(Boolean).join(' ') };
      if (request.startsWith('@/')) return load(path.join(src, request.slice(2)) + '.ts');
      return module.require(request);
    };
    loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: {
      module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
      jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true,
    } }).outputText, file);
    cache.set(file, loaded.exports);
    return loaded.exports;
  }
  const Component = load(path.join(__dirname, kind, 'page.tsx')).default;
  function render() { cursor = 0; return Component(); }
  function elements(node, predicate, result = []) {
    if (Array.isArray(node)) node.forEach(child => elements(child, predicate, result));
    else if (React.isValidElement(node)) {
      if (predicate(node)) result.push(node);
      elements(node.props.children, predicate, result);
      elements(node.props.actions, predicate, result);
    }
    return result;
  }
  function control(predicate) {
    const found = elements(render(), predicate)[0];
    assert.ok(found, 'expected page control');
    return found;
  }
  return {
    auth, submitted, load,
    controls: predicate => elements(render(), predicate),
    toggle(enabled) { control(node => node.props.id === 'model-override-enabled').props.onChange({ target: { checked: enabled } }); },
    input(field) { return control(node => node.props.name === 'qraft-override-' + field); },
    fill(field, value) { this.input(field).props.onChange({ target: { value } }); },
    async submit() {
      control(node => node.type === 'form').props.onSubmit({ preventDefault() {} });
      await new Promise(resolve => setImmediate(resolve));
      return submitted.at(-1);
    },
    reset() { control(node => node.type === 'button' && Array.isArray(node.props.children) && node.props.children.includes('重置表单')).props.onClick(); },
    html: () => renderToStaticMarkup(render()),
  };
}

for (const kind of ['new', 'gplt']) {
  test(kind + ': default administrator form mounts no override credentials and submits only saved defaults', async () => {
    const h = page(kind);
    assert.equal(h.controls(node => node.props.name?.startsWith('qraft-override-')).length, 0);
    assert.equal(h.controls(node => node.type === 'input' && node.props.type === 'password').length, 0);
    assert.equal(Object.hasOwn(await h.submit(), 'provider_config'), false);
  });

  test(kind + ': explicitly enabled overrides submit the entered model key and use isolated autofill attributes', async () => {
    const h = page(kind); h.toggle(true);
    h.fill('statement-model', 'synthetic-model');
    h.fill('statement-api_key', 'synthetic-model-key');
    h.fill('verification-api_key', 'synthetic-verification-key');
    if (kind === 'new') h.fill('statement-provider', 'synthetic-provider');
    const key = h.input('statement-api_key');
    assert.equal(key.props.type, 'password');
    assert.equal(key.props.autoComplete, 'new-password');
    assert.match(key.props.name, /^qraft-override-/);
    assert.equal(h.input('statement-model').props.autoComplete, 'off');
    const payload = await h.submit();
    assert.equal(payload.provider_config.statement.api_key, 'synthetic-model-key');
    assert.equal(payload.provider_config.verification.api_key, 'synthetic-verification-key');
  });

  test(kind + ': disabling overrides blocks even a delayed autofill event and re-enabling starts empty', async () => {
    const h = page(kind); h.toggle(true);
    const autofill = h.input('statement-api_key').props.onChange;
    h.fill('statement-model', 'synthetic-model');
    h.fill('statement-api_key', 'synthetic-old-login-password');
    if (kind === 'new') h.fill('statement-provider', 'synthetic-member@example.test');
    h.toggle(false);
    autofill({ target: { value: 'synthetic-delayed-login-password' } });
    assert.equal(h.controls(node => node.props.name?.startsWith('qraft-override-')).length, 0);
    const payload = await h.submit();
    assert.equal(Object.hasOwn(payload, 'provider_config'), false);
    assert.doesNotMatch(JSON.stringify(payload), /synthetic-.*(?:password|member|model)/);
    h.toggle(true);
    assert.equal(h.input('statement-api_key').props.value, '');
    assert.equal(h.input('statement-model').props.value, '');
    if (kind === 'new') assert.equal(h.input('statement-provider').props.value, '');
  });

  test(kind + ': members cannot enable or serialize retained administrator overrides', async () => {
    const h = page(kind); h.toggle(true);
    h.fill('statement-api_key', 'synthetic-model-key');
    h.auth.isAdmin = false;
    assert.equal(h.controls(node => node.props.id === 'model-override-enabled').length, 0);
    assert.equal(h.controls(node => node.props.name?.startsWith('qraft-override-')).length, 0);
    assert.equal(Object.hasOwn(await h.submit(), 'provider_config'), false);
  });
}

test('resetting the single-problem form disables and removes override credentials', async () => {
  const h = page('new'); h.toggle(true);
  h.fill('statement-api_key', 'synthetic-model-key');
  h.reset();
  assert.equal(h.controls(node => node.props.name?.startsWith('qraft-override-')).length, 0);
  assert.equal(Object.hasOwn(await h.submit(), 'provider_config'), false);
});

test('provider-auth failure guidance identifies statement configuration and explains old-input retry', () => {
  const h = page('new');
  const Notice = h.load(path.join(src, 'components/workflow/ProviderFailureNotice.tsx')).default;
  const html = failure => renderToStaticMarkup(React.createElement(Notice, { failure }));
  const text = html('GenerateStatementActivity: openai-responses API returned HTTP 401: Invalid token');
  assert.match(text, /模型服务鉴权未通过/);
  assert.match(text, /生成（G）模型/);
  assert.match(text, /本次是否启用了模型覆盖/);
  assert.match(text, /原任务“重试”仍沿用该配置/);
  assert.match(text, /重新创建任务/);
  assert.equal(html('Qraft account request failed: HTTP 401'), '');
  assert.equal(html('openai-responses upstream timeout HTTP 504'), '');
  assert.equal(html('sandbox failed with exit code 401'), '');
});
