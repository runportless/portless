import sitemap from '@astrojs/sitemap';
import {defineConfig} from 'astro/config';

export default defineConfig({
  site: 'https://www.portless.run',
  output: 'static',
  build: {
    format: 'file',
  },
  integrations: [sitemap()],
});
