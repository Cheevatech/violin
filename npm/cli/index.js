#!/usr/bin/env node

const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');

const OWNER = process.env.VIOLIN_GITHUB_OWNER || 'Cheevatech';
const REPOSITORY = process.env.VIOLIN_GITHUB_REPOSITORY || 'violin';
const VERSION = process.env.VIOLIN_VERSION || require('../../package.json').version;
const RELEASE_PUBLIC_KEY = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA2siEtn2yfPR1xgfsinc4v9ryM7FOAKA/WYYPksv1aFw=
-----END PUBLIC KEY-----`;

function platformKey() {
  const platform = process.platform === 'darwin' ? 'darwin' : process.platform;
  const arch = process.arch === 'arm64' ? 'arm64' : process.arch === 'x64' ? 'amd64' : process.arch;
  if (!['darwin', 'linux'].includes(platform) || !['arm64', 'amd64'].includes(arch)) {
    throw new Error(`unsupported platform: ${process.platform}/${process.arch}`);
  }
  return `${platform}-${arch}`;
}

function cachePath() {
  const base = process.env.XDG_CACHE_HOME || (process.platform === 'darwin'
    ? path.join(os.homedir(), 'Library', 'Caches')
    : path.join(os.homedir(), '.cache'));
  return path.join(base, 'violin', VERSION, platformKey(), 'violin');
}

function cacheMetadataPath() {
  return `${cachePath()}.json`;
}

function releaseBase() {
  return process.env.VIOLIN_RELEASE_BASE_URL || `https://github.com/${OWNER}/${REPOSITORY}/releases/download/v${VERSION}`;
}

async function fetchBytes(url) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`download failed (${response.status}): ${url}`);
  return Buffer.from(await response.arrayBuffer());
}

function manifestURL() {
  return process.env.VIOLIN_RELEASE_MANIFEST_URL || `${releaseBase()}/manifest.json`;
}

function verifySignature(manifestData) {
  const publicKey = process.env.VIOLIN_RELEASE_PUBLIC_KEY || RELEASE_PUBLIC_KEY;
  const signature = manifestData.signature;
  if (!signature) {
    if (process.env.VIOLIN_ALLOW_UNSIGNED_RELEASE === '1') return;
    throw new Error('release manifest is unsigned; set VIOLIN_ALLOW_UNSIGNED_RELEASE=1 only for development');
  }
  const payload = JSON.stringify(manifestData.artifacts);
  const valid = crypto.verify(null, Buffer.from(payload), publicKey, Buffer.from(signature, 'base64'));
  if (!valid) throw new Error('release manifest signature verification failed');
}

async function ensureBinary() {
  const target = cachePath();
  const metadata = cacheMetadataPath();
  if (fs.existsSync(target) && fs.existsSync(metadata)) {
    try {
      const recorded = JSON.parse(fs.readFileSync(metadata, 'utf8'));
      const actual = crypto.createHash('sha256').update(fs.readFileSync(target)).digest('hex');
      const executable = (fs.statSync(target).mode & 0o111) !== 0;
      if (recorded.version === VERSION && recorded.platform === platformKey() && recorded.sha256 === actual && executable) {
        return target;
      }
    } catch (_) {
      // Treat an unreadable cache entry as invalid and fetch a clean artifact.
    }
    fs.rmSync(target, { force: true });
    fs.rmSync(metadata, { force: true });
  }
  fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
  const manifest = JSON.parse((await fetchBytes(manifestURL())).toString('utf8'));
  verifySignature(manifest);
  const artifact = manifest.artifacts?.[platformKey()];
  if (!artifact?.url || !artifact?.sha256) throw new Error(`release manifest has no verified artifact for ${platformKey()}`);
  const data = await fetchBytes(artifact.url.startsWith('http') ? artifact.url : `${releaseBase()}/${artifact.url}`);
  const actual = crypto.createHash('sha256').update(data).digest('hex');
  if (actual !== artifact.sha256) throw new Error(`release checksum mismatch for ${platformKey()}`);
  const temporary = `${target}.tmp-${process.pid}`;
  fs.writeFileSync(temporary, data, { mode: 0o700 });
  fs.renameSync(temporary, target);
  fs.writeFileSync(metadata, JSON.stringify({ version: VERSION, platform: platformKey(), sha256: actual }) + '\n', { mode: 0o600 });
  return target;
}

function doctor() {
  return { platform: platformKey(), version: VERSION, cache: cachePath(), node: process.version };
}

async function main(args) {
  if (args[0] === 'doctor') {
    process.stdout.write(`${JSON.stringify(doctor(), null, 2)}\n`);
    return;
  }
  const binary = await ensureBinary();
  const child = spawn(binary, args, { stdio: 'inherit', env: process.env });
  child.on('exit', code => process.exit(code ?? 1));
}

if (require.main === module) main(process.argv.slice(2)).catch(error => {
  process.stderr.write(`violin: ${error.message}\n`);
  process.exit(1);
});

module.exports = { platformKey, cachePath, cacheMetadataPath, releaseBase, verifySignature, ensureBinary };
