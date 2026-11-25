import { rmSync } from 'node:fs';
import { goBinary } from './helpers.ts';

// Builds the Go server once before the contract tests start. On a fresh CI machine with no Go build cache this takes
// longer than a test hook may, and building it in every test file at once only makes that worse.
export default function setup(): () => void {
  const bin = goBinary();
  process.env.CK_SERVER_BIN = bin;
  return () => rmSync(bin, { force: true });
}
