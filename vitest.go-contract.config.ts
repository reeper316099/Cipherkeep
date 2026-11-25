import { defineConfig } from 'vitest/config';

// Runs the TypeScript API, vault and UI-serving tests against the Go server (go/cmd/ck-server): `npm run test:go-contract`.
export default defineConfig({
  test: {
    include: [
      'packages/server/test/api.test.ts',
      'packages/server/test/vault.test.ts',
      'packages/server/test/static.test.ts',
    ],
    environment: 'node',
    globalSetup: ['packages/server/test/go-contract-setup.ts'],
    testTimeout: 60_000,
    env: { CK_SERVER: 'go' },
  },
});
