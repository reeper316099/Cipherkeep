import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';
import { APP_NAME } from '../../packages/shared/src/constants.ts';

const API = process.env.CIPHERKEEP_API ?? 'http://127.0.0.1:5174';
const TOKEN = process.env.CIPHERKEEP_TOKEN ?? 'dev-token';

/**
 * Strict CSP for the production bundle: no inline scripts, no remote origins. The desktop build (`--mode wails`)
 * answers the API in process, so it may reach only its own origin; a plain build may also reach a loopback API port
 * (the Go dev server).
 */
function csp(mode: string): Plugin {
  const connect = mode === 'wails' ? "'self'" : "'self' http://127.0.0.1:*";
  return {
    name: 'ck-csp',
    apply: 'build',
    transformIndexHtml: () => [
      {
        tag: 'meta',
        attrs: {
          'http-equiv': 'Content-Security-Policy',
          content: `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src ${connect}; object-src 'none'; base-uri 'none'; form-action 'none'`,
        },
        injectTo: 'head-prepend',
      },
    ],
  };
}

/** Single source of truth for the product name: replaces %APP_NAME% in index.html. */
function appName(): Plugin {
  return {
    name: 'ck-app-name',
    transformIndexHtml: (html) => html.replaceAll('%APP_NAME%', APP_NAME),
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), appName(), csp(mode)],
  base: './', // relative asset paths, so the bundle works wherever it is served from
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // Dev only: the proxy adds the bearer token so the browser never needs it.
      '/api': { target: API, changeOrigin: true, headers: { authorization: `Bearer ${TOKEN}` } },
    },
  },
  build: { outDir: 'dist', sourcemap: false, chunkSizeWarningLimit: 900 },
}));
