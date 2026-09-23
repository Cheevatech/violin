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

function installedPath() {
  return path.join(os.homedir(), '.local', 'share', 'violin', 'bin', 'violin');
}

function installedMetadataPath() { return `${installedPath()}.json`; }

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
  const cached = metadataStatus(cachePath(), cacheMetadataPath());
  const installed = metadataStatus(installedPath(), installedMetadataPath());
  return { platform: platformKey(), version: VERSION, cache: cachePath(), installed: installedPath(),
    cache_status: cached, installed_status: installed,
    update_available: cached.valid && (!installed.valid || cached.sha256 !== installed.sha256 || cached.version !== installed.version),
    node: process.version };
}

function metadataStatus(binary, metadata) {
  try {
    const recorded = JSON.parse(fs.readFileSync(metadata, 'utf8'));
    const actual = crypto.createHash('sha256').update(fs.readFileSync(binary)).digest('hex');
    return { valid: recorded.sha256 === actual, version: recorded.version, sha256: actual };
  } catch (_) { return { valid: false }; }
}

function runBinary(binary, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, { stdio: 'inherit', env: process.env });
    child.once('error', reject);
    child.once('exit', code => resolve(code ?? 1));
  });
}

async function installManaged(binary) {
  const target = installedPath();
  const metadata = installedMetadataPath();
  const suffix = `${Date.now()}-${process.pid}`;
  const backup = `${target}.backup-${suffix}`;
  const metaBackup = `${metadata}.backup-${suffix}`;
  const temporary = `${target}.tmp-${suffix}`;
  const hadBinary = fs.existsSync(target);
  const hadMetadata = fs.existsSync(metadata);
  const configPath = path.join(os.homedir(), '.codex', 'config.toml');
  const oldConfig = fs.existsSync(configPath) ? fs.readFileSync(configPath) : null;
  fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 });
  const cacheMeta = JSON.parse(fs.readFileSync(cacheMetadataPath(), 'utf8'));
  try {
    fs.copyFileSync(binary, temporary);
    fs.chmodSync(temporary, 0o700);
    if (hadBinary) fs.copyFileSync(target, backup);
    if (hadMetadata) fs.copyFileSync(metadata, metaBackup);
    fs.renameSync(temporary, target);
    fs.writeFileSync(metadata, JSON.stringify(cacheMeta) + '\n', { mode: 0o600 });
    const code = await runBinary(target, ['install']);
    if (code !== 0) throw new Error(`install failed with exit code ${code}`);
    return { binary: target, backup: hadBinary ? backup : null };
  } catch (error) {
    if (oldConfig !== null) {
      const restore = `${configPath}.tmp-${suffix}`;
      fs.writeFileSync(restore, oldConfig, { mode: 0o600 });
      fs.renameSync(restore, configPath);
    } else fs.rmSync(configPath, { force: true });
    if (hadBinary && fs.existsSync(backup)) fs.renameSync(backup, target);
    else if (!hadBinary) fs.rmSync(target, { force: true });
    if (hadMetadata && fs.existsSync(metaBackup)) fs.renameSync(metaBackup, metadata);
    else if (!hadMetadata) fs.rmSync(metadata, { force: true });
    throw error;
  } finally { fs.rmSync(temporary, { force: true }); }
}

async function main(args) {
  if (args[0] === 'doctor') {
    process.stdout.write(`${JSON.stringify(doctor(), null, 2)}\n`);
    return;
  }
  const binary = await ensureBinary();
  if (args[0] === 'install') { await installManaged(binary); return; }
  process.exitCode = await runBinary(binary, args);
}

if (require.main === module) main(process.argv.slice(2)).catch(error => {
  process.stderr.write(`violin: ${error.message}\n`);
  process.exit(1);
});

module.exports = { platformKey, cachePath, cacheMetadataPath, installedPath, installedMetadataPath, releaseBase, verifySignature, ensureBinary, installManaged, doctor };
