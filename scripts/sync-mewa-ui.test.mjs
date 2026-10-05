import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import fs from 'node:fs/promises';
import { watch } from 'node:fs';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { importPackage, verifyPackage } from './sync-mewa-ui.mjs';

const root = await fs.mkdtemp(path.join(tmpdir(), 'mewa-ui-import-test-'));
const source = path.join(root, 'source');
const consumer = path.join(root, 'consumer');
await fs.mkdir(source);
await fs.mkdir(consumer);
const validManifest = { schemaVersion: 1, name: 'mewa-ui', version: 'test', foundations: { base: 'base.css', tokens: 'base.css', fonts: [] }, licenses: {}, components: [{ slug: 'example', css: 'base.css', auto: null, component: null, controller: null }], source: { dirty: true } };
const manifest = JSON.stringify(validManifest);
const css = 'body { color: black; }\n';
const digest = value => createHash('sha256').update(value).digest('hex');
await fs.writeFile(path.join(source, 'manifest.json'), manifest);
await fs.writeFile(path.join(source, 'base.css'), css);
await fs.writeFile(path.join(source, 'checksums.json'), JSON.stringify({ algorithm: 'sha256', files: { 'manifest.json': digest(manifest), 'base.css': digest(css) } }));
try {
  const expectedHash = digest(await fs.readFile(path.join(source, 'checksums.json')));
  const imported = await importPackage(source, consumer);
  assert.equal(imported.hash, expectedHash);
  assert.equal(imported.published, true);
  assert.equal(await fs.readFile(path.join(imported.destination, 'base.css'), 'utf8'), css);
  assert.equal((await importPackage(source, consumer)).published, false);
  const concurrentConsumer = path.join(root, 'concurrent-consumer');
  await fs.mkdir(concurrentConsumer);
  const concurrent = await Promise.all([importPackage(source, concurrentConsumer), importPackage(source, concurrentConsumer)]);
  assert.equal(concurrent.filter(result => result.published).length, 1);
  assert.deepEqual(await fs.readdir(path.join(concurrentConsumer, 'ui/mewa-ui')), [expectedHash]);
  const collisionConsumer = path.join(root, 'collision-consumer');
  const collisionParent = path.join(collisionConsumer, 'ui/mewa-ui');
  await fs.mkdir(collisionParent, { recursive: true });
  const collisionDestination = path.join(collisionParent, expectedHash);
  let collisionInode;
  let watcher;
  let timeout;
  const createdCollision = new Promise((resolve, reject) => {
    timeout = setTimeout(() => reject(new Error('Staging publication was not observed')), 5000);
    watcher = watch(collisionParent, (event, name) => {
      if (!name?.startsWith('.staging-')) return;
      watcher.close();
      fs.mkdir(collisionDestination).then(() => fs.stat(collisionDestination)).then(stat => { collisionInode = stat.ino; resolve(); }, reject);
    });
  });
  try {
    await assert.rejects(importPackage(source, collisionConsumer), /ENOENT|inventory mismatch/);
    await createdCollision;
    assert.equal((await fs.stat(collisionDestination)).ino, collisionInode);
    assert.deepEqual(await fs.readdir(collisionDestination), []);
    assert.deepEqual(await fs.readdir(collisionParent), [expectedHash]);
  } finally { clearTimeout(timeout); watcher?.close(); }
  for (const invalid of [
    { ...validManifest, foundations: { ...validManifest.foundations, base: '../outside.css' } },
    { ...validManifest, foundations: { ...validManifest.foundations, tokens: 'missing.css' } },
    { ...validManifest, foundations: { ...validManifest.foundations, fonts: ['/absolute.woff2'] } },
    { ...validManifest, components: [{ slug: 'example', css: 'missing.css' }] },
  ]) {
    const text = JSON.stringify(invalid);
    await fs.writeFile(path.join(source, 'manifest.json'), text);
    await fs.writeFile(path.join(source, 'checksums.json'), JSON.stringify({ algorithm: 'sha256', files: { 'manifest.json': digest(text), 'base.css': digest(css) } }));
    await assert.rejects(verifyPackage(source), /manifest entry/);
  }
  await fs.writeFile(path.join(source, 'manifest.json'), manifest);
  await fs.writeFile(path.join(source, 'checksums.json'), JSON.stringify({ algorithm: 'sha256', files: { 'manifest.json': digest(manifest), 'base.css': digest(css) } }));
  await fs.writeFile(path.join(source, 'base.css'), 'corrupt');
  await assert.rejects(importPackage(source, consumer), /checksum mismatch/);
  assert.equal(await fs.readFile(path.join(imported.destination, 'base.css'), 'utf8'), css);
  await fs.writeFile(path.join(source, 'base.css'), css);
  await fs.writeFile(path.join(source, 'extra.txt'), 'unlisted');
  await assert.rejects(verifyPackage(source), /Extra: extra.txt/);
  await fs.rm(path.join(source, 'extra.txt'));
  await fs.rm(path.join(source, 'base.css'));
  await assert.rejects(verifyPackage(source), /Missing: base.css/);
  await fs.symlink(path.join(imported.destination, 'base.css'), path.join(source, 'base.css'));
  await assert.rejects(verifyPackage(source), /symlink is not allowed/);
  await fs.rm(path.join(source, 'base.css'));
  await fs.writeFile(path.join(source, 'base.css'), css);
  await fs.writeFile(path.join(imported.destination, 'base.css'), 'changed immutable bytes');
  await assert.rejects(importPackage(source, consumer), /checksum mismatch/);
  assert.deepEqual((await fs.readdir(path.join(consumer, 'ui/mewa-ui'))), [expectedHash]);
  console.log('Importer checks passed: publication, no-op, concurrent publication, late destination protection, manifest references, corruption, extra/missing files, symlinks, existing-package protection, and staging cleanup.');
} finally { await fs.rm(root, { recursive: true, force: true }); }
