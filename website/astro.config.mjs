// @ts-check
import { defineConfig } from 'astro/config';

import tailwindcss from '@tailwindcss/vite';

import sitemap from '@astrojs/sitemap';

import rebaseAbsoluteUrls from './integrations/rebase-absolute-urls.mjs';

// A PR preview is served from a subdirectory of the production domain
// (/pr-preview/pr-<N>/), so the whole site has to be built knowing that prefix
// -- otherwise every asset URL resolves against the domain root and 404s while
// the page still renders, which is a broken preview that CI reports as green.
//
// Unset, as it is in every production build, this is '/' and the build is
// exactly what it was before.
const base = process.env.BASE_PATH ?? '/';

// https://astro.build/config
export default defineConfig({
  site: 'https://mcp-anything.ai',
  base,
  output: 'static',

  vite: {
    plugins: [tailwindcss()]
  },

  // rebaseAbsoluteUrls runs after the build and is a no-op when base is '/'.
  // It covers the URLs Astro cannot rewrite: the hand-written ones.
  integrations: [sitemap(), rebaseAbsoluteUrls()]
});