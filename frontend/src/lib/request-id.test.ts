import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { requestID } from './request-id.ts';

test('HTTP context without randomUUID still produces distinct secure UUIDs', () => {
  const source = { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) };
  const values = new Set(Array.from({length:100}, () => requestID(source)));
  assert.equal(values.size,100);
  for (const value of values) assert.match(value,/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/);
});
test('secure contexts retain the native generator', () => {
  const expected = 'e8989b3f-5dc0-469a-8abc-53e0b4e45adc';
  assert.equal(requestID({randomUUID:()=>expected,getRandomValues:()=>{throw Error('unused')}}),expected);
});
test('entropy failure cannot produce a weak fallback key', () => {
  assert.throws(()=>requestID({getRandomValues:()=>{throw Error('unavailable')}}),/unavailable/);
});
