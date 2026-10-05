// 投资组合预测测试：验证到账时间、现金流增长和缺失汇率的影响范围。

import test from 'node:test';
import assert from 'node:assert/strict';
import { calculatePortfolio, calculateMonthEndGrowth } from '../src/portfolio.ts';
const assets=[{category:'deposit',amount:10000,currency:'CNY',annual_rate:0}];

test('month-end growth only counts remaining-month earnings and dated proceeds', /* 验证本月剩余增长只计现有资产收益、本月底储蓄和月底前到账的现金流。 */ () => {
 const result = calculateMonthEndGrowth([
  {category:'deposit',amount:10000,currency:'CNY',annual_rate:100},
  {category:'fixed',amount:10000,currency:'CNY',annual_rate:100},
 ], {CNY:1,USD:7}, new Date('2026-10-05T00:00:00Z'), 2000, [
  {date:'2026-10-05',amount:90000,currency:'CNY'},
  {date:'2026-10-31',amount:100,currency:'USD'},
  {date:'2026-11-01',amount:80000,currency:'CNY'},
  {date:'2027-10-05',amount:90000,currency:'CNY'},
 ]);
 assert.ok(Math.abs(result.investmentGain - 506.141182) < 0.001);
 assert.equal(result.savingsContribution, 2700);
 assert.ok(Math.abs(result.expectedGain - 3206.141182) < 0.001);
 assert.equal(result.endDate, '2026-10-31');
});

test('unknown future currency does not block this month but an incoming one does', /* 验证下月缺失汇率不影响本月，本月到账外币缺失汇率则阻止不完整估算。 */ () => {
 const today = new Date('2026-10-05T00:00:00Z');
 assert.equal(calculateMonthEndGrowth(assets,{CNY:1},today,0,[{date:'2026-11-01',amount:100,currency:'USD'}]).expectedGain,0);
 assert.equal(calculateMonthEndGrowth(assets,{CNY:1},today,0,[{date:'2026-10-31',amount:100,currency:'USD'}]),null);
});

test('month-end does not count already reflected savings and proceeds twice', /* 验证月底当天剩余增长为零，不重复计入当日已到账本金。 */ () => {
 assert.equal(calculateMonthEndGrowth(assets,{CNY:1},new Date('2026-10-31T00:00:00Z'),2000,[{date:'2026-10-31',amount:100,currency:'CNY'}]).expectedGain,0);
});

test('leap February uses its actual month end and calendar-year day count', /* 验证闰年二月的月末与剩余天数，保留负收益估算。 */ () => {
 const result = calculateMonthEndGrowth([{category:'deposit',amount:10000,currency:'CNY',annual_rate:100}],{CNY:1},new Date('2024-02-28T00:00:00Z'));
 assert.equal(result.endDate,'2024-02-29');
 assert.ok(Math.abs(result.investmentGain - 18.956392) < 0.001);
 const loss = calculateMonthEndGrowth([{category:'stock',amount:10000,currency:'CNY',annual_rate:-20}],{CNY:1},new Date('2026-10-05T00:00:00Z'));
 assert.ok(loss.investmentGain < 0);
});

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
test('legacy fund schedules do not add future contributions',/* 验证历史定投设置不再增加预测金额，现有资产仍计息。 */ ()=>{
 const a=[{category:'fund',code:'016452',amount:10000,currency:'CNY',annual_rate:10,investment_strategy:'monthly',investment_amount:1000}];
 const result=calculatePortfolio(a,{CNY:1},1,new Date('2026-01-01T00:00:00Z'));
 assert.equal(result.forecast,11000);
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
