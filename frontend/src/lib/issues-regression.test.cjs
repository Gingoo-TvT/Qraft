const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');
function load(relative, dependencies = {}) {
 const filename=path.resolve(__dirname, relative);
 const code=ts.transpileModule(fs.readFileSync(filename,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,jsx:ts.JsxEmit.React,esModuleInterop:true}}).outputText;
 const m={exports:{}};
 vm.runInNewContext(code,{module:m,exports:m.exports,URLSearchParams,require:name=>name in dependencies?dependencies[name]:require(name)},{filename});
 return m.exports;
}
const locations=load('./problem-list-location.ts',{'./constants':{DEFAULT_PAGE_SIZE:20}});
test('list/detail/edit round trip retains pagination, filters, sort and destination set',()=>{
 const filter={page:7,size:50,search:'sum & difference',level:'algorithm',status:'draft',difficulty_min:800,difficulty_max:1200,tags:['dp','tree'],sort_by:'difficulty',sort_order:'asc'};
 const list=locations.problemListHref(filter,'synthetic-set');
 const detail=locations.problemDetailHref('synthetic-id',list);
 const edit=locations.problemDetailHref('synthetic-id',list,true);
 for (const link of [detail,edit]) {
  const back=locations.problemListReturnTo(new URL(link,'https://test.invalid').searchParams.get('returnTo'));
  assert.equal(back,list);
  assert.equal(new URL(back,'https://test.invalid').searchParams.get('set'),'synthetic-set');
  assert.equal(JSON.stringify(locations.problemFilterFromQuery(back.split('?')[1])),JSON.stringify(filter));
 }
});
test('direct visit defaults and invalid return locations are safe',()=>{
 assert.equal(locations.problemFilterFromQuery('page=-1&size=500&level=bogus').page,1);
 assert.equal(locations.problemFilterFromQuery('page=NaN').size,20);
 for(const value of [null,'//example.com','https://example.com','/problems-extra','/problems\\?page=3']) assert.equal(locations.problemListReturnTo(value),'/problems');
});
const images=load('./markdown-image.ts');
const Renderer=load('../components/MarkdownRenderer.tsx',{'@/lib/markdown-image':images,'next/dynamic':()=>()=>null}).default;
const render=content=>renderToStaticMarkup(React.createElement(Renderer,{content}));
test('Markdown pictures render in text, lists and tables, including empty alt and URL parentheses',()=>{
 for(const content of ['![diagram](https://example.test/plot(a).png "A plot")','- ![](https://example.test/a.png)','| a | b |\n|---|---|\n| ![x](./a.png) | text |']) {
  assert.match(render(content),/<img /);
 }
 const html=render('Before ![figure](https://example.test/a.png "Title") after');
 assert.match(html,/alt="figure"/);assert.match(html,/title="Title"/);assert.match(html,/Before /);assert.match(html,/ after/);
});
test('HTML images retain safe attributes without forwarding scripts or unsafe sources',()=>{
 const html=render('<img src="https://example.test/a.png?x=1&amp;y=2" alt="figure" onerror="alert(1)">');
 assert.match(html,/<img /);assert.doesNotMatch(html,/onerror|alert\(/);assert.match(html,/x=1&amp;y=2/);
 for(const source of ['javascript:alert(1)','data:text/html,hello','file:///tmp/secret','java\nscript:alert(1)','\\\\example.test/a']) assert.equal(images.safeImageSource(source),null);
 assert.doesNotMatch(render('<img src="javascript:evil" onerror="evil">'),/<img /);
});
test('code examples containing image syntax remain code',()=>{
 assert.doesNotMatch(render('```md\n![alt](https://example.test/a.png)\n```'),/<img /);
 assert.doesNotMatch(render('`![alt](https://example.test/a.png)`'),/<img /);
});
