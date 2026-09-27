const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const ts=require('typescript');
const Module=require('node:module');
const path=require('node:path');
const file=path.join(__dirname,'problem-import-errors.ts');
const moduleUnderTest=new Module(file,module);
moduleUnderTest._compile(ts.transpileModule(fs.readFileSync(file,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText,file);
const {importFailureSummary}=moduleUnderTest.exports;
test('import errors distinguish timeout, exhausted repair and preserved rating failure',()=>{
 assert.match(importFailureSummary('ImportModelTimeout'),/超时/);
 assert.match(importFailureSummary('InvalidImportModelResponse'),/两次修复/);
 assert.match(importFailureSummary('RatingAssessmentFailed'),/已.*保存/);
 assert.match(importFailureSummary('generated test-case count must be positive'),/结构或数量/);
});
