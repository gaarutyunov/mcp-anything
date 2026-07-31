# Vendored `@gaarutyunov/ui-kit`

Source: [gaarutyunov/ui-kit](https://github.com/gaarutyunov/ui-kit)
Pinned at **v0.3.0** (`a7b5a7107230fa5d6aead3dcdc4e93ab030526bd`).

## Why vendored rather than installed

The kit publishes to **GitHub Packages**, which requires authentication even for
public packages — an anonymous `GET https://npm.pkg.github.com/@gaarutyunov/ui-kit`
answers `401`. A workflow's `GITHUB_TOKEN` is scoped to *this* repository, so it
cannot read a package owned by another one; `npm ci` would need a long-lived PAT
in repository secrets just to install a zero-dependency set of web components.
`gaarutyunov/epos` and `gaarutyunov/gopgql` vendor it for the same reason.

The kit is dependency-free ES modules and CSS, so vendoring costs nothing at
build time and keeps the website build reproducible with no secrets.

## How it is wired in

- `src/tokens/tokens.css` is imported from `src/styles/global.css`. It defines
  the `--ga-*` design tokens for both themes: the dark palette on `:root`, and a
  light palette on `:root[data-theme="light"]`. The site's theme toggle sets
  that `data-theme` attribute, so the kit's own tokens drive both themes.
- `src/index.js` registers the `<ga-*>` custom elements. It is imported from a
  `<script>` tag in the layouts, **never** from Astro frontmatter: the components
  extend `HTMLElement`, which does not exist while Astro pre-renders in Node, so
  registration has to happen in the browser.

## Updating

```bash
git -C <path-to>/ui-kit archive vX.Y.Z src LICENSE README.md \
  | tar -x -C website/vendor/ui-kit
```

Then update the version and commit above.
