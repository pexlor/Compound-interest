import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeMarketResults,missingMarketRates,shouldApplyMarketResponse} from '../src/market-cache.ts';
const asset={category:'fund',code:'021000'};
const valid={category:'fund',code:'021000',annualRate:17,annualReady:true,requestedDays:1095};

test('missing historical input cannot silently become zero rate',()=>{
 assert.equal(missingMarketRates([asset],{},1095).length,1);
 const pending={...valid,annualRate:0,annualReady:false,pending:true};
 assert.equal(missingMarketRates([asset],mergeMarketResults({},[pending],1095),1095).length,1);
});
test('a valid stale cached rate survives a partial refresh',()=>{
 const original=mergeMarketResults({},[{...valid,stale:true}],1095);
 const result=mergeMarketResults(original,[{...valid,annualReady:false,pending:true}],1095);
 assert.equal(result['fund:021000'].annualRate,17);
 assert.equal(missingMarketRates([asset],result,1095).length,0);
});
test('switching intervals excludes the previous intervals rate',()=>{
 const previous=mergeMarketResults({},[valid],1095);
 assert.deepEqual(mergeMarketResults(previous,[],365),{});
 assert.equal(missingMarketRates([asset],previous,365).length,1);
});
test('manual cash rates require no unsupported historical data',()=>{
 assert.equal(missingMarketRates([{category:'money',code:null},{category:'fixed',code:'BTC'},{category:'deposit',code:null}],{},1095).length,0);
});

test('a late response cannot remove the active intervals cache',()=>{
 const current={...valid,requestedDays:365};
 const previous=mergeMarketResults({},[current],365);
 assert.equal(shouldApplyMarketResponse(1095,365,1,2),false);
 assert.equal(shouldApplyMarketResponse(365,365,1,2),false);
 assert.equal(shouldApplyMarketResponse(365,365,2,2),true);
 const result=shouldApplyMarketResponse(1095,365,1,2)?mergeMarketResults(previous,[valid],1095):previous;
 assert.equal(result['fund:021000'].requestedDays,365);
});
