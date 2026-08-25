import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import fs from 'fs';

export default defineConfig(() => {
  const udsPath = path.resolve(__dirname, '../../../uds/packages/ai-design-system');
  const hasLocalUds = fs.existsSync(udsPath);

  return {
    plugins: [react()],
    resolve: {
      alias: [
        { find: '@', replacement: path.resolve(__dirname, './src') },
        ...(hasLocalUds
          ? [
              {
                find: /^ai-design-system$/,
                replacement: path.resolve(udsPath, 'dist/index.js'),
              },
              {
                find: /^ai-design-system\//,
                replacement: udsPath + '/',
              },
            ]
          : []),
      ],
    },
    server: {
      port: 3000,
      watch: {
        followSymlinks: true,
      },
      proxy: {
        '/self-service': {
          target: 'http://localhost:4433',
          changeOrigin: true,
        },
      },
    },
  };
});
