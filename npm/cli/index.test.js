const test = require('node:test');
const assert = require('node:assert/strict');
const cli = require('./index.js');

test('platform key is a supported release target', () => {
  assert.match(cli.platformKey(), /^(darwin|linux)-(amd64|arm64)$/);
});

test('unsigned local manifest is accepted for development fixtures', () => {
  const previous = process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE;
  process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE = '1';
  assert.doesNotThrow(() => cli.verifySignature({ artifacts: {} }));
  if (previous === undefined) delete process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE;
  else process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE = previous;
});

test('unsigned release is rejected by default', () => {
  const previous = process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE;
  delete process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE;
  assert.throws(() => cli.verifySignature({ artifacts: {} }), /unsigned/);
  if (previous !== undefined) process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE = previous;
});
