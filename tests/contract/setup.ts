import { rmSync } from 'node:fs';
import { goBinary } from './helpers.ts';

// Builds the Go server once before the contract tests start (on a fresh CI machine this takes longer than a test
// hook may).
export default function setup(): () => void {
  const bin = goBinary();
  process.env.CK_SERVER_BIN = bin;
  return () => rmSync(bin, { force: true });
}
