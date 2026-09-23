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

test('managed install updates a stable path and restores binary after failure', async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const os = require('node:os');
  const crypto = require('node:crypto');
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'violin-managed-'));
  const oldHome = process.env.HOME;
  const oldCache = process.env.XDG_CACHE_HOME;
  process.env.HOME = temp;
  process.env.XDG_CACHE_HOME = path.join(temp, 'cache');
  const cached = cli.cachePath();
  const put = script => {
    fs.mkdirSync(path.dirname(cached), { recursive: true });
    fs.writeFileSync(cached, script, { mode: 0o700 });
    fs.chmodSync(cached, 0o700);
    fs.writeFileSync(cli.cacheMetadataPath(), JSON.stringify({ version: 'fixture', platform: cli.platformKey(), sha256: crypto.createHash('sha256').update(script).digest('hex') }));
  };
  try {
    fs.mkdirSync(path.join(temp, '.codex'));
    const config = path.join(temp, '.codex', 'config.toml');
    fs.writeFileSync(config, 'original config\n');
    put('#!/bin/sh\nexit 0\n');
    await cli.installManaged(cached);
    const stable = cli.installedPath();
    assert.equal(fs.readFileSync(stable, 'utf8'), '#!/bin/sh\nexit 0\n');
    put('#!/bin/sh\nprintf "changed config\\n" > "$HOME/.codex/config.toml"\nexit 1\n');
    await assert.rejects(cli.installManaged(cached), /install failed/);
    assert.equal(cli.installedPath(), stable);
    assert.equal(fs.readFileSync(stable, 'utf8'), '#!/bin/sh\nexit 0\n');
    assert.equal(fs.readFileSync(config, 'utf8'), 'original config\n');
  } finally {
    if (oldHome === undefined) delete process.env.HOME; else process.env.HOME = oldHome;
    if (oldCache === undefined) delete process.env.XDG_CACHE_HOME; else process.env.XDG_CACHE_HOME = oldCache;
    fs.rmSync(temp, { recursive: true, force: true });
  }
});

test('release checksum failure leaves installed binary untouched', async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const os = require('node:os');
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'violin-checksum-'));
  const previous = { HOME: process.env.HOME, XDG_CACHE_HOME: process.env.XDG_CACHE_HOME,
    VIOLIN_ALLOW_UNSIGNED_RELEASE: process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE,
    VIOLIN_RELEASE_MANIFEST_URL: process.env.VIOLIN_RELEASE_MANIFEST_URL };
  const oldFetch = global.fetch;
  Object.assign(process.env, { HOME: temp, XDG_CACHE_HOME: path.join(temp, 'cache'),
    VIOLIN_ALLOW_UNSIGNED_RELEASE: '1', VIOLIN_RELEASE_MANIFEST_URL: 'https://fixture.test/manifest.json' });
  fs.mkdirSync(path.dirname(cli.installedPath()), { recursive: true });
  fs.writeFileSync(cli.installedPath(), 'old binary');
  global.fetch = async url => ({ ok: true, arrayBuffer: async () => Buffer.from(url.endsWith('manifest.json')
    ? JSON.stringify({ artifacts: { [cli.platformKey()]: { url: 'bad', sha256: '0'.repeat(64) } } }) : 'bad binary') });
  try {
    await assert.rejects(cli.ensureBinary(), /checksum mismatch/);
    assert.equal(fs.readFileSync(cli.installedPath(), 'utf8'), 'old binary');
  } finally {
    global.fetch = oldFetch;
    for (const [key, value] of Object.entries(previous)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
    fs.rmSync(temp, { recursive: true, force: true });
  }
});
