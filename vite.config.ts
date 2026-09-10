import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  base: '/R2RProcessControl/',
  plugins: [react()],
  root: 'frontend',
  build: { outDir: '../dist', emptyOutDir: true },
  server: {
    port: 5173,
    proxy: {
      '/R2RProcessControl/api': {
        target: 'http://localhost:4173',
      },
    },
  },
});
