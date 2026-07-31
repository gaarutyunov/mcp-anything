import { GaElement, define } from "../../core/base-element.js";

/**
 * `<ga-card>` — an elevated surface / container.
 *
 * Styled after the "pet projects" cards on garutyunov.com: a translucent
 * surface with a subtle border that lightens on hover (color transitions
 * only — no lift, no shadow), and a slotted title that turns accent-blue when
 * the card is interactive.
 *
 * Attributes:
 *   interactive  boolean — hover affordance + pointer cursor
 *   href         optional — makes the whole card a link
 *   padding      "none" | "sm" | "md" | "lg"  (default md)
 *
 * Slots: `header`, default (body), `footer`.
 */
export class GaCard extends GaElement {
  static observed = ["interactive", "href", "padding"];

  static styles = /* css */ `
    :host { display: block; }
    .card {
      display: flex;
      flex-direction: column;
      gap: var(--ga-space-3, 12px);
      color: var(--ga-fg, #ededed);
      text-decoration: none;
      background: color-mix(in srgb, var(--ga-bg-elev, #1a1a1a) 30%, transparent);
      border: 1px solid var(--ga-border, #1a1a1a);
      border-radius: var(--ga-radius-lg, 8px);
      overflow: hidden;
      transition: background var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease);
    }
    :host([interactive]) .card,
    :host([href]) .card { cursor: pointer; }
    :host([interactive]) .card:hover,
    :host([href]) .card:hover {
      background: color-mix(in srgb, var(--ga-bg-elev, #1a1a1a) 60%, transparent);
      border-color: var(--ga-dim, #454545);
    }
    :host([href]) .card:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }

    /* Slotted title turns accent-blue on hover (like the project cards). */
    ::slotted(h3), ::slotted(strong) { transition: color var(--ga-transition, 0.18s ease); }
    :host([interactive]) .card:hover ::slotted(h3),
    :host([interactive]) .card:hover ::slotted(strong),
    :host([href]) .card:hover ::slotted(h3),
    :host([href]) .card:hover ::slotted(strong) { color: var(--ga-accent, #54a2ff); }

    .body { padding: var(--ga-space-5, 20px); }
    :host([padding="none"]) .body { padding: 0; }
    :host([padding="sm"]) .body { padding: var(--ga-space-3, 12px); }
    :host([padding="lg"]) .body { padding: var(--ga-space-8, 32px); }

    .header, .footer { display: none; }
    .header.show, .footer.show { display: block; }
    .header {
      padding: var(--ga-space-4, 16px) var(--ga-space-5, 20px);
      border-bottom: 1px solid var(--ga-border, #1a1a1a);
      font-weight: 600;
    }
    .footer {
      padding: var(--ga-space-4, 16px) var(--ga-space-5, 20px);
      border-top: 1px solid var(--ga-border, #1a1a1a);
      color: var(--ga-muted, #878787);
      font-size: var(--ga-fs-sm, 14px);
    }
    /* Collapse the gap when only the body is present. */
    .card:not(:has(.header.show)):not(:has(.footer.show)) { gap: 0; }
  `;

  connectedCallback() {
    super.connectedCallback();
    this._sync = () => this._toggleSlots();
    this.shadowRoot.addEventListener("slotchange", this._sync);
  }

  _toggleSlots() {
    for (const name of ["header", "footer"]) {
      const slot = this.$(`slot[name="${name}"]`);
      const wrap = this.$(`.${name}`);
      if (slot && wrap) {
        wrap.classList.toggle("show", slot.assignedNodes().length > 0);
      }
    }
  }

  template() {
    const href = this.attr("href");
    const tag = href ? "a" : "div";
    const attrs = href ? `href="${href}"` : "";
    return /* html */ `
      <${tag} class="card" part="card" ${attrs}>
        <div class="header" part="header"><slot name="header"></slot></div>
        <div class="body" part="body"><slot></slot></div>
        <div class="footer" part="footer"><slot name="footer"></slot></div>
      </${tag}>
    `;
  }
}

define("ga-card", GaCard);
