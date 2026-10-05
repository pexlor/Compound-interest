import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { mutationHeaders } from '../src/mutations.ts';

// HTTP 内网环境只有 getRandomValues 时，写入仍应获得有效且不同的幂等编号。
test('HTTP contexts without randomUUID can generate distinct write request keys', () => {
  const httpCrypto = { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) };
  const keys = new Set();
  for (let i = 0; i < 100; i++) {
    const headers = mutationHeaders(httpCrypto);
    assert.equal(headers['Content-Type'], 'application/json');
    const key = headers['Idempotency-Key'];
    assert.equal(typeof key, 'string');
    assert.ok(key.length > 0 && key.length <= 128);
    keys.add(key);
  }
  assert.equal(keys.size, 100);
});

test('secure contexts still generate distinct write request keys', () => {
  const a = mutationHeaders(webcrypto);
  const b = mutationHeaders(webcrypto);
  assert.equal(a['Content-Type'], 'application/json');
  assert.ok(a['Idempotency-Key'].length > 0 && a['Idempotency-Key'].length <= 128);
  assert.notEqual(a['Idempotency-Key'], b['Idempotency-Key']);
});
