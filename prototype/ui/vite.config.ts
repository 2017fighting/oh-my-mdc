import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// PROTOTYPE ONLY — throwaway. Not part of the Go binary.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: { port: 5273, host: '127.0.0.1' },
});
