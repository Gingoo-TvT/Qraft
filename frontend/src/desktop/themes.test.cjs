const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const Module = require('node:module');
const ts = require('typescript');

// Compile the pure theme module with the project's existing TypeScript compiler.
const file = path.join(__dirname, 'themes.ts');
const loaded = new Module(file, module);
loaded.filename = file;
loaded.paths = module.paths;
loaded._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText, file);
const theme = loaded.exports;

test('all six presets have readable text, muted labels and button foregrounds in both modes', () => {
 assert.equal(theme.BUILTIN_THEMES.length, 6);
 for (const preset of theme.BUILTIN_THEMES) {
  assert.doesNotThrow(() => theme.validateTheme(preset));
  for (const palette of Object.values(preset.colors)) {
   for (const bg of [palette.background, palette.surface, palette.sidebar]) {
    assert.ok(theme.contrastRatio(palette.text, bg) >= 4.5, preset.id + ' text');
    assert.ok(theme.contrastRatio(palette.muted, bg) >= 4.5, preset.id + ' muted');
   }
   assert.ok(theme.contrastRatio(theme.foregroundColor(palette.accent), palette.accent) >= 4.5);
  }
 }
});
test('theme export/import preserves both complete palettes and normalizes hex', () => {
 const exported = theme.serializeTheme(theme.BUILTIN_THEMES[2]);
 assert.deepEqual(theme.parseThemeJSON(exported), theme.BUILTIN_THEMES[2]);
 const lower = JSON.parse(exported); lower.colors.light.accent = '#abcdef';
 assert.equal(theme.parseThemeJSON(JSON.stringify(lower)).colors.light.accent, '#ABCDEF');
});
test('bad JSON, unsupported keys, code/URLs and unreadable text cannot produce a theme', () => {
 assert.throws(() => theme.parseThemeJSON('{bad'), /JSON/);
 assert.throws(() => theme.parseThemeJSON(' '.repeat(16385)), /16 KB/);
 const changes = [
  x => x.version = 2,
  x => x.script = 'alert(1)',
  x => x.colors.css = 'body{}',
  x => x.colors.light.style = 'background:red',
  x => x.colors.light.accent = 'url(https://example.com/a)',
  x => x.colors.light.accent = 'var(--accent)',
  x => x.colors.light.accent = '#123',
  x => x.colors.light.accent = '#12345678',
  x => x.colors.light.text = '#EEEEEE',
  x => x.colors.dark.muted = '#222222',
  x => delete x.colors.dark.border,
  x => x.name = '字'.repeat(41),
  x => x.name = 'bad\nname',
 ];
 for (const change of changes) {
  const bad = structuredClone(theme.BUILTIN_THEMES[0]); change(bad);
  assert.throws(() => theme.parseThemeJSON(JSON.stringify(bad)));
 }
 assert.deepEqual(theme.BUILTIN_THEMES[0], theme.parseThemeJSON(theme.serializeTheme(theme.BUILTIN_THEMES[0])));
});
test('pale custom accents select readable black buttons and derivative link colors', () => {
 const created = theme.createTheme('黄昏', '#FFFF00', theme.BUILTIN_THEMES[0]);
 assert.match(created.id, /^custom-/);
 assert.equal(theme.foregroundColor('#FFFF00'), '#000000');
 for (const palette of Object.values(created.colors)) {
  const readable = theme.readableAccent(palette.accent, [palette.background, palette.surface]);
  assert.ok(theme.contrastRatio(readable, palette.background) >= 4.5);
  assert.ok(theme.contrastRatio(readable, palette.surface) >= 4.5);
 }
});
test('legacy display mode remains separate from color palette and system changes', () => {
 const legacy = { theme:'system', density:'compact', sidebar_collapsed:true, last_path:'/problem-sets' };
 const prefs = theme.normalizePreferences(legacy);
 assert.equal(prefs.color_theme, 'graphite');
 assert.deepEqual(prefs.custom_themes, []);
 const variables = new Map();
 const root = { style: { setProperty:(name,value)=>variables.set(name,value) }, classList:{toggle:()=>{}}, dataset:{} };
 assert.equal(theme.applyTheme(prefs, false, root), false);
 assert.equal(root.style.colorScheme, 'light');
 assert.equal(root.dataset.density, 'compact');
 const lightBackground = variables.get('--db');
 assert.equal(theme.applyTheme(prefs, true, root), true);
 assert.equal(root.style.colorScheme, 'dark');
 assert.notEqual(variables.get('--db'), lightBackground);
 assert.match(variables.get('--forge-500'), /^\d+ \d+ \d+$/);
 assert.equal(root.dataset.colorTheme, 'graphite');
 assert.equal(theme.applyTheme({...prefs, theme:'light', color_theme:'iris'}, true, root), false);
 assert.equal(root.dataset.colorTheme, 'iris');
});
