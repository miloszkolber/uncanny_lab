import { createHash, randomUUID } from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const exists = async location => {
  try { await fs.lstat(location); return true; }
  catch (error) { if (error.code === 'ENOENT') return false; throw error; }
};
const safeRelativePath = name => typeof name === 'string' && name && !/[\\?#:%\s\u0000-\u001f]/.test(name) && !path.posix.isAbsolute(name) && !name.split('/').some(part => !part || part === '.' || part === '..');

function verifyManifest(manifest, inventory) {
  if (manifest.schemaVersion !== 1 || manifest.name !== 'mewa-ui' || typeof manifest.version !== 'string' || !manifest.version || !manifest.foundations || !Array.isArray(manifest.foundations.fonts) || !Array.isArray(manifest.components) || !manifest.licenses || typeof manifest.licenses !== 'object' || Array.isArray(manifest.licenses)) throw new Error('Invalid Mewa UI package manifest.');
  const requireEntry = (name, label) => {
    if (!safeRelativePath(name) || !inventory.has(name)) throw new Error(`Invalid or missing manifest entry (${label}): ${name}`);
  };
  requireEntry(manifest.foundations.base, 'foundation base');
  requireEntry(manifest.foundations.tokens, 'foundation tokens');
  manifest.foundations.fonts.forEach(name => requireEntry(name, 'font'));
  Object.entries(manifest.licenses).forEach(([label, name]) => requireEntry(name, `license ${label}`));
  for (const component of manifest.components) {
    if (!component || typeof component !== 'object' || typeof component.slug !== 'string') throw new Error('Invalid component manifest entry.');
    requireEntry(component.css, `${component.slug} CSS`);
    for (const field of ['controller', 'component', 'auto']) {
      if (component[field] != null) requireEntry(component[field], `${component.slug} ${field}`);
    }
  }
}

async function publicationLock(parent, hash) {
  const lock = path.join(parent, `.publish-${hash}.lock`);
  const deadline = Date.now() + 30000;
  while (true) {
    try { await fs.mkdir(lock, { mode: 0o700 }); break; }
    catch (error) {
      if (error.code !== 'EEXIST') throw error;
      let stat;
      try { stat = await fs.lstat(lock); }
      catch (statError) { if (statError.code === 'ENOENT') continue; throw statError; }
      if (stat.isSymbolicLink() || !stat.isDirectory()) throw new Error(`Publication lock must be a real directory: ${lock}`);
      if (Date.now() >= deadline) throw new Error(`Publication lock is busy: ${lock}. Check its owner before removing a stale lock.`);
      await delay(25);
    }
  }
  try { await fs.writeFile(path.join(lock, 'owner.json'), JSON.stringify({ pid: process.pid, started: new Date().toISOString() }), { flag: 'wx', mode: 0o600 }); }
  catch (error) { await fs.rm(lock, { recursive: true, force: true }); throw error; }
  return async () => { await fs.rm(lock, { recursive: true, force: true }); };
}

async function listFiles(root, relative = '') {
  const location = path.join(root, relative);
  const stat = await fs.lstat(location);
  if (stat.isSymbolicLink() || !stat.isDirectory()) throw new Error(`Package directories must be real directories: ${location}`);
  const result = [];
  for (const entry of await fs.readdir(location, { withFileTypes: true })) {
    const name = relative ? `${relative}/${entry.name}` : entry.name;
    if (entry.isSymbolicLink()) throw new Error(`Package symlink is not allowed: ${name}`);
    if (entry.isDirectory()) result.push(...await listFiles(root, name));
    else if (entry.isFile()) result.push(name);
    else throw new Error(`Unsupported package entry: ${name}`);
  }
  return result.sort();
}

export async function verifyPackage(directory) {
  const root = path.resolve(directory);
  const files = await listFiles(root);
  const checksumBytes = await fs.readFile(path.join(root, 'checksums.json'));
  const checksums = JSON.parse(checksumBytes);
  if (checksums.algorithm !== 'sha256' || !checksums.files || typeof checksums.files !== 'object' || Array.isArray(checksums.files)) throw new Error('Invalid SHA-256 package checksum manifest.');
  const expected = Object.keys(checksums.files);
  if (!expected.length || expected.includes('checksums.json')) throw new Error('Invalid package file inventory.');
  for (const name of expected) {
    if (!safeRelativePath(name) || !/^[a-f0-9]{64}$/.test(checksums.files[name])) throw new Error(`Invalid checksum entry: ${name}`);
  }
  const allowed = new Set([...expected, 'checksums.json']);
  const extra = files.filter(name => !allowed.has(name));
  const present = new Set(files);
  const missing = expected.filter(name => !present.has(name));
  if (extra.length || missing.length) throw new Error(`Package inventory mismatch. Missing: ${missing.join(', ') || 'none'}. Extra: ${extra.join(', ') || 'none'}.`);
  for (const name of expected) {
    if (sha256(await fs.readFile(path.join(root, name))) !== checksums.files[name]) throw new Error(`Package checksum mismatch: ${name}`);
  }
  const manifest = JSON.parse(await fs.readFile(path.join(root, 'manifest.json'), 'utf8'));
  verifyManifest(manifest, present);
  return { root, hash: sha256(checksumBytes), files: files.length, manifest };
}

export async function importPackage(sourceDirectory, consumerDirectory) {
  const source = await verifyPackage(sourceDirectory);
  const consumer = path.resolve(consumerDirectory);
  const consumerStat = await fs.lstat(consumer);
  if (consumerStat.isSymbolicLink() || !consumerStat.isDirectory()) throw new Error('Consumer root must be an existing real directory.');
  for (const relative of ['ui', 'ui/mewa-ui']) {
    const location = path.join(consumer, relative);
    if (await exists(location)) {
      const stat = await fs.lstat(location);
      if (stat.isSymbolicLink() || !stat.isDirectory()) throw new Error(`Vendor directory must be a real directory: ${location}`);
    } else {
      try { await fs.mkdir(location); }
      catch (error) { if (error.code !== 'EEXIST') throw error; }
      const stat = await fs.lstat(location);
      if (stat.isSymbolicLink() || !stat.isDirectory()) throw new Error(`Vendor directory must be a real directory: ${location}`);
    }
  }
  const parent = path.join(consumer, 'ui/mewa-ui');
  const destination = path.join(parent, source.hash);
  if (await exists(destination)) {
    const current = await verifyPackage(destination);
    if (current.hash !== source.hash) throw new Error('Existing immutable package has a different identity.');
    return { ...source, destination, published: false };
  }
  const staging = path.join(parent, `.staging-${randomUUID()}`);
  try {
    await fs.cp(source.root, staging, { recursive: true, errorOnExist: true, force: false });
    const staged = await verifyPackage(staging);
    if (staged.hash !== source.hash) throw new Error('Package identity changed during staging.');
    const unlock = await publicationLock(parent, source.hash);
    try {
      if (await exists(destination)) {
        const current = await verifyPackage(destination);
        if (current.hash !== source.hash) throw new Error('Concurrent package publication has a different identity.');
        return { ...source, destination, published: false };
      }
      await fs.rename(staging, destination);
      return { ...source, destination, published: true };
    } finally { await unlock(); }
  } finally { await fs.rm(staging, { recursive: true, force: true }); }
}

async function main() {
  const args = process.argv.slice(2);
  let result;
  if (args[0] === '--check' && args.length === 2) result = await verifyPackage(args[1]);
  else if (args.length >= 1 && args.length <= 2 && !args[0].startsWith('--')) {
    const defaultConsumer = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
    result = await importPackage(args[0], args[1] || defaultConsumer);
  } else throw new Error('Usage: bun scripts/sync-mewa-ui.mjs DIST_DIRECTORY [CONSUMER_ROOT]\n       bun scripts/sync-mewa-ui.mjs --check PACKAGE_DIRECTORY');
  console.log(JSON.stringify({ hash: result.hash, version: result.manifest.version, source: result.manifest.source, files: result.files, destination: result.destination, published: result.published, urlPrefix: `/ui/mewa-ui/${result.hash}/` }, null, 2));
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
