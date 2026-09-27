const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');
const root = path.resolve(__dirname, '../..');
class APIError extends Error { constructor(message, code, status) { super(message); this.code = code; this.status = status; } }
let desktop;
const cache = new Map();
function load(file) {
 file = path.resolve(file);
 if (cache.has(file)) return cache.get(file);
 const loaded = new Module(file, module); loaded.filename = file; loaded.paths = module.paths;
 loaded.require = request => {
  if (request === '@/lib/api' || (request === './api' && file.endsWith('rating-api.ts'))) return { APIError, getApiBaseUrl: () => 'https://service.invalid' };
  if (request === '@/lib/desktop-runtime' || request === './desktop-runtime') return { desktopRuntime: () => desktop };
  if (request === '@/components/MarkdownRenderer') return { __esModule: true, default: ({ content }) => React.createElement('div', null, content) };
  if (request.startsWith('@/') || request.startsWith('./')) {
   const base = request.startsWith('@/') ? path.join(root, request.slice(2)) : path.resolve(path.dirname(file), request);
   for (const ext of ['.ts', '.tsx', '']) if (fs.existsSync(base + ext)) return load(base + ext);
  }
  return module.require(request);
 };
 loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: {
  module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true,
 } }).outputText, file);
 cache.set(file, loaded.exports); return loaded.exports;
}
const form = load(path.join(__dirname, 'review-form.ts'));
const session = load(path.join(__dirname, 'review-session.ts'));
const api = load(path.join(root, 'lib/rating-api.ts'));
const report = load(path.join(__dirname, 'Report.tsx'));
const Decisions = load(path.join(__dirname, 'Decisions.tsx')).default;
const ReviewProblem = load(path.join(__dirname, 'ReviewProblem.tsx')).default;
const html = (component, props) => renderToStaticMarkup(React.createElement(component, props));
function store() {
 const data = new Map();
 return { getItem: key => data.get(key) ?? null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) };
}
test('review token is removed before tab storage and survives only the tab session', () => {
 const storage = store(), events = [];
 const wrapped = { ...storage, setItem: (key, value) => { events.push('store'); storage.setItem(key, value); } };
 const location = { hash: '#token=secret%2Btoken', pathname: '/rating/review', search: '' };
 const result = session.consumeReviewToken(location, { replaceState: (_, __, url) => events.push(url) }, wrapped);
 assert.equal(result, 'secret+token');
 assert.deepEqual(events, ['/rating/review', 'store']);
 assert.equal(session.consumeReviewToken({ ...location, hash: '' }, { replaceState: () => assert.fail() }, storage), result);
 session.clearReviewToken(storage);
 assert.equal(session.consumeReviewToken({ ...location, hash: '' }, { replaceState: () => assert.fail() }, storage), '');
});
test('a fresh token replaces the prior invitation and empty token clears prior credentials', () => {
 const storage = store(); session.saveReviewToken('old-token', storage);
 assert.equal(session.consumeReviewToken({ hash: '#token=new-token', pathname: '/rating/review', search: '' }, { replaceState() {} }, storage), 'new-token');
 assert.equal(session.consumeReviewToken({ hash: '#token=', pathname: '/rating/review', search: '' }, { replaceState() {} }, storage), '');
 assert.equal(session.consumeReviewToken({ hash: '', pathname: '/rating/review', search: '' }, { replaceState() {} }, storage), '');
});
test('disabled storage does not leave a token in the URL or lose the in-memory credential', () => {
 let replaced = false;
 const denied = { getItem() { throw Error('denied'); }, setItem() { throw Error('denied'); }, removeItem() { throw Error('denied'); } };
 assert.equal(session.consumeReviewToken({ hash: '#token=secret', pathname: '/rating/review', search: '' }, { replaceState() { replaced = true; } }, denied), 'secret');
 assert.equal(replaced, true);
});
test('public review API sends only the scoped token header, not workspace auth or URL credentials', async () => {
 desktop = undefined;
 global.window = { localStorage: { getItem: () => { throw Error('public API must not access admin storage'); } } };
 let captured;
 global.fetch = async (url, init) => { captured = { url, init }; return Response.json({ success: true, data: { revision: 1 } }); };
 await api.submitRatingFeedback('review-secret', form.emptyFeedback());
 assert.equal(captured.url, 'https://service.invalid/api/v1/public/rating/review');
 assert.equal(new Headers(captured.init.headers).get('X-Qraft-Review-Token'), 'review-secret');
 assert.equal(new Headers(captured.init.headers).get('Authorization'), null);
 assert.equal(captured.init.credentials, 'omit');
 assert.equal(captured.init.referrerPolicy, 'no-referrer');
 assert.equal(captured.init.cache, 'no-store');
 assert.ok(!captured.init.body.includes('review-secret'));
 delete global.window; delete global.fetch;
});
test('a fresh desktop cannot contact any rating service before connection selection', async () => {
 desktop = { state: { configured: false } };
 global.fetch = () => assert.fail('unconfigured desktop must not fetch');
 await assert.rejects(api.getReviewTask('token'), /先在客户端/);
 await assert.rejects(api.getRatingWorkspace('problem'), /先在客户端/);
 delete global.fetch; desktop = undefined;
});
test('old service and revoked invitation states are actionable without forced navigation', async () => {
 global.fetch = async () => new Response('old service', { status: 404 });
 await assert.rejects(api.getRatingWorkspace('p'), e => { assert.match(api.ratingError(e), /服务已更新/); return true; });
 global.fetch = async () => Response.json({ success: false }, { status: 401 });
 await assert.rejects(api.getReviewTask('token'), e => { assert.match(api.reviewError(e), /已过期、撤销或无效/); return true; });
 delete global.fetch;
});
test('feedback allows early stop and unresolved observation without turning them into failure', () => {
 for (const outcome of ['stopped', 'in_progress']) {
  const value = { ...form.emptyFeedback(), outcome, elapsed_minutes: 20, independent_minutes: 20 };
  assert.equal(form.validateFeedback(value, 60), '');
  assert.equal(form.serializeFeedback(value).observed_full_window, false);
 }
 assert.match(form.validateFeedback({ ...form.emptyFeedback(), outcome: 'unsolved', elapsed_minutes: 20, independent_minutes: 20, observed_full_window: true }, 60), /观察不足/);
});
test('feedback checks assistance order and independent versus total time', () => {
 const assisted = { ...form.emptyFeedback(), outcome: 'solved', elapsed_minutes: 70, independent_minutes: 45, assistance: ['ai'], assistance_after_minutes: 45 };
 assert.equal(form.validateFeedback(assisted, 60), '');
 assert.match(form.validateFeedback({ ...assisted, independent_minutes: 60 }, 60), /首次获得帮助/);
 assert.match(form.validateFeedback({ ...assisted, assistance_after_minutes: 80 }, 60), /首次获得/);
 assert.match(form.validateFeedback({ ...assisted, independent_minutes: 90 }, 60), /不能超过总/);
 assert.match(form.validateFeedback({ ...form.emptyFeedback(), outcome: 'not_attempted', elapsed_minutes: 10 }, 60), /未尝试/);
});
test('feedback serialization whitelists fields and does not echo hidden/admin data', () => {
 const value = { ...form.emptyFeedback(), first_route: '  DP  ', official_rating: 1900, reviewer_id: 'forged', window_minutes: 1, assistance_after_minutes: 8, result_url: 'https://unused.invalid' };
 const result = form.serializeFeedback(value);
 assert.equal(result.first_route, 'DP');
 for (const field of ['official_rating', 'reviewer_id', 'window_minutes', 'assistance_after_minutes', 'result_url']) assert.equal(Object.hasOwn(result, field), false, field);
});
const emptyWorkspace = () => ({
 subject: { problem_id: 'problem', hash: 'current', target_difficulty: 1400 }, feedback_hash: 'empty',
 assessments: [], feedback: [], calibrations: [], decisions: [],
 human: { total_reviewers: 0, effective_reviewers: 0, independent_solved: 0, window_failures: 0, censored: 0, assisted: 0, review_threshold: 30, review_triggered: false, groups: [], limitations: [] },
});
test('empty assessment shows no invented reference interval or formal value', () => {
 const output = html(report.Overview, { workspace: emptyWorkspace() });
 assert.match(output, /参照不足/);
 assert.match(output, /暂定/);
 assert.match(output, /尚无评价/);
 assert.match(output, /目标难度/);
 assert.ok(!output.includes('准确率'));
});
test('stale report is visibly historical and cannot enable administrator confirmation', () => {
 const workspace = emptyWorkspace();
 workspace.assessments = [{ id: 'old', subject: { hash: 'old' }, stale: true, report: { estimate: { lower: 1200, upper: 1800, representative: 1500, notes: [] }, models: [], summary: 'old report', limitations: [] } }];
 const output = html(report.Overview, { workspace, assessment: workspace.assessments[0] });
 assert.match(output, /不能用于当前正式评分/);
 const decisions = html(Decisions, { workspace, onRefresh: async () => {} });
 assert.match(decisions, /尚无可确认/);
 assert.match(decisions, /disabled=""[^>]*>记录管理员决定/);
});
test('reviewer problem view renders only statement, limits, samples and assignment conditions', () => {
 const task = { title: 'Synthetic sum', statement: 'Compute a + b.', time_limit: 1000, memory_limit: 256, samples: [{ input: '1 2', output: '3' }], window_minutes: 60, context: 'practice', expires_at: '2026-12-01T00:00:00Z', official_rating: 'SECRET-RATING', detailed_solution: 'SECRET-SOLUTION', kcs: ['SECRET-KC'], feedback: [{ notes: 'SECRET-OTHERS' }] };
 const output = html(ReviewProblem, { task });
 assert.match(output, /Compute a \+ b/);
 assert.match(output, /60 分钟/);
 for (const hidden of ['SECRET-RATING', 'SECRET-SOLUTION', 'SECRET-KC', 'SECRET-OTHERS']) assert.ok(!output.includes(hidden));
});

test('form labels have stable explicit associations independent of select values', () => {
 const { Field } = load(path.join(__dirname, 'Primitives.tsx'));
 const output = html(Field, { label: '作答结果', hint: '只记录本次尝试', children: React.createElement('select', null, React.createElement('option', null, '仍在尝试')) });
 const labelID = output.match(/<label id="([^"]+)"/)?.[1];
 assert.ok(labelID);
 assert.ok(output.includes('aria-labelledby="' + labelID + '"'));
 assert.match(output, /<label[^>]*>作答结果<\/label>/);
 assert.match(output, /aria-describedby=/);
});
test('unresolved assessment cannot offer direct acceptance and invalid problem cannot offer manual rating', () => {
 const workspace = emptyWorkspace();
 workspace.assessments = [{ id: 'current', status: 'completed', subject: { hash: 'current' }, stale: false, report: { validity: 'blocked', estimate: { representative: 1500 }, disagreements: [] } }];
 const output = html(Decisions, { workspace, onRefresh: async () => {} });
 assert.match(output, /修题前不能确认正式分数/);
 assert.match(output, /<option value="accept" disabled="">/);
 assert.match(output, /<option value="modify" disabled="">/);
});

test('self-reported ability is dated and oversize multibyte explanations cannot pass the form', () => {
 assert.match(form.validateFeedback({ ...form.emptyFeedback(), cf_rating: 1600 }, 60), /记录日期/);
 assert.equal(form.validateFeedback({ ...form.emptyFeedback(), cf_rating: 1600, cf_rating_at: '2025-01-01T00:00:00Z' }, 60), '');
 assert.match(form.validateFeedback({ ...form.emptyFeedback(), blockers: '汉'.repeat(3000) }, 60), /说明过长/);
});

test('unlinked human routes do not appear as measured zero observations', () => {
 const output = html(report.Paths, { assessment: { report: { paths: [{ id: 'p', name: 'candidate', kind: 'alternative', summary: 'route', complexity: 'O(n)', validation: 'candidate', kc_ids: [], bypasses: [], evidence: [], human_observations: 0 }], kcs: [] } } });
 assert.match(output, /尚未关联已核实的人类路线观察/);
 assert.ok(!output.includes('人类路线观察：0'));
});
