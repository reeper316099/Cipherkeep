import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    projects: [
      {
        test: {
          name: 'node',
          include: ['packages/*/test/**/*.test.ts', 'scripts/**/*.test.ts'],
          environment: 'node',
          // Hosted Windows runners are several times slower than a laptop.
          testTimeout: 30_000,
        },
      },
      {
        // The API contract: the TypeScript API, vault and UI-serving tests, run against the Go server.
        test: {
          name: 'contract',
          include: ['tests/contract/**/*.test.ts'],
          environment: 'node',
          globalSetup: ['tests/contract/setup.ts'],
          testTimeout: 60_000,
        },
      },
      {
        plugins: [react()],
        test: {
          name: 'web',
          include: ['apps/web/test/**/*.test.{ts,tsx}'],
          environment: 'jsdom',
          setupFiles: ['apps/web/test/setup.ts'],
          css: false,
        },
      },
    ],
  },
});
