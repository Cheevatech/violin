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

test('downloaded cache is restored when its checksum metadata no longer matches', async () => {
  const temp = require('node:fs').mkdtempSync(require('node:path').join(require('node:os').tmpdir(), 'violin-npm-'));
  const previousCache = process.env.XDG_CACHE_HOME;
  const previousUnsigned = process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE;
  const previousManifest = process.env.VIOLIN_RELEASE_MANIFEST_URL;
  const previousFetch = global.fetch;
  const payload = Buffer.from('fake violin binary');
  const sha256 = require('node:crypto').createHash('sha256').update(payload).digest('hex');
  let fetches = 0;
  process.env.XDG_CACHE_HOME = temp;
  process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE = '1';
  process.env.VIOLIN_RELEASE_MANIFEST_URL = 'https://fixture.test/manifest.json';
  global.fetch = async url => {
    fetches += 1;
    return { ok: true, arrayBuffer: async () => Buffer.from(url.endsWith('manifest.json')
      ? JSON.stringify({ artifacts: { [cli.platformKey()]: { url: 'binary', sha256 } } })
      : payload) };
  };
  try {
    const first = await cli.ensureBinary();
    require('node:fs').chmodSync(first, 0o600);
    const second = await cli.ensureBinary();
    assert.equal(require('node:fs').readFileSync(second).toString(), payload.toString());
    assert.equal(fetches, 4);
  } finally {
    require('node:fs').rmSync(temp, { recursive: true, force: true });
    global.fetch = previousFetch;
    if (previousCache === undefined) delete process.env.XDG_CACHE_HOME;
    else process.env.XDG_CACHE_HOME = previousCache;
    if (previousUnsigned === undefined) delete process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE;
    else process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE = previousUnsigned;
    if (previousManifest === undefined) delete process.env.VIOLIN_RELEASE_MANIFEST_URL;
    else process.env.VIOLIN_RELEASE_MANIFEST_URL = previousManifest;
  }
});
