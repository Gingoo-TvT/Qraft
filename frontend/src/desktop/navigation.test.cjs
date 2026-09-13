const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');

let currentPath = '/';
let currentDesktop;
const marker = name => () => React.createElement('section', { 'data-test-page': name }, name);
const businessPage = () => React.createElement('section', { 'data-test-page': 'business' }, currentPath);
const noop = () => {};
const link = ({ children, ...props }) => React.createElement('a', props, children);

// Keep App's real route selection and layout. Only replace page bodies and
// native/browser dependencies so this test needs neither a service nor a DOM.
const mocks = {
 './Home': marker('home'),
 './Welcome': marker('welcome'),
 './Settings': marker('settings'),
 './Exports': marker('exports'),
 './ThemeQuickPicker': () => null,
 '@/components/layout/Header': () => null,
 '@/components/layout/Sidebar': () => null,
 '@/components/layout/NotificationToaster': { NotificationToaster: () => null },
 './router': { __esModule: true, default: link, navigate: noop, usePathname: () => currentPath },
 './routes': { routePage: route => ['/','/desktop/settings','/desktop/exports'].includes(route) ? null : businessPage },
 './runtime': {
  useDesktop: () => currentDesktop,
  serviceURL: state => state.service_url,
  boot: () => ({ api_base: '/bridge', ui_base: '/ui' }),
  nativeRequest: () => { throw new Error('Server rendering must not call the native bridge'); },
 },
 './startup-path': { startupPath: route => route },
};
const file = path.join(__dirname, 'App.tsx');
const loaded = new Module(file, module);
loaded.filename = file;
loaded.paths = module.paths;
loaded.require = request => Object.hasOwn(mocks, request) ? mocks[request] : module.require(request);
loaded._compile(ts.transpileModule(fs.readFileSync(process.env.QRAFT_APP_SOURCE || file, 'utf8'), {
 compilerOptions: {
  module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
  jsx: ts.JsxEmit.React, esModuleInterop: true,
 },
}).outputText, file);
const App = loaded.exports.default;

function render(route, { mode = 'local', configured = true, ready = false, busy = false, url = '' } = {}) {
 currentPath = route;
 currentDesktop = {
  state: {
   configured, service_url: url,
   version: '2.2.0', packaged: true, data_dir: '/synthetic-client',
   config: { schema_version: 1, mode, server_url: mode === 'remote' ? url : '', local_port: 18180 },
   operation: { busy, action: busy ? 'start' : '', message: busy ? '正在启动服务' : '', error: '', updated: '' },
  },
  preferences: { theme: 'light', color_theme: 'graphite', sidebar_collapsed: false },
  connection: url ? { ready, url, release_version: ready ? '2.2.0' : '', problem_sets: ready, error: ready ? '' : '服务暂时无法连接' } : null,
  probing: false, notice: '', setNotice: noop, updatePreferences: noop, refreshState: async () => {},
 };
 return renderToStaticMarkup(React.createElement(App));
}

function assertPage(html, expected, route) {
 assert.ok(html.includes('data-test-page="' + expected + '"'), route + ' must render ' + expected);
 if (expected !== 'settings') assert.ok(!html.includes('data-test-page="settings"'), route + ' must not become client settings');
}

const businessRoutes = [
 '/problems', '/problems/new', '/quizzes', '/quizzes/new',
 '/problem-sets', '/problem-sets/new', '/problem-sets/assemble',
 '/settings', '/embedding', '/search', '/workflows',
];

test('local backend not started preserves home and each business destination', () => {
 assertPage(render('/'), 'home', '/');
 for (const route of businessRoutes) assertPage(render(route), 'business', route);
});

test('starting the local backend does not replace pages with settings', () => {
 assertPage(render('/', { busy: true }), 'home', '/');
 for (const route of businessRoutes) assertPage(render(route, { busy: true }), 'business', route);
});

test('only the client settings route shows settings; local exports remain available', () => {
 assertPage(render('/desktop/settings'), 'settings', '/desktop/settings');
 assertPage(render('/desktop/exports'), 'exports', '/desktop/exports');
});

test('configured but offline remote service preserves business pages', () => {
 const offline = { mode: 'remote', url: 'https://qraft.invalid' };
 assertPage(render('/', offline), 'home', '/');
 for (const route of businessRoutes) assertPage(render(route, offline), 'business', route);
});

test('local pages without a service endpoint explain setup and link to the backend controls', () => {
 for (const options of [{}, { busy: true }]) {
  const target = '/desktop/settings?tab=local';
  const html = render('/problems/new', options);
  const main = html.match(/<main\b[^>]*>([\s\S]*?)<\/main>/)?.[1] || '';
  assert.match(main, /role="(?:status|alert)"/, 'connection notice belongs in the page content');
  assert.ok(main.includes('href="' + target + '"'), 'notice links to ' + target);
 }
});

test('a ready connection shows the requested page without an offline notice', () => {
 const html = render('/problems/new', { mode: 'remote', ready: true, url: 'https://qraft.invalid' });
 assertPage(html, 'business', '/problems/new');
 const main = html.match(/<main\b[^>]*>([\s\S]*?)<\/main>/)?.[1] || '';
 assert.doesNotMatch(main, /href="\/desktop\/settings\?tab=(?:local|connection)"/);
});

test('a fresh unconfigured client still opens welcome instead of a business page', () => {
 for (const route of ['/', '/problems/new', '/desktop/settings']) {
  assertPage(render(route, { mode: 'remote', configured: false }), 'welcome', route);
 }
});
