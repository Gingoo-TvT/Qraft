const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const file = path.join(__dirname, 'question-search-state.ts');
const loaded = new Module(file, module);
loaded.filename = file;
loaded.paths = module.paths;
loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText, file);
const { parseQuestionSearch, questionResultsHref, questionDetailHref } = loaded.exports;
const parse = text => parseQuestionSearch(new URLSearchParams(text));

test('query roundtrip preserves leading zero IDs, Unicode, literal wildcards and combined filters', () => {
 const filter = {q:'000123 中文_% &+', type:'choice', tag:'图&树', knowledge_point:'01_图', quiz_difficulty:'hard', page:3, size:20};
 const href = questionResultsHref(filter);
 assert.deepEqual(parse(href.split('?')[1]), {...filter, min_difficulty:undefined, max_difficulty:undefined});
});

test('URL state retains native difficulty scales and ignores invalid pagination', () => {
 const numeric = parse('min_difficulty=800&max_difficulty=1500&page=-1&size=999');
 assert.equal(numeric.min_difficulty, 800);
 assert.equal(numeric.max_difficulty, 1500);
 assert.equal(numeric.page, 1);
 assert.equal(numeric.size, 20);
 const quiz = parse('min_difficulty=800&quiz_difficulty=easy&type=judge&page=2');
 assert.equal(quiz.min_difficulty, undefined);
 assert.equal(quiz.quiz_difficulty, 'easy');
 assert.equal(quiz.type, 'judge');
 assert.equal(quiz.page, 2);
 assert.equal(parse('page=1e2&size=1.5&type=wrong&quiz_difficulty=wrong').type, undefined);
 assert.equal(parse('page=1000001').page, 1);
});

test('filter changes can reset pagination without losing the query or changing string identifiers', () => {
 const before = parse('q=0001&tag=old&page=7&size=50');
 const after = new URL(questionResultsHref({...before, tag:'new', page:1}), 'https://qraft.invalid');
 assert.equal(after.searchParams.has('page'), false);
 assert.equal(after.searchParams.get('q'), '0001');
 assert.equal(after.searchParams.get('size'), '50');
 assert.equal(after.searchParams.get('tag'), 'new');
});

test('detail links depend on source even when both libraries contain programming questions or the same ID', () => {
 const id = '00000000-0000-0000-0000-000000000123';
 assert.equal(questionDetailHref({source:'problem',type:'programming',id}), '/problems/'+id);
 assert.equal(questionDetailHref({source:'quiz',type:'programming',id}), '/quizzes/'+id);
});
