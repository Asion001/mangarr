import { defineConfig, devices } from '@playwright/test';
// README screenshots (npm run screenshots): the production build with a
// made-up library, written to docs/screenshots.
export default defineConfig({
  testDir:'./screenshots', workers:1, reporter:'line',
  use:{baseURL:'http://127.0.0.1:4174', ...devices['Desktop Chrome'], colorScheme:'dark'},
  webServer:{command:'node node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port 4174 --strictPort',url:'http://127.0.0.1:4174',reuseExistingServer:!process.env.CI},
});
