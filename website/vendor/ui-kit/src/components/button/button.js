import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-button>` — the kit's primary action element.
 *
 * Attributes:
 *   variant     "primary" | "secondary" | "ghost" | "danger"  (default secondary)
 *   size        "sm" | "md" | "lg"                              (default md)
 *   href        render as a link instead of a button
 *   download    (link) filename hint / force-download           — forwarded to <a>
 *   target      (link) "_blank" | "_self" | …                   — forwarded to <a>
 *   rel         (link) e.g. "noopener noreferrer"               — forwarded to <a>
 *   type        (button) "button" | "submit" | "reset"          — forwarded to <button>
 *   name        (button) form control name                      — forwarded to <button>
 *   aria-label  accessible label                                — forwarded to <a>/<button>
 *   disabled    boolean
 *   loading     boolean — shows a spinner and blocks clicks
 *   block       boolean — full width
 *
 * Slots: default (label), `start` / `end` (icons).
 */
export class GaButton extends GaElement {
  static observed = [
    "variant", "size", "href", "download", "target", "rel",
    "type", "name", "aria-label", "disabled", "loading", "block",
  ];

  static styles = /* css */ `
    :host { display: inline-block; }
    :host([block]) { display: block; }

    .btn {
      --_bg: var(--ga-bg-elev, #1a1a1a);
      --_fg: var(--ga-fg, #ededed);
      --_bd: var(--ga-border-strong, #2a2a2a);
      --_bg-hover: var(--ga-bg-elev-hover, #1f1f1f);

      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: var(--ga-space-2, 8px);
      width: 100%;
      font-family: inherit;
      font-weight: 500;
      line-height: 1;
      white-space: nowrap;
      text-decoration: none;
      cursor: pointer;
      border: 1px solid var(--_bd);
      border-radius: var(--ga-radius, 6px);
      background: var(--_bg);
      color: var(--_fg);
      transition: background var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease),
        filter var(--ga-transition, 0.18s ease),
        transform var(--ga-transition, 0.18s ease);
    }
    .btn:hover { background: var(--_bg-hover); }
    .btn:active { transform: translateY(1px); }
    .btn:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }

    /* sizes */
    :host([size="sm"]) .btn { font-size: var(--ga-fs-sm, 14px); padding: 6px 12px; height: 32px; }
    .btn { font-size: var(--ga-fs-sm, 14px); padding: 8px 16px; height: 40px; }
    :host([size="lg"]) .btn { font-size: var(--ga-fs-base, 17px); padding: 12px 22px; height: 48px; }

    /* variants */
    :host([variant="primary"]) .btn {
      --_bg: var(--ga-accent, #54a2ff);
      --_fg: var(--ga-accent-contrast, #000);
      --_bd: var(--ga-accent, #54a2ff);
    }
    :host([variant="primary"]) .btn:hover { background: var(--ga-accent, #54a2ff); filter: brightness(1.1); }

    :host([variant="ghost"]) .btn {
      --_bg: transparent;
      --_bd: transparent;
    }
    :host([variant="ghost"]) .btn:hover { background: var(--ga-bg-elev, #1a1a1a); }

    :host([variant="danger"]) .btn {
      --_bg: transparent;
      --_fg: var(--ga-red, #ff6568);
      --_bd: color-mix(in srgb, var(--ga-red, #ff6568) 40%, transparent);
    }
    :host([variant="danger"]) .btn:hover {
      background: color-mix(in srgb, var(--ga-red, #ff6568) 12%, transparent);
    }

    :host([disabled]) .btn,
    :host([loading]) .btn {
      opacity: 0.5;
      pointer-events: none;
      cursor: not-allowed;
    }

    .spinner {
      width: 1em; height: 1em;
      border: 2px solid currentColor;
      border-right-color: transparent;
      border-radius: 50%;
      animation: spin 0.6s linear infinite;
    }
    @keyframes spin { to { transform: rotate(360deg); } }

    ::slotted([slot="start"]), ::slotted([slot="end"]) { display: inline-flex; }
  `;

  connectedCallback() {
    super.connectedCallback();
    this.addEventListener("click", this._guard, true);
  }

  disconnectedCallback() {
    this.removeEventListener("click", this._guard, true);
  }

  _guard = (e) => {
    if (this.hasFlag("disabled") || this.hasFlag("loading")) {
      e.stopImmediatePropagation();
      e.preventDefault();
    }
  };

  /** Forward `name` from the host as attribute `out` on the inner element. */
  _pass(name, out = name) {
    return this.hasAttribute(name)
      ? ` ${out}="${esc(this.getAttribute(name))}"`
      : "";
  }

  template() {
    const href = this.attr("href");
    const tag = href ? "a" : "button";
    // aria-label is forwarded to whichever inner element we render.
    const aria = this._pass("aria-label");
    const attrs = href
      ? `href="${esc(href)}"` +
        this._pass("download") +
        this._pass("target") +
        this._pass("rel") +
        aria
      : `type="${esc(this.attr("type", "button"))}"` +
        this._pass("name") +
        aria +
        (this.hasFlag("disabled") ? " disabled" : "");
    const spinner = this.hasFlag("loading") ? `<span class="spinner" aria-hidden="true"></span>` : "";
    return /* html */ `
      <${tag} class="btn" part="button" ${attrs}>
        <slot name="start"></slot>
        ${spinner}
        <slot></slot>
        <slot name="end"></slot>
      </${tag}>
    `;
  }
}

define("ga-button", GaButton);
