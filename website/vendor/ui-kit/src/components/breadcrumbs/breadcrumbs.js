import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-breadcrumbs>` — a monospace breadcrumb trail (matches the CV / skill
 * navigation on garutyunov.com). Configure with an `items` JSON attribute:
 *
 *   <ga-breadcrumbs items='[
 *     {"label":"Home","href":"/"},
 *     {"label":"Skills","href":"/skills"},
 *     {"label":"TypeScript"}
 *   ]'></ga-breadcrumbs>
 *
 * The last item is the current page — rendered in the foreground colour with no
 * link. Earlier items are muted links, separated by "/".
 *
 * Attributes: items (JSON: { label, href? }[]).
 */
export class GaBreadcrumbs extends GaElement {
  static observed = ["items"];

  static styles = /* css */ `
    :host { display: block; }
    ol {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: var(--ga-space-2, 8px);
      margin: 0;
      padding: 0;
      list-style: none;
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-sm, 14px);
    }
    li { display: inline-flex; align-items: center; gap: var(--ga-space-2, 8px); }
    a {
      color: var(--ga-muted, #878787);
      text-decoration: none;
      transition: color var(--ga-transition, 0.18s ease);
    }
    a:hover { color: var(--ga-fg, #ededed); }
    a:focus-visible {
      outline: none;
      border-radius: var(--ga-radius, 6px);
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .current { color: var(--ga-fg, #ededed); }
    .sep { color: var(--ga-dim, #454545); user-select: none; }
  `;

  _parse() {
    try { return JSON.parse(this.attr("items", "[]")); }
    catch { return []; }
  }

  template() {
    const items = this._parse();
    const last = items.length - 1;
    const crumbs = items.map((it, i) => {
      const sep = i > 0 ? `<span class="sep" part="separator" aria-hidden="true">/</span>` : "";
      const isCurrent = i === last;
      const label = esc(it.label);
      const body = !isCurrent && it.href
        ? `<a part="link" href="${esc(it.href)}">${label}</a>`
        : `<span class="current" part="current" ${isCurrent ? `aria-current="page"` : ""}>${label}</span>`;
      return `<li>${sep}${body}</li>`;
    }).join("");
    return /* html */ `<nav aria-label="Breadcrumb" part="nav"><ol part="list">${crumbs}</ol></nav>`;
  }
}

define("ga-breadcrumbs", GaBreadcrumbs);
