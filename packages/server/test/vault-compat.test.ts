import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { AesGcmCodec } from '@cipherkeep/jsonstore';
import { Vault } from '../src/vault.ts';

// fixtures/compat/vault/go was written by the Go vault (UPDATE_COMPAT=1 go test ./internal/vault); /node by
// scripts/gen-vault-fixtures.ts. Node must unlock what Go wrote, and the Go tests check the other direction.
const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../fixtures/compat/vault',
);
const PASSPHRASE = 'compat passphrase, public test data';
const SECRET = 'vault compat ok: ünïcode 😀';

describe('vault made by the Go version', () => {
  it('unlocks with the passphrase and the recovery key, and the key reads the data', async () => {
    const dir = path.join(root, 'go');
    const v = new Vault(dir);
    const k1 = await v.unlockWithPassphrase(PASSPHRASE);
    const k2 = await v.unlockWithRecoveryKey(
      readFileSync(path.join(dir, 'recovery.txt'), 'utf8').trim(),
    );
    expect(k1.equals(k2)).toBe(true);
    const plain = new AesGcmCodec(k1).decode(readFileSync(path.join(dir, 'secret.bin')));
    expect(plain.toString('utf8')).toBe(SECRET);
    await expect(v.unlockWithPassphrase('not the passphrase')).rejects.toMatchObject({
      code: 'BAD_CREDENTIALS',
    });
  });
});
