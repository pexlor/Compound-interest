// 收入工具测试：验证月末归属日期、首期比例、数量守恒与闰日奖金预览。

import test from 'node:test';
import assert from 'node:assert/strict';
import { generateBatches, nextBonus } from '../src/income.ts';

test('monthly vesting clamps month-end and preserves granted quantity after rounding',/* 验证归属日期按月末截断，并在舍入后保持授予总数量。 */ ()=>{
 const batches=generateBatches(100,'2024-01-31',4,1,25);
 assert.deepEqual(batches.map(/* 提取每个归属批次的日期，供测试校验。 */ b=>b.vestDate),['2024-01-31','2024-02-29','2024-03-31','2024-04-30']);
 assert.deepEqual(batches.map(/* 提取每个归属批次的数量，供测试校验。 */ b=>b.quantity),[25,25,25,25]);
 assert.ok(Math.abs(generateBatches(100,'2026-01-01',7,3,25).reduce(/* 累计归属批次数量，用于校验授予总量。 */ (s,b)=>s+b.quantity,0)-100)<0.000001);
});
test('cliff share stays in the first installment and remaining shares are distributed',/* 验证首期集中归属比例和剩余批次的数量分配。 */ ()=>{
 const batches=generateBatches(120,'2026-01-01',4,12,50);
 assert.deepEqual(batches.map(/* 提取每个归属批次的数量，供测试校验。 */ b=>b.quantity),[60,20,20,20]);
 assert.equal(batches[3].vestDate,'2029-01-01');
});
test('bonus preview uses prior earning year and handles recurring leap-day payment',/* 验证奖金所属年度与闰日领取日期的处理。 */ ()=>{
 const result=nextBonus(36600,{workStartDate:'2024-07-01',payMonth:2,payDay:29,yearOffset:1},'2025-01-01');
 assert.equal(result.date,'2025-02-28');
 assert.equal(result.earningYear,2024);
 assert.equal(result.amount,1656000);
});
