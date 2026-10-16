import { defineConfig, devices } from '@playwright/test';
// Phone-profile load measurement (npm run perf:phone): the production build
// served by vite preview, a mid-range phone with a 4x slower CPU and slow
// 4G. It reports numbers; it isn't part of the e2e suite. PERF_URL measures
// a running mangarr instead, which serves the files the way production does.
const url = process.env.PERF_URL;
export default defineConfig({
  testDir:'./perf', workers:1, reporter:'line',
  use:{baseURL:url ?? 'http://127.0.0.1:4173'},
  webServer:url ? undefined : {command:'node node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port 4173 --strictPort',url:'http://127.0.0.1:4173',reuseExistingServer:!process.env.CI},
  projects:[{name:'phone',use:{...devices['Pixel 7']}}],
});
