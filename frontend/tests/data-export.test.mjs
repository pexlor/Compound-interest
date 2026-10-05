import test from 'node:test';
import assert from 'node:assert/strict';
import {buildExportURL} from '../src/data-export.ts';

test('export choices and asset scope reach the API without losing date boundaries', /* 验证选择、资产范围和日期边界正确编码。 */ ()=>{
 const u=new URL(buildExportURL({datasets:['assets','prices'],from:'2026-01-01',to:'2026-10-05',assetIds:[3,8]}),'http://localhost');
 assert.equal(u.pathname,'/api/export');assert.equal(u.searchParams.get('datasets'),'assets,prices');
 assert.equal(u.searchParams.get('assetIds'),'3,8');assert.equal(u.searchParams.get('from'),'2026-01-01');assert.equal(u.searchParams.get('to'),'2026-10-05');
});
test('empty selection and invalid dates cannot start a download', /* 验证无效选择和日期不能下载。 */ ()=>{
 assert.throws(()=>buildExportURL({datasets:[]}),/至少/);
 assert.throws(()=>buildExportURL({datasets:['assets'],assetIds:[]}),/资产/);
 assert.throws(()=>buildExportURL({datasets:['prices'],from:'2026-02-30'}),/日期/);
 assert.throws(()=>buildExportURL({datasets:['prices'],from:'2026-10-05',to:'2026-10-01'}),/开始/);
});
test('all-assets export omits the asset filter and accepts an open date range', /* 验证全部资产与开放日期范围。 */ ()=>{
 const u=new URL(buildExportURL({datasets:['assets'],to:'2026-10-05'}),'http://localhost');
 assert.equal(u.searchParams.has('assetIds'),false);assert.equal(u.searchParams.has('from'),false);
});
