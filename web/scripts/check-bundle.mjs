// Fails the build when the JavaScript a first visit downloads (the entry
// script and the chunks index.html preloads) grows past the budget, gzipped.
// Pages load lazily, so a page's own code doesn't count here.
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
const BUDGET_KB = 140;
const dist = new URL('../dist/', import.meta.url).pathname;
const html = fs.readFileSync(path.join(dist, 'index.html'), 'utf8');
const files = [...html.matchAll(/(?:src|href)="\.?\/?(assets\/[^"]+\.js)"/g)].map((m) => m[1]);
if (files.length === 0) { console.error('check-bundle: no scripts found in dist/index.html (build first)'); process.exit(1); }
let total = 0;
for (const f of new Set(files)) {
  const kb = zlib.gzipSync(fs.readFileSync(path.join(dist, f))).length / 1024;
  total += kb;
  console.log(`${f.padEnd(40)} ${kb.toFixed(1)} kB gzip`);
}
console.log(`first load: ${total.toFixed(1)} kB gzip (budget ${BUDGET_KB} kB)`);
if (total > BUDGET_KB) { console.error('check-bundle: over budget; load the new code lazily or raise the budget on purpose'); process.exit(1); }
