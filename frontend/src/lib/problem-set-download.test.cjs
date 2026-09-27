const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const code = ts.transpileModule(fs.readFileSync(path.join(__dirname, 'problem-set-download.ts'), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
function load({ desktop, fetcher } = {}) {
 const m={exports:{}}; const events=[]; const clicks=[]; const revoked=[];
 const context={module:m,exports:m.exports,TextEncoder,URL:{createObjectURL:()=> 'blob:test',revokeObjectURL:u=>revoked.push(u)},CustomEvent:class{constructor(type,options){this.type=type;this.detail=options.detail}},document:{body:{appendChild(){}},createElement:()=>({click(){clicks.push(this.download)},remove(){}})},window:{dispatchEvent:e=>events.push(e),setTimeout:f=>f()},fetch:fetcher,
 require:name=> name==='./api'? {APIError:Error,exportProblemSetTestingURL:(id,format)=>`/api/${id}?format=${format}`} : name==='./auth-session'? {currentSession:()=>({csrf_token:'csrf'}),serviceFetch:fetcher} : {desktopRuntime:()=>desktop}};
 vm.runInNewContext(code,context);return {...m.exports,events,clicks,revoked};
}
test('upload drops inactive dump/metadata and preserves quoted long IDs',()=>{
 const lib=load();const result=lib.parseExportTagCatalog(JSON.stringify({source:{database:'private'},allTagsTree:[{private:true}],activeTagsTree:[{id:'9007199254740993123'}]}));
 assert.deepEqual(Object.keys(result),['activeTagsTree']);assert.equal(result.activeTagsTree[0].id,'9007199254740993123');assert.throws(()=>lib.parseExportTagCatalog('{}'));
});
test('web download sends scoped catalog and cleans blob URL',async()=>{
 let request;const lib=load({fetcher:async(url,options)=>{request=options;return {ok:true,headers:{get:()=> 'application/zip'},blob:async()=>({})}}});
 await lib.downloadProblemSetTesting('set','generic','set.zip',{activeTagsTree:[]});
 assert.equal(request.method,'POST');assert.match(request.body,/tag_catalog/);assert.deepEqual(lib.clicks,['set.zip']);assert.deepEqual(lib.revoked,['blob:test']);
});
test('backend error never becomes a downloaded JSON file',async()=>{
 const lib=load({fetcher:async()=>({ok:false,status:409,json:async()=>({error:{message:'数据未就绪'}})})});await assert.rejects(lib.downloadProblemSetTesting('set','generic','set.zip'),/数据未就绪/);assert.equal(lib.clicks.length,0);
});
test('desktop uses native save with selected service and CSRF',async()=>{
 let sent;const lib=load({desktop:{base:'/desktop',state:{service_url:'https://example.test'}},fetcher:async(url,options)=>{sent={url,...options};return {ok:true,json:async()=>({success:true,data:{status:'completed',name:'set.zip'}})}}});
 await lib.downloadProblemSetTesting('set','generic','set.zip',{activeTagsTree:[]});
 assert.equal(sent.url,'/desktop/native/download');assert.equal(sent.headers['X-CSRF-Token'],'csrf');assert.equal(sent.headers['X-Qraft-Service'],'https://example.test');assert.ok(JSON.parse(sent.body).body.tag_catalog);assert.equal(lib.events[0].type,'algoforge:exported');assert.equal(lib.clicks.length,0);
});
test('Hydro ignores template catalog and a cancelled native save stays quiet',async()=>{
 let sent;const lib=load({desktop:{base:'/desktop',state:{service_url:'https://example.test'}},fetcher:async(url,options)=>{sent=JSON.parse(options.body);return {ok:true,json:async()=>({success:true,data:{status:'cancelled'}})}}});
 await lib.downloadProblemSetTesting('set','hydro','set.zip',{activeTagsTree:[]});assert.equal(sent.body,undefined);assert.equal(lib.events.length,0);
});
