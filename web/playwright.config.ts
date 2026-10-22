import { defineConfig, devices } from '@playwright/test';
export default defineConfig({
  testDir:'./e2e', fullyParallel:true,
  // CI shares a busy machine: give slow steps room and retry a flaky test
  ...(process.env.CI ? {timeout:60_000, expect:{timeout:15_000}, retries:2} : {}),
  reporter:process.env.CI ? [['line'],['html',{open:'never'}]] : 'line',
  use:{baseURL:'http://127.0.0.1:5173',trace:'retain-on-failure',screenshot:'only-on-failure'},
  webServer:{command:'node node_modules/vite/bin/vite.js --host 127.0.0.1',url:'http://127.0.0.1:5173',reuseExistingServer:!process.env.CI},
  projects:[{name:'chromium',use:{...devices['Desktop Chrome']}}],
});
