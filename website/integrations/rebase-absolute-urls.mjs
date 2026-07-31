/**
 * Rebase hand-written root-absolute URLs in the emitted HTML onto Astro's `base`.
 *
 * Astro rewrites the URLs it generates itself -- the hashed `/_astro/*` bundles
 * -- when `base` is set. It cannot rewrite a hand-written `href="/docs/routing"`
 * or `href="/favicon.svg"`, and this site has 71 of those across 24 files.
 *
 * That gap is the silent half of the base-path problem. Built for a preview at
 * /pr-preview/pr-<N>/, the stylesheets would load (Astro rebased those) while
 * every navigation link and every file from public/ still pointed at the
 * production domain root -- so a reviewer clicking "Docs" in the preview would
 * land on the live site and review the wrong content, with nothing failing.
 *
 * Doing it here rather than at 71 call sites keeps this off the page files and
 * makes the rule impossible to forget on the next page someone adds.
 *
 * A production build sets no BASE_PATH, so `base` is '/' and this integration
 * returns before touching a single file: the emitted HTML is byte-identical to
 * what it was before this existed.
 */

import { readdir, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

// The attributes this site puts local URLs in. `content` is deliberately
// excluded: it carries the og:/twitter: image tags, which are absolute
// production URLs that should keep pointing at production.
const URL_ATTR = /(\s(?:href|src)=")(\/(?!\/)[^"]*)"/g;

// A preview is a verbatim copy of the site at a second URL on the production
// domain. Left indexable, it competes with the real pages in search results.
const NOINDEX = '<meta name="robots" content="noindex, nofollow" />';

async function htmlFilesIn(dir) {
  const found = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) found.push(...(await htmlFilesIn(full)));
    else if (entry.name.endsWith('.html')) found.push(full);
  }
  return found;
}

export default function rebaseAbsoluteUrls() {
  let base = '/';

  return {
    name: 'rebase-absolute-urls',
    hooks: {
      'astro:config:done': ({ config }) => {
        base = config.base;
      },

      'astro:build:done': async ({ dir, logger }) => {
        // Astro normalises `base` for us, but whether it keeps a trailing
        // slash depends on `trailingSlash`, so do not depend on either form.
        const prefix = base.replace(/\/+$/, '');
        if (!prefix) {
          logger.info('base is "/" -- no rebasing needed (production build)');
          return;
        }

        const files = await htmlFilesIn(fileURLToPath(dir));
        let rewritten = 0;

        for (const file of files) {
          const html = await readFile(file, 'utf8');

          let count = 0;
          let out = html.replace(URL_ATTR, (match, attr, url) => {
            // Already under the prefix: Astro rebased this one itself.
            if (url === prefix || url.startsWith(`${prefix}/`)) return match;
            count += 1;
            return `${attr}${prefix}${url}"`;
          });

          if (!out.includes('name="robots"')) {
            out = out.replace('</head>', `${NOINDEX}</head>`);
          }

          if (out !== html) {
            await writeFile(file, out);
            rewritten += count;
          }
        }

        logger.info(`rebased ${rewritten} absolute URL(s) in ${files.length} page(s) onto ${prefix}`);
      },
    },
  };
}
