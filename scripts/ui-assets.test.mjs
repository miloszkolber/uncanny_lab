import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { verifyPackage } from './sync-mewa-ui.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const hash = '510d5083135db0edd06415d36b2d9096b968e354aa86acba9c132f90ad8f4fdd';
const prefix = `/ui/mewa-ui/${hash}/`;
const packageRoot = path.join(root, 'ui/mewa-ui', hash);

test('the complete imported Mewa package retains its verified dirty-snapshot identity', async () => {
  const result = await verifyPackage(packageRoot);
  assert.equal(result.hash, hash);
  assert.equal(result.manifest.version, '0.2.0');
  assert.equal(result.manifest.source.dirty, true);
});

test('the UI loads packaged CSS, fonts, and automatic modules with a complete local dependency graph', async () => {
  const html = await fs.readFile(path.join(root, 'web/static/index.html'), 'utf8');
  for (const entry of ['css/base.css', 'css/tokens.css', 'fonts/google-sans-code.css', 'fonts/google-sans-code.woff2']) assert.ok(html.includes(prefix + entry));
  assert.doesNotMatch(html, /\/ui\/(?:src|components)\//);
  assert.doesNotMatch(html, /data-size=|<script>|<style>/);
  const queue = [];
  for (const [, ref] of html.matchAll(/(?:href|src)="(\/ui\/[^"]+)"/g)) {
    assert.ok(ref.startsWith(prefix));
    if (ref.endsWith('.js')) assert.ok(ref.startsWith(`${prefix}auto/`));
    queue.push(path.join(root, ref.slice(1)));
  }
  const visited = new Set();
  while (queue.length) {
    const entry = queue.shift();
    if (visited.has(entry)) continue;
    visited.add(entry);
    const relative = path.relative(packageRoot, entry);
    assert.ok(relative && !relative.startsWith('..') && !path.isAbsolute(relative), `dependency escapes package: ${entry}`);
    const data = await fs.readFile(entry);
    if (entry.endsWith('.woff2')) continue;
    const source = data.toString('utf8');
    const refs = entry.endsWith('.js')
      ? [...source.matchAll(/(?:from\s*|import\s*)['"](\.[^'"]+)['"]/g)].map(match => match[1])
      : [...source.matchAll(/url\(['"]?(\.[^'"\)]+)['"]?\)/g)].map(match => match[1]);
    for (const ref of refs) queue.push(path.resolve(path.dirname(entry), ref));
  }
  assert.ok([...visited].some(entry => entry.includes('/runtime/')));
  assert.ok([...visited].some(entry => entry.includes('/controllers/')));
  assert.ok([...visited].some(entry => entry.endsWith('/fonts/google-sans-code.woff2')));
});

test('consumer CSS uses current defined foundation roles without private control-state overrides', async () => {
  const consumer = await fs.readFile(path.join(root, 'web/static/styles.css'), 'utf8');
  const foundation = await fs.readFile(path.join(packageRoot, 'css/base.css'), 'utf8') + await fs.readFile(path.join(packageRoot, 'css/tokens.css'), 'utf8');
  const definitions = new Set([...`${foundation}\n${consumer}`.matchAll(/(--[a-zA-Z0-9-]+)\s*:/g)].map(match => match[1]));
  for (const [, role] of consumer.matchAll(/var\((--[a-zA-Z0-9-]+)\s*\)/g)) assert.ok(definitions.has(role), `undefined role ${role}`);
  assert.doesNotMatch(consumer, /--(?:text-muted|font-weight-550|focus-ring-width)|\.btn\[aria-busy/);
});
