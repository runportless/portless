import { test as base, expect } from '@playwright/test';
import { createInstallation } from './installation.mjs';

export const test = base.extend({
  portless: async ({}, use, testInfo) => {
    const installation = await createInstallation(testInfo.outputPath('diagnostics'));
    try {
      await installation.start();
      await use(installation);
    } finally {
      await installation.stop();
    }
  },
});
export { expect };
