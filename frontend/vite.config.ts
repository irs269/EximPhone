import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  base: './', // relative paths so the same build works under nginx and inside Electron
  server: { proxy: { '/api': { target: 'https://localhost', secure: false, ws: true } } },
});
