import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { copyFileSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const outputDirectory = '../internal/interfaces/webui/assets';

function copyPretendardVariable() {
  return {
    name: 'copy-pretendard-variable',
    closeBundle() {
      copyFileSync(
        resolve('node_modules/pretendard/dist/web/variable/woff2/PretendardVariable.woff2'),
        resolve(outputDirectory, 'PretendardVariable.woff2'),
      );
      const bundlePath = resolve(outputDirectory, 'app.js');
      const bundle = readFileSync(bundlePath, 'utf8').replace(/[\t ]+$/gm, '');
      writeFileSync(bundlePath, bundle);
    },
  };
}

export default defineConfig({
  plugins: [react(), copyPretendardVariable()],
  publicDir: false,
  define: {
    'process.env.NODE_ENV': JSON.stringify('production'),
  },
  build: {
    outDir: outputDirectory,
    emptyOutDir: false,
    cssCodeSplit: false,
    lib: {
      entry: 'src/main.tsx',
      formats: ['iife'],
      name: 'CinekoDesktop',
      fileName: () => 'app.js',
      cssFileName: 'app',
    },
  },
});
