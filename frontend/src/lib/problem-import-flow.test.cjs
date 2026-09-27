const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const vm = require('node:vm');
const filename = path.join(__dirname, 'problem-import-flow.ts');
const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
const moduleValue = { exports: {} };
vm.runInNewContext(output, { module: moduleValue, exports: moduleValue.exports, require, URL, console, Error, TextEncoder });
const { parseSourceURLs, previewSources, resolveSelectedSources } = moduleValue.exports;

test('input supports multiple lines and rejects credentials or executable schemes', () => {
 assert.deepEqual(Array.from(parseSourceURLs('https://one.example/a\nhttps://two.example/b\n')), ['https://one.example/a', 'https://two.example/b']);
 for (const bad of ['javascript:alert(1)', 'file:///etc/passwd', 'https://user:secret@example.test/a']) assert.throws(() => parseSourceURLs(bad));
 assert.throws(() => parseSourceURLs(Array(51).fill('https://example.test/a').join('\n')));
});

test('a failed URL does not prevent a valid collection from being selected', async () => {
 const result = await previewSources(['https://bad.example', 'https://good.example'], async url => {
  if (url.includes('bad')) throw new Error('读取失败');
  return { title: '练习', kind: 'collection', final_url: url, warnings: [], items: [{ id: 'A', title: 'A', url: url + '/A' }, { id: 'B', title: 'B', url: url + '/B' }] };
 });
 assert.equal(result.items.length, 3);
 assert.equal(result.items[0].selected, false);
 assert.equal(result.items[0].error, '读取失败');
 assert.equal(result.items.filter(item => item.selected).length, 2);
});

test('selected member failures are isolated, unused members are never fetched, and body is preserved', async () => {
 const fence = String.fromCharCode(96).repeat(3);
 const original = '# Title\n\n$1 \\le n$\n\n' + fence + 'cpp\n cout << "x";\n' + fence + '\n';
 const candidates = [
  { item_id: 'a', title: 'Already loaded', statement: original, selected: true },
  { item_id: 'b', title: 'Unreadable', statement: '', source_url: 'https://bad.example/b', selected: true },
  { item_id: 'c', title: 'Skipped', statement: '', source_url: 'https://unused.example/c', selected: false },
  { item_id: 'd', title: 'Loadable', statement: '', source_url: 'https://good.example/d', selected: true },
 ];
 const fetched = [];
 const result = await resolveSelectedSources(candidates, async url => {
  fetched.push(url);
  if (url.includes('bad')) throw new Error('站点要求登录');
  return { kind: 'problem', title: 'D', items: [{ id: 'D', url, title: 'D', statement: original }] };
 });
 assert.deepEqual(fetched, ['https://bad.example/b', 'https://good.example/d']);
 assert.equal(result.items.length, 2);
 assert.equal(result.items[0].statement, original);
 assert.equal(result.items[1].statement, original);
 assert.equal(result.failures[0].item_id, 'b');
 assert.equal('selected' in result.items[0], false);
 assert.equal('provider_config' in result.items[0], false);
});

test('ambiguous collection nesting is not silently flattened or selected as one problem', async () => {
 const result = await resolveSelectedSources([{ item_id: 'a', title: 'nested', statement: '', source_url: 'https://example.test/list', selected: true }], async () => ({ kind: 'collection', title: 'list', items: [{ id: 'A', statement: 'A' }, { id: 'B', statement: 'B' }] }));
 assert.equal(result.items.length, 0);
 assert.equal(result.failures.length, 1);
});

test('collection expansion has a visible limit rather than an unbounded crawl', async () => {
 const result = await previewSources(['https://example.test/list'], async () => ({ title: 'list', kind: 'collection', items: Array.from({ length: 70 }, (_, i) => ({ id: '' + i, title: '' + i, url: 'https://example.test/p/' + i })) }));
 assert.equal(result.items.length, 50);
 assert.match(result.warnings.join(' '), /70.*50/);
});

test('oversized source is reported per item without preventing a valid submission', async () => {
 const result = await resolveSelectedSources([
  { item_id: 'large', title: 'Large', statement: '文'.repeat(50000), selected: true },
  { item_id: 'small', title: 'Small', statement: '完整题面', selected: true },
 ], async () => { throw new Error('Unexpected fetch'); });
 assert.equal(result.items.length, 1);
 assert.equal(result.items[0].item_id, 'small');
 assert.equal(result.failures[0].item_id, 'large');
});
