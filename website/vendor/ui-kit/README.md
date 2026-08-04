# GA UI Kit

A **universal, zero-dependency UI kit** built on native **Web Components**. One
component set that works in vanilla JS, React, Astro, Vue, Svelte, SolidJS — or
no framework at all.

The visual language is distilled from two projects into a single Geist-inspired,
pure-black design system:

- [**garutyunov.com**](https://github.com/gaarutyunov/garutyunov.com) — a Next.js portfolio
- [**stereoscope**](https://github.com/gaarutyunov/stereoscope) — a buildless WebGPU image converter

📖 **[Live docs & playground →](https://gaarutyunov.github.io/ui-kit/)**

> The documentation site is **built from the kit's own components** — a
> buildless, framework-free SPA (hash routing + ES modules) with an
> interactive playground, API tables and a dark/light toggle. No Storybook, no
> bundler, so it renders in **any browser, including mobile Safari**.

---

## Why Web Components?

Custom elements (`<ga-button>`, `<ga-card>`, …) are part of the HTML standard, so
the same kit drops into any stack with no framework-specific adapter:

| Concern | How the kit handles it |
| --- | --- |
| **Framework portability** | Native custom elements — no React/Vue build |
| **Style isolation** | Shadow DOM; host page styles can't leak in |
| **Theming** | CSS custom properties pierce the shadow boundary |
| **Runtime weight** | ~3 KB base class, no Lit/Stencil dependency |

## Components

`ga-button` · `ga-radio-group` · `ga-badge` · `ga-card` · `ga-avatar` ·
`ga-input` · `ga-switch` · `ga-spinner` · `ga-alert` · `ga-kbd` · `ga-code` ·
`ga-tabs` · `ga-breadcrumbs` · `ga-table` · `ga-note` · `ga-slider` ·
`ga-file-drop` · `ga-fab` · `ga-panel` · `ga-header` · `ga-bottom-nav` ·
`ga-bottom-sheet` · `ga-icon` · `ga-select` · `ga-calendar` ·
`ga-date-input` · `ga-chart-frame` · `ga-chat` · `ga-chat-message`

New in this line-up:

- **`ga-radio-group`** — a single-select control in the segmented-pill style
  (config via `items` JSON + a reflected `value`; form-associated, arrow-key nav).
- **`ga-code`** — a copyable code / command block (clipboard copy by default,
  or an external `↗` link when given `href`).
- **`ga-breadcrumbs`** — a monospace breadcrumb trail (config via `items` JSON).
- **`ga-table`** — a data table with a shared column grid and slotted light-DOM
  rows, so a whole row can be an `<a href>` and cells stay rich.

Cards and pills follow the
[garutyunov.com](https://github.com/gaarutyunov/garutyunov.com) styling; the
note, slider, file-drop, FAB and panel are ported from
[stereoscope](https://github.com/gaarutyunov/stereoscope).

## Install

### npm (GitHub Packages)

The package is published to **GitHub Packages**. Point the `@gaarutyunov`
scope at the GitHub registry (once), then install:

```ini
# .npmrc
@gaarutyunov:registry=https://npm.pkg.github.com
```

```bash
npm install @gaarutyunov/ui-kit
```

> GitHub Packages requires authentication even for public packages — add a
> personal access token with `read:packages` to your `~/.npmrc`
> (`//npm.pkg.github.com/:_authToken=YOUR_TOKEN`).

### Standalone `<script>` (no build, no npm)

Every release attaches a self-contained bundle. Drop it straight into any page
— it registers all `<ga-*>` elements on load:

```html
<link rel="stylesheet"
  href="https://github.com/gaarutyunov/ui-kit/releases/latest/download/ga-ui-kit.css" />
<script
  src="https://github.com/gaarutyunov/ui-kit/releases/latest/download/ga-ui-kit.min.js"></script>

<ga-button variant="primary">Hello</ga-button>
```

An ES-module build (`ga-ui-kit.esm.js`) is attached too, for
`<script type="module">import`. Pin a version with
`releases/download/vX.Y.Z/…` instead of `releases/latest/…`.

## Usage by framework

### Vanilla HTML / JS

```html
<link rel="stylesheet" href="@gaarutyunov/ui-kit/tokens.css" />
<script type="module">import "@gaarutyunov/ui-kit";</script>

<ga-button variant="primary">Get started</ga-button>
<ga-alert tone="success" title="Done">Saved your changes.</ga-alert>
```

### React

```jsx
import "@gaarutyunov/ui-kit";
import "@gaarutyunov/ui-kit/tokens.css";

export function Demo() {
  // Custom elements are just DOM — props become attributes, events via ref/onEvent.
  return (
    <ga-card interactive>
      <strong>Hello from React</strong>
      <ga-button variant="primary">Click</ga-button>
    </ga-card>
  );
}
```

> React 19 supports custom elements (incl. properties & events) natively. On
> React ≤18, attribute props work out of the box; for custom events attach a
> listener with a `ref`.

For **typed JSX**, reference the opt-in, types-only React entry once — then
`<ga-card>` and friends type-check with their documented attributes (no local
declarations, works with `moduleResolution` `"bundler"` and React 19):

```tsx
import "@gaarutyunov/ui-kit";        // registers the elements (runtime)
import "@gaarutyunov/ui-kit/react";  // teaches JSX about them (types only)
```

See [TypeScript](#typescript) below.

### Astro

```astro
---
import "@gaarutyunov/ui-kit";
import "@gaarutyunov/ui-kit/tokens.css";
---
<ga-tabs tabs='[{"id":"a","label":"One"},{"id":"b","label":"Two"}]'>
  <div slot="a">First panel</div>
  <div slot="b">Second panel</div>
</ga-tabs>
```

### Vue / Svelte / Solid

All three render custom elements directly. In Vue, mark `ga-*` as custom
elements in your compiler options (`isCustomElement: tag => tag.startsWith('ga-')`).

## TypeScript

Types ship **inside** the kit — there's no `@types/…` package to install. The
per-element declarations are generated from the same JSDoc that documents the
components (`tsc --allowJs --declaration --emitDeclarationOnly`; `typescript` is
a dev-only dependency, so the kit stays zero-runtime-dependency).

Three things are wired up:

1. **Class declarations** beside every component, so
   `import { GaButton } from "@gaarutyunov/ui-kit"` is fully typed.
2. **`HTMLElementTagNameMap`** is augmented from the main entry, so DOM lookups
   are typed automatically — for vanilla, Vue, Svelte and Solid users alike:

   ```ts
   import "@gaarutyunov/ui-kit";
   const card = document.querySelector("ga-card"); // GaCard | null
   const code = document.createElement("ga-code");  // GaCode
   ```

3. A separate, **types-only** `@gaarutyunov/ui-kit/react` entry that augments
   `React.JSX.IntrinsicElements` with every tag and its attributes (boolean
   attributes accept `"" | boolean`). Reference it once, anywhere:

   ```tsx
   import "@gaarutyunov/ui-kit/react";

   <ga-card interactive padding="lg">
     <ga-button variant="primary" href="/dl" download="report.pdf">Download</ga-button>
   </ga-card>;
   ```

   The React entry is deliberately **not** pulled in by the main entry, so
   importing the kit never touches React's JSX — vanilla / Vue / Svelte / Solid
   projects are unaffected. Requires a bundler-style `moduleResolution`
   (`"bundler"`, `"node16"` or `"nodenext"`); works with React 19.

Regenerate the declarations after changing a component's JSDoc:

```bash
npm run types
```

## Theming

Re-brand the whole kit by overriding a few CSS variables — the same
`--accent` / `--radius` pattern stereoscope uses:

```css
:root {
  --ga-accent: #ac4bff;          /* purple instead of blue */
  --ga-radius: 10px;
  --ga-font-sans: "Inter", system-ui, sans-serif;
}
```

Opt into the bundled **light theme**:

```html
<html data-theme="light">
```

See [`src/tokens/tokens.css`](src/tokens/tokens.css) for the full token set
(palette, typography, spacing, elevation, motion).

## Develop

The kit itself is **zero runtime dependency**, and the docs site is buildless —
so running and developing the kit needs nothing installed. You only need Node to
run the tiny static dev server (ES modules must be served over `http://`, not
`file://`):

```bash
npm run dev     # docs site at http://localhost:8000
npm run build   # assemble the static site → dist/
npm run types   # regenerate the .d.ts from JSDoc (needs the dev-only typescript)
```

> The only `devDependency` is **`typescript`**, used solely to emit the type
> declarations — it is never shipped or required at runtime.

Layout:

- `src/` — the kit. Each component lives in `src/components/<name>/<name>.js`;
  `src/core/base-element.js` is the ~3 KB base class; `src/tokens/tokens.css`
  holds the design tokens.
- `site/` — the docs site (`app.js` router/renderer, `app.css`, `registry.js`
  content), built from the `ga-*` components themselves.
- `scripts/` — `build.mjs` (copies `index.html` + `site/` + `src/` into
  `dist/`), `serve.mjs` (dev server), `bundle.mjs` (esbuild bundles for
  release), and `types.mjs` (regenerates the `.d.ts` from JSDoc). Hand-written
  type entries (`src/index.d.ts`, `src/global.d.ts`, `src/react.d.ts`) live
  alongside the generated ones.

To document a new component, add it to `site/registry.js` — no code changes
needed elsewhere.

Build the standalone bundles locally with `npm run bundle` (uses esbuild via
`npx`, so nothing is added to `package.json`).

## Releasing

Publishing is automated by **`release.yml`**. Create a GitHub **Release** named
`vX.Y.Z` (or run the workflow manually with a version) and it will:

1. set the package version from the tag,
2. publish the npm package to **GitHub Packages**, and
3. build `ga-ui-kit.min.js` / `ga-ui-kit.esm.js` / `ga-ui-kit.css` and **attach
   them to the Release** as downloadable assets.

Both channels use the repo's `GITHUB_TOKEN` — no extra secrets required.

## Deployment

Two GitHub Actions workflows publish the docs site to GitHub Pages. Because the
site is buildless static files, **CI installs nothing** — it just runs the copy
script and publishes `dist/`:

- **`deploy.yml`** — on every push to `main`, assembles the site and publishes
  it to the root of the `gh-pages` branch → <https://gaarutyunov.github.io/ui-kit/>.
- **`pr-preview.yml`** — on every pull request, deploys an isolated preview to
  `…/pr-preview/pr-<N>/` and posts a sticky comment with the link. The preview
  is removed automatically when the PR is closed. (Relative imports + hash
  routing mean no base-path configuration is needed.)

**One-time setup:** in the repo's **Settings → Pages**, set the source to
**Deploy from a branch** and choose the **`gh-pages`** branch (`/ root`). The
branch is created automatically by the first workflow run.

## License

MIT © German Arutyunov
