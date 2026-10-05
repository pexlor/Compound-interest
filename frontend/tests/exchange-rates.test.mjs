// 汇率展示测试：验证双向换算、交叉汇率与无效数据处理。
import test from 'node:test';
import assert from 'node:assert/strict';
import { exchangePairs } from '../src/exchange-rates.ts';

test('converts all three currency pairs in both directions', /* 验证每单位人民币、美元和港元的双向换算。 */ () => {
  const pairs = exchangePairs({ CNY: 1, USD: 7, HKD: 0.875 });
  assert.deepEqual(pairs, [
    { base: 'CNY', quote: 'USD', forward: 1 / 7, reverse: 7 },
    { base: 'CNY', quote: 'HKD', forward: 1 / 0.875, reverse: 0.875 },
    { base: 'HKD', quote: 'USD', forward: 0.125, reverse: 8 },
  ]);
});

test('unavailable currencies only hide affected pairs', /* 验证缺失、零、负值或非有限汇率不生成误导数值。 */ () => {
  for (const USD of [undefined, 0, -7, NaN, Infinity]) {
    const pairs = exchangePairs({ CNY: 1, USD, HKD: 0.875 });
    assert.equal(pairs[0].forward, null);
    assert.equal(pairs[0].reverse, null);
    assert.equal(pairs[1].reverse, 0.875);
    assert.equal(pairs[2].forward, null);
    assert.equal(pairs[2].reverse, null);
  }
});
