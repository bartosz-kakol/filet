import { defineConfig, devices } from '@playwright/test';

// Each test starts its own Filet server (bin/filet-test, built with -tags
// filettest) on a free port with fresh temp places; see e2e/fixtures.ts.
export default defineConfig({
	testDir: 'e2e',
	timeout: 120_000,
	expect: { timeout: 15_000 },
	workers: process.env.CI ? 2 : 3,
	reporter: [['list']],
	use: { trace: 'retain-on-failure', acceptDownloads: true },
	projects: [
		{ name: 'chromium', use: { ...devices['Desktop Chrome'] } },
		{ name: 'firefox', use: { ...devices['Desktop Firefox'] } },
		{ name: 'iphone', use: { ...devices['iPhone 15'] } },
		{ name: 'pixel', use: { ...devices['Pixel 7'] } }
	]
});
