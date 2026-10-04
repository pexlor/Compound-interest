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
test('cash proceeds grow only after their arrival',/* 验证现金收入只有到账后才参与复利增长。 */ ()=>{
 const a=[{category:'deposit',amount:10000,currency:'CNY',annual_rate:10}];
 const early=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0,[{date:'2026-02-01',amount:10000,currency:'CNY'}]);
 const late=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'),0,[{date:'2027-01-01',amount:10000,currency:'CNY'}]);
 assert.ok(early.forecast>late.forecast);
 assert.ok(Math.abs(late.forecast-21000)<0.1);
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
