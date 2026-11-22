// Builds the shared library the Go import/export code is compared against (fixtures/compat/importers/library) and records
// what the TypeScript implementation returns (expected.json). Re-run only when behaviour is meant to change:
//   npx tsx scripts/gen-importer-fixtures.ts
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createContext } from '../packages/server/src/context.ts';
import { commitPlan } from '../packages/server/src/import/index.ts';
import { planNotion } from '../packages/server/src/import/notion.ts';
import { fromFolder } from '../packages/server/src/import/source.ts';
import { FIXTURE, computeExpected } from './importers-expected.ts';

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
  'fixtures',
  'compat',
  'importers',
);
const lib = path.join(root, 'library');
rmSync(root, { recursive: true, force: true });
mkdirSync(lib, { recursive: true });

const ctx = await createContext({ dataDir: lib, watch: false });
const vfs = await fromFolder(FIXTURE);
await commitPlan(
  ctx,
  planNotion(vfs, { sourceName: 'notion-export' }),
  { strategy: 'update-empty-only' },
  { vfs },
);
await ctx.repos.labs.create({
  title: 'Secret Lab',
  platform: 'Bandit',
  writeup: 'the flag is here',
});
await ctx.repos.labs.create({
  title: 'Open Lab',
  platform: 'Bandit',
  writeup: 'public notes',
  writeupPrivate: false,
});
await ctx.repos.labs.create({
  title: 'Hidden, "quoted"\nLab',
  platform: 'Bandit',
  writeup: 'line one\nline two | pipe',
  writeupPrivate: true,
  tags: ['x', 'y z'],
});
await ctx.repos.commands.create({
  name: '=cmd',
  category: 'General',
  syntax: '=cmd',
  options: [{ flag: '-x', meaning: 'x' }],
});
await ctx.repos.notes.create({
  title: 'Wiki Note',
  folder: 'a/b',
  markdown: 'See [[ls]] and [[Directory Tree]]',
  tags: ['t'],
});
await ctx.close();
// Only the library content is the fixture: no backups, import records, lock or activity log.
for (const d of ['backups', 'imports', 'activity'])
  rmSync(path.join(lib, d), { recursive: true, force: true });
rmSync(path.join(lib, '.lock'), { force: true });

const expected = await computeExpected(lib);
writeFileSync(path.join(root, 'expected.json'), JSON.stringify(expected, null, 1) + '\n');
console.log('wrote', path.join(root, 'expected.json'));
