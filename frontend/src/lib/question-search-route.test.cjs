const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');

const file = path.join(__dirname, 'question-search-route.ts');
const loaded = new Module(file, module);
loaded.filename = file;
loaded.paths = module.paths;
loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText, file);
const { hasQuestionSearch, questionSearchHref } = loaded.exports;

test('search is available throughout question creation, libraries and sets', () => {
 for (const route of [
  '/', '/search', '/problems', '/problems/', '/problems/new', '/problems/synthetic-problem',
  '/problems/synthetic-problem/edit', '/quizzes', '/quizzes/new', '/quizzes/synthetic-quiz',
  '/problem-sets', '/problem-sets/new', '/problem-sets/assemble', '/problem-sets/synthetic-set',
 ]) assert.equal(hasQuestionSearch(route), true, route);
});

test('configuration and tools do not inherit search from a content route prefix', () => {
 for (const route of [
  '/settings', '/embedding', '/desktop/settings', '/desktop/exports', '/exports',
  '/workflows', '/workflows/synthetic-task', '/integration', '/knowledge-points',
  '/testdata-config', '/quizzes/import', '/problems/import', '/problems/hydro',
  '/problems/hydro/import', '/problems/quarantine', '/problems/gplt',
  '/quizzes/import/edit', '/problems/new/edit', '/problems/quarantine/edit',
  '/problems/synthetic-problem/unregistered-tool', '/unknown',
 ]) assert.equal(hasQuestionSearch(route), false, route);
});

test('new searches from other content pages discard unrelated list filters', () => {
 assert.equal(questionSearchHref('/quizzes', 'type=choice&page=7&subject=old', '  树 & 图  '),
  '/search?q=%E6%A0%91+%26+%E5%9B%BE');
 assert.equal(questionSearchHref('/problems', 'search=old', '   '), '/search');
});

test('editing search preserves filters and resets pagination, including clearing the query', () => {
 const query = 'q=old&kind=quiz&type=choice&tag=tree&tag=graph&knowledge=k1&difficulty=hard&page=8';
 const result = new URL(questionSearchHref('/search', query, '  new  '), 'https://qraft.invalid');
 assert.equal(result.searchParams.get('q'), 'new');
 assert.equal(result.searchParams.has('page'), false);
 assert.deepEqual(result.searchParams.getAll('tag'), ['tree', 'graph']);
 for (const [key, value] of Object.entries({kind:'quiz', type:'choice', knowledge:'k1', difficulty:'hard'})) {
  assert.equal(result.searchParams.get(key), value);
 }
 const cleared = new URL(questionSearchHref('/search', query, ''), 'https://qraft.invalid');
 assert.equal(cleared.searchParams.has('q'), false);
 assert.equal(cleared.searchParams.get('kind'), 'quiz');
 assert.equal(cleared.searchParams.has('page'), false);
 assert.equal(questionSearchHref('/search', 'q=old&page=2', ''), '/search');
});
