import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { computeExpected } from '../../../scripts/parity-expected.ts';

// fixtures/compat/parity/expected.json is what the Go port is compared against. This test keeps it honest: if the
// TypeScript behaviour changes on purpose, regenerate the fixtures (npx tsx scripts/gen-parity-fixtures.ts) and the Go
// parity tests (go test ./internal/library) then show whether Go still agrees.
const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../fixtures/compat/parity',
);

describe('parity fixtures', () => {
  it('expected.json is still what the TypeScript code returns for the shared library', async () => {
    const want = JSON.parse(readFileSync(path.join(root, 'expected.json'), 'utf8'));
    const got = JSON.parse(JSON.stringify(await computeExpected(path.join(root, 'library'))));
    expect(got).toEqual(want);
  }, 60_000);
});
