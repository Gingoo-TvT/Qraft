const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const React = require('react');
const src = path.resolve(__dirname, '../../..');

function nodes(node, predicate, result = []) {
  if (Array.isArray(node)) node.forEach(child => nodes(child, predicate, result));
  else if (React.isValidElement(node)) {
    if (predicate(node)) result.push(node);
    nodes(node.props.children, predicate, result);
    nodes(node.props.actions, predicate, result);
  }
  return result;
}
function text(node) {
  if (Array.isArray(node)) return node.map(text).join('');
  if (React.isValidElement(node)) return text(node.props.children);
  return typeof node === 'string' || typeof node === 'number' ? String(node) : '';
}
const sameDeps = (a, b) => a && b && a.length === b.length && a.every((value, i) => Object.is(value, b[i]));

// Run real handlers/effects with synthetic hooks and transport; no browser,
// account or server is opened. Timers are retained in memory and cleaned up.
function harness(options = {}) {
  const hooks = [], effects = [], timers = new Map(), cache = new Map();
  const submitted = [], reads = [], previews = [], resumed = [], downloads = [];
  let cursor = 0, timerID = 0, dirty = true, tree, Component;
  let location = new URL(options.url || 'https://qraft.example/problems/import');
  const env = {
    window: {
      get location() { return location; },
      history: { replaceState(_state, _unused, target) { location = new URL(target, location); } },
      confirm: () => true,
    },
    setTimeout(callback) { const id = ++timerID; timers.set(id, callback); return id; },
    clearTimeout(id) { timers.delete(id); },
  };
  env.window.setInterval = env.setTimeout;
  env.window.clearInterval = env.clearTimeout;
  const auth = { isAdmin: options.admin ?? true, session: { user: { id: 'synthetic-user' } } };
  const importAPI = {
    importActive: status => ['pending', 'queued', 'running'].includes(status),
    async previewProblemSource(url) {
      previews.push(url);
      if (!options.preview) throw new Error('Unexpected source request');
      return options.preview(url);
    },
    async resumeProblemImport(id) {
      resumed.push(id);
      return options.resume ? options.resume(id) : { workflow_id: 'resumed-import' };
    },
    async startProblemImport(payload) {
      submitted.push(structuredClone(payload));
      return options.start ? options.start(payload) : { workflow_id: 'synthetic-import' };
    },
    async getProblemImport(id) {
      reads.push(id);
      return options.report ? options.report(id) : { workflow_id: id, status: 'completed', items: [] };
    },
  };
  const react = {
    ...React,
    useState(initial) {
      const index = cursor++;
      if (!(index in hooks)) hooks[index] = { value: typeof initial === 'function' ? initial() : initial };
      return [hooks[index].value, value => {
        const next = typeof value === 'function' ? value(hooks[index].value) : value;
        if (!Object.is(next, hooks[index].value)) { hooks[index].value = next; dirty = true; }
      }];
    },
    useRef(initial) {
      const index = cursor++;
      if (!(index in hooks)) hooks[index] = { current: initial };
      return hooks[index];
    },
    useMemo(callback, deps) {
      const index = cursor++;
      if (!hooks[index] || !sameDeps(hooks[index].deps, deps)) hooks[index] = { value: callback(), deps };
      return hooks[index].value;
    },
    useCallback(callback, deps) { return react.useMemo(() => callback, deps); },
    useEffect(callback, deps) {
      const index = cursor++;
      if (!hooks[index] || !sameDeps(hooks[index].deps, deps)) {
        const old = hooks[index];
        hooks[index] = { deps, cleanup: old?.cleanup };
        effects.push(() => { old?.cleanup?.(); hooks[index].cleanup = callback(); });
      }
    },
  };
  const simple = tag => props => React.createElement(tag, null, props.children, props.actions);
  function load(file) {
    if (cache.has(file)) return cache.get(file);
    const loaded = new Module(file, module);
    loaded.filename = file; loaded.paths = module.paths;
    loaded.require = request => {
      if (request === '__test_environment__') return env;
      if (request === 'react') return react;
      if (request === 'next/navigation') return { useParams: () => ({ id: options.setID || 'synthetic-set' }) };
      if (request === 'next/link') return { __esModule: true, default: simple('a') };
      if (request === '@/components/auth/AuthProvider') return { useAuth: () => auth };
      if (request === '@/components/ui/Workspace') return { PageHeader: simple('header'), SectionHeading: simple('h2') };
      if (request === '@/components/ui/ViewTabs') return { ViewTabs: () => null, ViewPanel: simple('section') };
      if (request === '@/components/problem-sets/GenerationConfigFields') return { __esModule: true, default: () => null };
      if (request === '@/lib/problem-import-api') return importAPI;
      if (request === '@/lib/problem-set-download') return {
        async downloadProblemSetTesting(id, format) { downloads.push({ id, format }); return { status: 'completed' }; },
        parseExportTagCatalog: JSON.parse,
      };
      if (request === './desktop-runtime') return { desktopRuntime: () => options.desktop || null };
      if (request === './auth-session') return { SESSION_EXPIRED: 'expired', serviceFetch: async () => { throw new Error('Unexpected network request'); } };
      if (request === './utils') return { buildQueryString: () => '' };
      if (request === '@/lib/api') return {
        ...load(path.join(src, 'lib/api.ts')),
        async getProblemSet() { return { data: options.set }; },
        async listProblems() { return { data: [] }; },
        async listQuizzes() { return { data: [] }; },
        async getProblemSetGeneration() { return { data: options.set?.generation }; },
      };
      if (request.startsWith('@/')) {
        const base = path.join(src, request.slice(2));
        return load(fs.existsSync(base + '.ts') ? base + '.ts' : base + '.tsx');
      }
      return module.require(request);
    };
    const compiled = ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: {
      module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
      jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true,
    } }).outputText;
    loaded._compile('const { window, setTimeout, clearTimeout } = require("__test_environment__");\n' + compiled, file);
    cache.set(file, loaded.exports);
    return loaded.exports;
  }
  function render() { cursor = 0; dirty = false; tree = Component(); return tree; }
  async function flush() {
    for (let n = 0; n < 20; n++) {
      if (dirty) render();
      while (effects.length) effects.shift()();
      await new Promise(resolve => setImmediate(resolve));
      if (!dirty && !effects.length) return;
    }
    assert.fail('component effects did not settle');
  }
  function control(predicate) {
    if (dirty) render();
    const found = nodes(tree, predicate)[0];
    assert.ok(found, 'expected page control');
    return found;
  }
  function labelled(label) {
    const parent = control(node => node.type === 'label' && text(node).includes(label));
    const input = nodes(parent, node => ['input', 'textarea'].includes(node.type))[0];
    assert.ok(input, 'expected input for ' + label);
    return input;
  }
  return {
    submitted, reads, previews, resumed, downloads, timers, load, auth,
    async mount(file = 'app/problems/import/page.tsx') { Component = load(path.join(src, file)).default; await flush(); },
    async click(label) { control(node => node.type === 'button' && text(node) === label).props.onClick(); await flush(); },
    async change(label, value) {
      const input = labelled(label);
      input.props.onChange({ target: input.props.type === 'checkbox' ? { checked: value } : { value } });
      await flush();
    },
    async radio(label) { labelled(label).props.onChange(); await flush(); },
    control,
    all(predicate) { if (dirty) render(); return nodes(tree, predicate); },
    text() { if (dirty) render(); return text(tree); },
    get url() { return location; },
    async tick() { const pending = [...timers.values()]; timers.clear(); pending.forEach(callback => callback()); await flush(); },
    dispose() { hooks.forEach(hook => hook?.cleanup?.()); timers.clear(); assert.equal(timers.size, 0); },
  };
}
async function addText(h, title = '合成题目', statement = '  # 题面\n\n输入 \x60n\x60，输出 $n+1$。\n') {
  await h.click('粘贴题面');
  await h.change('题目标题', title);
  await h.change('完整题面（Markdown）', statement);
  await h.click('加入清单');
}

test('preserved import submits original Markdown and set settings without model overrides', async t => {
  const h = harness(); t.after(() => h.dispose()); await h.mount();
  const statement = '  # 不改写题面\n\n| n | answer |\n| - | - |\n| 1 | 2 |\n\n\x60\x60\x60cpp\n// $n$ stays code\n\x60\x60\x60\n  ';
  await addText(h, '原题', statement);
  await h.change('题集名称', '  合成测试题集  ');
  await h.click('开始处理');
  assert.equal(h.submitted.length, 1);
  const payload = h.submitted[0];
  assert.equal(payload.mode, 'preserve_statement');
  assert.equal(payload.items[0].statement, statement);
  assert.equal(payload.create_set, true);
  assert.equal(payload.title, '合成测试题集');
  assert.equal(payload.language, 'cpp');
  assert.equal(payload.locale, 'zh');
  assert.equal(Object.hasOwn(payload, 'provider_config'), false);
  assert.equal(Object.hasOwn(payload, 'difficulty'), false);
  assert.equal(h.url.searchParams.get('workflow'), 'synthetic-import');
});

test('inspiration uses selected difficulty and can import without creating a set', async t => {
  const h = harness(); t.after(() => h.dispose()); await h.mount();
  await addText(h); await h.radio('作为创意生成');
  await h.change('目标难度', '1800');
  await h.change('将成功题目汇成题集', false);
  await h.click('开始处理');
  const payload = h.submitted[0];
  assert.equal(payload.mode, 'inspiration');
  assert.equal(payload.difficulty, 1800);
  assert.equal(payload.create_set, false);
  assert.equal(Object.hasOwn(payload, 'title'), false);
  assert.equal(Object.hasOwn(payload, 'provider_config'), false);
});

test('failed collection member does not prevent submitting other selected statements', async t => {
  const root = 'https://problems.example/contest/1', good = root + '/a', bad = root + '/b';
  const statement = '# A\n\n保留空格与 $a_i$。\n';
  const h = harness({ preview: async url => {
    if (url === root) return { url, final_url: url, title: '合成题集', kind: 'collection', items: [
      { id: 'a', url: good, title: 'A' }, { id: 'b', url: bad, title: 'B' },
    ] };
    if (url === bad) throw new Error('该题需要登录，请粘贴题面');
    return { url, final_url: url, title: 'A', kind: 'problem', items: [{ id: 'a', url, title: 'A', statement }] };
  } });
  t.after(() => h.dispose()); await h.mount();
  await h.change('公开题目、题集或创意文章链接', root);
  await h.click('解析链接'); await h.click('开始处理');
  assert.deepEqual(h.previews.sort(), [root, good, bad].sort());
  assert.equal(h.submitted.length, 1);
  assert.equal(h.submitted[0].items.length, 1);
  assert.equal(h.submitted[0].items[0].statement, statement);
  assert.equal(h.submitted[0].items[0].source_url, good);
  assert.equal(h.submitted[0].items[0].source_id, 'a');
  assert.equal(Object.hasOwn(h.submitted[0].items[0], 'selected'), false);
  assert.equal(Object.hasOwn(h.submitted[0], 'provider_config'), false);
  assert.match(h.text(), /该题需要登录，请粘贴题面/);
  assert.ok(h.text().includes(bad), 'failed source URL stays visible after submission');
});

test('refresh restores server report and result links without submitting another import', async t => {
  const h = harness({ url: 'https://qraft.example/problems/import?workflow=restored-import', report: async id => ({
    workflow_id: id, status: 'completed', problem_set_id: 'restored-set', items: [
      { item_id: 'a', title: '成功', status: 'imported', problem_id: 'problem-a', rating_assessment_id: 'rating-a' },
      { item_id: 'b', title: '重复', status: 'skipped_duplicate', duplicate_of: 'problem-old' },
      { item_id: 'c', title: '评估失败', status: 'assessment_failed', problem_id: 'problem-c', error: '合成评估错误' },
    ],
  }) });
  t.after(() => h.dispose()); await h.mount();
  assert.deepEqual(h.reads, ['restored-import']);
  assert.equal(h.submitted.length, 0);
  assert.match(h.text(), /1 项已导入 · 1 项重复跳过 · 1 项需处理/);
  const links = h.all(node => node.props.href).map(node => node.props.href);
  assert.ok(links.includes('/problem-sets/restored-set'));
  assert.ok(links.includes('/problems/problem-c'));
  assert.ok(links.includes('/rating?problem=problem-a'));
  assert.equal(h.timers.size, 0);
  await h.click('新建导入');
  assert.equal(h.url.searchParams.has('workflow'), false);
  assert.ok(h.all(node => node.type === 'button' && text(node) === '开始处理').length);
});

test('temporary progress failures retry and recover without resubmitting the batch', async t => {
  let attempts = 0;
  const h = harness({ url: 'https://qraft.example/problems/import?workflow=retry-import', report: async id => {
    if (++attempts === 1) throw new Error('临时断网');
    return { workflow_id: id, status: 'completed', items: [] };
  } });
  t.after(() => h.dispose()); await h.mount();
  assert.match(h.text(), /临时断网/); assert.equal(h.timers.size, 1);
  await h.tick();
  assert.equal(attempts, 2); assert.equal(h.submitted.length, 0);
  assert.doesNotMatch(h.text(), /临时断网/); assert.equal(h.timers.size, 0);
});

function syntheticSet(items, generation) {
  return { id: 'synthetic-set', code: 'SYNTHETIC', title: '合成题集', description: '', subject: '',
    kind: 'contest', status: 'draft', visibility: 'team', tags: [], style_prompt: '', difficulty_prompt: '',
    cooldown_sets: 2, total_score: 100, desired_item_count: 1, items, generation };
}
const programming = { id: 'item-a', problem_id: 'problem-a', position: 1, score: 100 };
for (const [name, items, generation, genericEnabled, hydroEnabled] of [
  ['programming set', [programming], undefined, true, true],
  ['mixed set', [programming, { id: 'item-b', quiz_id: 'quiz-b', quiz: { type: 'choice', title: '选择题' }, position: 2, score: 2 }], undefined, true, false],
  ['empty set', [], undefined, false, false],
  ['generating set', [programming], { status: 'generating', slots: [] }, false, false],
]) {
  test(name + ' exposes compatible ZIP formats using actual API item shape', async t => {
    const h = harness({ admin: false, set: syntheticSet(items, generation) });
    t.after(() => h.dispose()); await h.mount('app/problem-sets/[id]/page.tsx');
    for (const [label, format, enabled] of [['通用 ZIP', 'generic', genericEnabled], ['Hydro ZIP', 'hydro', hydroEnabled]]) {
      const button = h.control(node => node.type === 'button' && text(node) === label);
      assert.equal(Boolean(button.props.disabled), !enabled, label);
      if (enabled) {
        await h.click(label);
        assert.deepEqual(h.downloads.at(-1), { id: 'synthetic-set', format });
      }
    }
  });
}

test('both testing ZIP formats use desktop proxy and encode IDs', t => {
  const h = harness({ desktop: { api_base: 'http://127.0.0.1:32123/service' } }); t.after(() => h.dispose());
  const api = h.load(path.join(src, 'lib/api.ts'));
  for (const format of ['generic', 'hydro']) assert.equal(api.exportProblemSetTestingURL('id with/slash', format),
    'http://127.0.0.1:32123/service/api/v1/problem-sets/id%20with%2Fslash/export.zip?mode=testing&format=' + format);
});

test('desktop import route precedes the problem detail wildcard', t => {
  const h = harness(); t.after(() => h.dispose());
  const { pages, routePage } = h.load(path.join(src, 'desktop/routes.tsx'));
  assert.ok(pages['/problems/import']);
  assert.equal(routePage('/problems/import'), pages['/problems/import']);
  assert.notEqual(routePage('/problems/import'), routePage('/problems/synthetic-id'));
});


for (const status of [400, 403, 404]) {
  test('unavailable restored workflow (' + status + ') stops polling and offers a usable new import', async t => {
    let h;
    h = harness({ url: 'https://qraft.example/problems/import?workflow=unavailable', report: async () => {
      const { APIError } = h.load(path.join(src, 'lib/api.ts'));
      throw new APIError('无法访问此导入任务', 'SYNTHETIC_NOT_FOUND', status);
    } });
    t.after(() => h.dispose()); await h.mount();
    assert.match(h.text(), /无法访问此导入任务/);
    assert.equal(h.timers.size, 0);
    assert.equal(h.reads.length, 1);
    await h.click('返回新建导入');
    assert.equal(h.url.searchParams.has('workflow'), false);
    await addText(h);
    await h.click('开始处理');
    assert.equal(h.submitted.length, 1, 'returning restores a working submit form');
  });
}

test('adding link previews retains pasted statements and previously selected entries', async t => {
  const h = harness({ preview: async url => ({
    url, final_url: url, title: '链接题', kind: 'problem',
    items: [{ id: url, url, title: url.endsWith('/one') ? '链接一' : '链接二', statement: '# ' + url }],
  }) });
  t.after(() => h.dispose()); await h.mount();
  const body = '  # 粘贴题面\n\n$n$，原样保存。\n';
  await addText(h, '手动题', body);
  await h.click('外部链接');
  for (const url of ['https://problems.example/one', 'https://problems.example/two']) {
    await h.change('公开题目、题集或创意文章链接', url);
    await h.click('解析链接');
  }
  await h.click('开始处理');
  assert.equal(h.submitted[0].items.length, 3);
  assert.equal(h.submitted[0].items[0].statement, body);
  assert.deepEqual(h.submitted[0].items.map(item => item.title), ['手动题', '链接一', '链接二']);
});

test('retrying an unread source submits only that source and clears its obsolete failure', async t => {
  const root = 'https://problems.example/retry', good = root + '/a', bad = root + '/b';
  let badReads = 0;
  const h = harness({ preview: async url => {
    if (url === root) return { url, final_url: url, title: '重试题集', kind: 'collection', items: [
      { id: 'a', url: good, title: 'A' }, { id: 'b', url: bad, title: 'B' },
    ] };
    if (url === bad && ++badReads === 1) throw new Error('临时来源故障');
    return { url, final_url: url, title: '已恢复', kind: 'problem', items: [{ id: url, url, title: '已恢复', statement: '# 题面 ' + url }] };
  } });
  t.after(() => h.dispose()); await h.mount();
  await h.change('公开题目、题集或创意文章链接', root);
  await h.click('解析链接'); await h.click('开始处理');
  assert.deepEqual(h.submitted[0].items.map(item => item.source_url), [good]);
  await h.click('单独重试未读取的来源');
  await h.click('开始处理');
  assert.equal(h.submitted.length, 2);
  assert.deepEqual(h.submitted[1].items.map(item => item.source_url), [bad]);
  assert.equal(h.all(node => node.props['aria-label'] === '未提交的来源').length, 0);
  assert.doesNotMatch(h.text(), /临时来源故障/);
});


test('a rejected start preserves entered statements for correction and retry', async t => {
  let starts = 0;
  const h = harness({ start: async () => {
    if (++starts === 1) throw new Error('暂时无法创建任务');
    return { workflow_id: 'retried-start' };
  } });
  t.after(() => h.dispose()); await h.mount();
  const body = '# 不应丢失\n\n保留题面。\n';
  await addText(h, '可重试', body);
  await h.click('开始处理');
  assert.match(h.text(), /暂时无法创建任务/);
  assert.equal(h.url.searchParams.has('workflow'), false);
  await h.click('开始处理');
  assert.equal(h.submitted.length, 2);
  assert.equal(h.submitted[1].items[0].statement, body);
  assert.equal(h.url.searchParams.get('workflow'), 'retried-start');
});

test('deselected members are never fetched or included in the submitted batch', async t => {
  const root = 'https://problems.example/select', ignored = root + '/skip';
  const h = harness({ preview: async url => {
    assert.notEqual(url, ignored, 'deselected source must not be contacted');
    if (url === root) return { url, final_url: url, title: '选择题集', kind: 'collection', items: [
      { id: 'keep', url: root + '/keep', title: '保留题', statement: '# 已预览正文' },
      { id: 'skip', url: ignored, title: '不选这题' },
    ] };
    throw new Error('Unexpected fetch');
  } });
  t.after(() => h.dispose()); await h.mount();
  await h.change('公开题目、题集或创意文章链接', root);
  await h.click('解析链接');
  await h.change('不选这题', false);
  await h.click('开始处理');
  assert.deepEqual(h.previews, [root]);
  assert.deepEqual(h.submitted[0].items.map(item => item.source_id), ['keep']);
});


test('resuming a terminal import reuses server-side results and follows the new batch', async t => {
  const h = harness({ url: 'https://qraft.example/problems/import?workflow=old-import', report: async id => ({
    workflow_id: id, status: id === 'old-import' ? 'completed_with_errors' : 'running',
    items: [{ item_id: 'a', title: '已入库', status: 'imported', problem_id: 'stored-a' },
      { item_id: 'b', title: '失败项', status: 'failed', error: 'InvalidImportModelResponse' }],
  }) });
  t.after(() => h.dispose()); await h.mount();
  await h.click('继续未完成项目');
  assert.deepEqual(h.resumed, ['old-import']);
  assert.equal(h.url.searchParams.get('workflow'), 'resumed-import');
  assert.ok(h.reads.includes('resumed-import'));
  assert.equal(h.submitted.length, 0);
  assert.equal(h.all(n => n.type === 'button' && text(n) === '继续未完成项目').length, 0);
});


test('remove bad entries and replace a link with pasted text without losing the rest', async () => {
  const h = harness({ preview: async url => ({ kind: 'collection', url, items: [
    { id: 'a', title: 'Bad', url: 'https://source.example/a' },
    { id: 'b', title: 'Replace', url: 'https://source.example/b' },
    { id: 'c', title: 'Keep', url: 'https://source.example/c', statement: 'Keep original' },
  ] }) });
  try {
    await h.mount();
    await h.change('公开题目、题集或创意文章链接', 'https://source.example/contest');
    await h.click('解析链接');
    h.control(node => node.props['aria-label'] === '删除 Bad').props.onClick();
    await h.click('粘贴替换');
    await h.change('题目标题', 'Fixed');
    await h.change('完整题面', '  Pasted with examples\n');
    assert.equal(h.control(node => node.type === 'button' && text(node) === '开始处理').props.disabled, true);
    await h.click('保存替换');
    await h.click('开始处理');
    assert.deepEqual(h.submitted[0].items.map(item => item.title), ['Fixed', 'Keep']);
    assert.equal(h.submitted[0].items[0].source_url, 'https://source.example/b');
    assert.equal(h.submitted[0].items[0].statement, '  Pasted with examples\n');
    assert.deepEqual(h.previews, ['https://source.example/contest']);
  } finally { h.dispose(); }
});
