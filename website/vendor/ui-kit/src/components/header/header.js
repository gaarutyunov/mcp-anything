import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-header>` — a sticky app header.
 *
 * Modeled on the garutyunov.com header: a solid 56px bar with a brand link on
 * the left and slotted nav actions on the right. Slotted links are muted and
 * brighten to the foreground on hover; the brand does the reverse.
 *
 * Attributes:
 *   brand   brand text (or use the `brand` slot)
 *   href    brand link target
 *   static  boolean — disable sticky positioning (handy when embedding)
 *
 * Slots: `brand` (overrides the attribute), default (right-aligned actions).
 */
export class GaHeader extends GaElement {
  static observed = ["brand", "href", "static"];

  static styles = /* css */ `
    :host { display: block; }
    .hdr {
      position: sticky; top: 0; z-index: 50;
      display: flex; align-items: center; gap: 16px;
      height: 56px; padding: 0 16px;
      background: var(--ga-bg, #000);
    }
    :host([static]) .hdr { position: static; }

    .brand {
      flex: 0 1 auto; min-width: 0;
      font-size: var(--ga-fs-base, 17px); font-weight: 600;
      color: var(--ga-fg, #ededed); text-decoration: none;
      white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .brand:hover { color: var(--ga-muted, #878787); }

    .spacer { flex: 1 1 auto; }

    .actions { flex: none; display: flex; align-items: center; gap: 16px; }
    /* Slotted links live in the light DOM, so the host page's own \`a\` rules
       would otherwise win (outer tree beats ::slotted on the cascade). Use
       !important to keep the opinionated muted-nav look; consumers can still
       override with their own !important or by targeting ::part. */
    ::slotted(a) {
      color: var(--ga-muted, #878787) !important; text-decoration: none !important;
      font-size: var(--ga-fs-sm, 14px);
      transition: color var(--ga-transition, 0.18s ease);
    }
    ::slotted(a:hover) { color: var(--ga-fg, #ededed) !important; }
  `;

  template() {
    const href = this.attr("href");
    const tag = href ? "a" : "div";
    const attrs = href ? `href="${esc(href)}"` : "";
    return /* html */ `
      <header class="hdr" part="header">
        <${tag} class="brand" part="brand" ${attrs}><slot name="brand">${esc(this.attr("brand"))}</slot></${tag}>
        <div class="spacer"></div>
        <nav class="actions" part="actions"><slot></slot></nav>
      </header>
    `;
  }
}

define("ga-header", GaHeader);
