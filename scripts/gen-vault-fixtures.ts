// Writes fixtures/compat/vault/node: a vault.json made by the TypeScript vault, its recovery key, and a file encrypted
// with the master key. The Go vault must unlock it with the passphrase and with the recovery key, and read the file.
// (fixtures/compat/vault/go is the other direction, written by `UPDATE_COMPAT=1 go test ./internal/vault`.)
// The passphrase is public test data; it protects nothing. Re-run only if the vault format changes:
//   npx tsx scripts/gen-vault-fixtures.ts
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { AesGcmCodec } from '../packages/jsonstore/src/index.ts';
import { Vault } from '../packages/server/src/vault.ts';

export const VAULT_PASSPHRASE = 'compat passphrase, public test data';
export const VAULT_SECRET = 'vault compat ok: ünïcode 😀';

const out = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
  'fixtures',
  'compat',
  'vault',
  'node',
);
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
const { key, recoveryKey } = await new Vault(out, { algo: 'scrypt', N: 1024, r: 8, p: 1 }).create(
  VAULT_PASSPHRASE,
);
writeFileSync(path.join(out, 'recovery.txt'), recoveryKey + '\n');
writeFileSync(
  path.join(out, 'secret.bin'),
  new AesGcmCodec(key).encode(Buffer.from(VAULT_SECRET, 'utf8')),
);
console.log(`wrote ${out}`);
