// 投资组合预测测试：验证到账时间、现金流增长和缺失汇率的影响范围。

import test from 'node:test';
import assert from 'node:assert/strict';
import { calculatePortfolio } from '../src/portfolio.ts';
const assets=[{category:'deposit',amount:10000,currency:'CNY',annual_rate:0}];

test('only counts dated proceeds inside the forecast and converts currency',/* 验证只计入预测区间内的现金流，并按汇率换算到账金额。 */ ()=>{
 const result=calculatePortfolio(assets,{CNY:1,USD:7},1,new Date('2026-01-01T00:00:00Z'),0,[
  {date:'2025-12-31',amount:500,currency:'CNY'},
  {date:'2026-04-15',amount:40000,currency:'USD'},
  {date:'2027-01-02',amount:90000,currency:'CNY'},
 ]);
 assert.equal(result.forecast,290000);
 assert.equal(result.savingsContribution,280000);
});
test('future cash proceeds count principal without compounding',/* 现有资产计息，未来现金收入无论何时到账都只计本金。 */ ()=>{
 const a=[{category:'deposit',amount:10000,currency:'CNY',annual_rate:10}];
 const early=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0,[{date:'2026-02-01',amount:10000,currency:'CNY'}]);
 const late=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0,[{date:'2027-01-01',amount:10000,currency:'CNY'}]);
 assert.equal(early.forecast,21000);
 assert.equal(late.forecast,21000);
});
test('monthly savings add principal while only existing assets compound',()=>{
 const a=[{category:'deposit',amount:10000,currency:'CNY',annual_rate:10}];
 const result=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),1000,[]);
 assert.equal(result.savingsContribution,12000);
 assert.equal(result.forecast,23000);
});
test('future fund contributions add principal without investment returns',()=>{
 const a=[{category:'fund',code:'016452',amount:10000,currency:'CNY',annual_rate:10,investment_strategy:'monthly',investment_amount:1000}];
 const result=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'));
 assert.equal(result.forecast,23000);
});
test('missing proceeds exchange rate prevents an incomplete forecast',/* 验证预测区间内缺失现金流汇率时不返回不完整预测。 */ ()=>{
 assert.equal(calculatePortfolio(assets,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0,[{date:'2026-04-15',amount:100,currency:'USD'}]),null);
});
test('a missing currency outside the forecast horizon does not block it',/* 验证预测区间之外的缺失汇率不会阻止当前预测。 */ ()=>{
 assert.equal(calculatePortfolio(assets,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0,[{date:'2028-04-15',amount:100,currency:'USD'}]).forecast,10000);
});
test('does not invent annual bonus proceeds without dated cashflows',/* 验证未提供现金流时不会凭空计入未安排的年终奖。 */ ()=>{
 assert.equal(calculatePortfolio(assets,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0).forecast,10000);
});
