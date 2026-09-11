import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.dirname(fileURLToPath(import.meta.url));
export default defineConfig({
 root: path.join(root, 'desktop'),
 base: './',
 publicDir: false,
 plugins: [react()],
 resolve: {
  alias: {
   '@': path.join(root, 'src'),
   'next/link': path.join(root, 'src/desktop/router.tsx'),
   'next/navigation': path.join(root, 'src/desktop/router.tsx'),
   'next/dynamic': path.join(root, 'src/desktop/dynamic.tsx'),
  },
  dedupe: ['react', 'react-dom'],
 },
 define: { 'process.env.NEXT_PUBLIC_API_URL': JSON.stringify('') },
 css: { postcss: root },
 build: {
  outDir: path.join(root, '../desktop/internal/desktopui/assets/ui'),
  emptyOutDir: true,
  target: 'es2022',
  sourcemap: false,
  chunkSizeWarningLimit: 2500,
 },
});
