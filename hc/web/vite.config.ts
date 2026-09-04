import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@harness/hc-core': fileURLToPath(new URL('../packages/core/src/index.ts', import.meta.url)),
    },
  },
  server: {
    port: 4173,
    proxy: {
      '/admin/api': {
        changeOrigin: false,
        target: 'http://127.0.0.1:8080',
      },
      '/gateway/v1': {
        changeOrigin: false,
        target: 'http://127.0.0.1:8082',
        ws: true,
      },
    },
    strictPort: true,
  },
});
