// 预测界面测试：避免情景参数丢失、身份错配和旧响应覆盖。
import test from 'node:test';
import assert from 'node:assert/strict';
import { forecastURL, readForecastScenario, canApplyForecast, forecastEnd } from '../src/forecast.ts';

test('forecast request keeps inflation, restricted assets and explicit benchmarks',()=>{
 const url=new URL(forecastURL(5,{inflation:3,includeRestricted:true,benchmarks:{12:'cn_bond'}}),'http://localhost');
 assert.equal(url.searchParams.get('years'),'5');
 assert.equal(url.searchParams.get('inflation'),'3');
 assert.equal(url.searchParams.get('includeRestricted'),'true');
 assert.deepEqual(JSON.parse(url.searchParams.get('benchmarks')),{'12':'cn_bond'});
});
test('invalid saved scenario falls back to safe assumptions',()=>{
 assert.deepEqual(readForecastScenario('{bad'),{inflation:2,includeRestricted:true,benchmarks:{}});
 assert.equal(readForecastScenario('{"inflation":-1}').inflation,2);
 assert.deepEqual(readForecastScenario('{"inflation":3,"includeRestricted":true,"benchmarks":{"2":"fake","3":"cn_bond"}}').benchmarks,{'3':'cn_bond'});
});
test('late requests and old users cannot update forecast',()=>{
 assert.equal(canApplyForecast(1,2,4,4),false);
 assert.equal(canApplyForecast(2,2,3,4),false);
 assert.equal(canApplyForecast(2,2,4,4),true);
});
test('unavailable forecast never presents a fabricated zero endpoint',()=>{
 assert.equal(forecastEnd({state:'unavailable',series:[]}),null);
 assert.equal(forecastEnd({state:'ready',series:[{year:0,p50:100},{year:1,p50:110}]}).p50,110);
});

test('stored snapshot changes require reconciliation while identical balances do not', async()=>{
 const {sameStoredAssets}=await import('../src/forecast.ts');
 const current=[{id:1,version:2,amount:100,currency:'CNY',quantity:null,annual_rate:2}];
 assert.equal(sameStoredAssets(current,[{...current[0]}]),true);
 assert.equal(sameStoredAssets(current,[{...current[0],version:3,amount:110}]),false);
 assert.equal(sameStoredAssets(current,[]),false);
});

test('snapshot FX update reconciles changed rates without a stable request loop',async()=>{
 const {sameForecastRates}=await import('../src/forecast.ts');
 assert.equal(sameForecastRates({CNY:1,USD:7},{USD:7,CNY:1}),true);
 assert.equal(sameForecastRates({CNY:1,USD:7},{USD:7.2,CNY:1}),false);
});

test('old saved exclusions cannot remove recorded assets from retirement',()=>{
 assert.equal(readForecastScenario('{"includeRestricted":false}').includeRestricted,true);
 const url=new URL(forecastURL(1,{inflation:2,includeRestricted:false,benchmarks:{}}),'http://localhost');
 assert.equal(url.searchParams.get('includeRestricted'),'true');
});
