import { GaElement, define, esc } from "../../core/base-element.js";
import "../icon/icon.js";

/**
 * `<ga-bottom-nav>` — a mobile-app bottom navigation bar.
 *
 * A fixed bar of icon + label destinations, one active at a time. Configure
 * with an `items` JSON attribute; the icon is any glyph/emoji.
 *
 *   <ga-bottom-nav active="explore"
 *     items='[{"id":"explore","label":"Explore","icon":"🧭"}, ...]'>
 *   </ga-bottom-nav>
 *
 * Attributes:
 *   items   JSON: { id, label, icon }[]
 *   active  active item id (reflected)
 *   static  boolean — render inline instead of fixed (for embedding/demos)
 *
 * Events: `change` ({ id }).
 */
export class GaBottomNav extends GaElement {
  static observed = ["items", "active"];

  static styles = /* css */ `
    :host { display: block; }
    .nav {
      position: fixed; left: 0; right: 0; bottom: 0; z-index: 40;
      display: flex;
      background: var(--ga-bg, #000);
      border-top: 1px solid var(--ga-border, #1a1a1a);
      padding-bottom: env(safe-area-inset-bottom);
    }
    :host([static]) .nav {
      position: static;
      border: 1px solid var(--ga-border, #1a1a1a);
      border-radius: var(--ga-radius, 6px);
      padding-bottom: 0;
    }
    .item {
      flex: 1; min-width: 0;
      display: flex; flex-direction: column; align-items: center; gap: 3px;
      padding: 9px 4px 8px;
      background: none; border: 0; cursor: pointer;
      color: var(--ga-muted, #878787); font-family: inherit;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .item:hover { color: var(--ga-fg, #ededed); }
    .item[aria-current="page"] { color: var(--ga-accent, #54a2ff); }
    .item:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); border-radius: var(--ga-radius, 6px); }
    .icon { font-size: 20px; line-height: 1; }
    .label { font-size: 11px; line-height: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }
  `;

  _parse() {
    try { return JSON.parse(this.attr("items", "[]")); } catch { return []; }
  }

  template() {
    const items = this._parse();
    const active = this.attr("active") || items[0]?.id;
    const buttons = items.map((it) => {
      const icon = it.icon || "";
      // A bare name (e.g. "compass") renders a ga-icon; any other glyph is text.
      const iconHtml = /^[a-z][a-z0-9-]*$/.test(icon)
        ? `<ga-icon class="icon" name="${esc(icon)}" size="22"></ga-icon>`
        : `<span class="icon" aria-hidden="true">${esc(icon || "•")}</span>`;
      return `
      <button class="item" part="item" data-id="${esc(it.id)}"
        ${it.id === active ? 'aria-current="page"' : ""}>
        ${iconHtml}
        <span class="label">${esc(it.label)}</span>
      </button>`;
    }).join("");
    return /* html */ `<nav class="nav" part="nav" role="navigation">${buttons}</nav>`;
  }

  render() {
    super.render();
    this.shadowRoot.querySelectorAll(".item").forEach((b) =>
      b.addEventListener("click", () => this._select(b.dataset.id)));
  }

  _select(id) {
    if (id === this.attr("active")) return;
    this.setAttribute("active", id);
    this.emit("change", { id });
  }
}

define("ga-bottom-nav", GaBottomNav);
