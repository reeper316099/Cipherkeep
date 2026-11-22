import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { computeExpected } from '../../../scripts/importers-expected.ts';
import { csvTemplate } from '../src/export.ts';
import { planNotion } from '../src/import/notion.ts';
import { VirtualFs } from '../src/import/source.ts';

// fixtures/compat/importers/expected.json is what the Go import/export code is compared against. This test keeps it
// honest: if the TypeScript behaviour changes on purpose, regenerate the fixtures (npx tsx scripts/gen-importer-fixtures.ts)
// and the Go tests (go test ./internal/parity) then show whether Go still agrees.
const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../fixtures/compat/importers',
);

describe('importer parity fixtures', () => {
  it('expected.json is still what the TypeScript code returns for the shared inputs', async () => {
    const want = JSON.parse(readFileSync(path.join(root, 'expected.json'), 'utf8'));
    const got = JSON.parse(JSON.stringify(await computeExpected(path.join(root, 'library'))));
    expect(got).toEqual(want);
  }, 60_000);
});

describe('found while porting to Go', () => {
  it('a CSV template ends in one newline, not a stray carriage return', () => {
    expect(csvTemplate('labs')).toMatch(/[^\r\n]\n$/);
  });

  it('a snippet description is never cut through the middle of an emoji', () => {
    const vfs = new VirtualFs();
    vfs.add('Page.md', Buffer.from(`# Page\n\n${'x'.repeat(299)}\u{1F600} tail\n`, 'utf8'));
    const item = planNotion(vfs, { sourceName: 't' }).items[0]!;
    const d = item.alternatives!.snippets!.description as string;
    expect(d).toHaveLength(299);
    expect(/[\ud800-\udbff]$/.test(d)).toBe(false);
  });
});
