import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-panel>` — a slide-in drawer.
 *
 * Ported from stereoscope's `.curtain`: a fixed panel that slides in from the
 * edge with a backdrop scrim. Close via the × button, the scrim, or Escape.
 *
 * Attributes:
 *   open   boolean — reflected; toggles visibility
 *   side   "right" (default) | "left"
 *   title  optional header text (overridden by the `header` slot)
 *
 * Slots: `header`, default (body), `footer`.
 * Methods: show() / close() / toggle().
 * Events: `open`, `close`.
 */
export class GaPanel extends GaElement {
  static observed = ["open", "side", "title"];

  static styles = /* css */ `
    :host { display: contents; }
    .scrim {
      position: fixed; inset: 0; z-index: 49;
      background: rgba(0, 0, 0, 0.5);
      opacity: 0; visibility: hidden;
      transition: opacity 0.32s ease, visibility 0.32s;
    }
    :host([open]) .scrim { opacity: 1; visibility: visible; }

    .panel {
      position: fixed; top: 0; right: 0; z-index: 50;
      width: min(420px, 100%); height: 100%;
      display: flex; flex-direction: column;
      background: var(--ga-bg, #000);
      border-left: 1px solid var(--ga-border, #1a1a1a);
      box-shadow: -16px 0 40px rgba(0, 0, 0, 0.4);
      transform: translateX(100%);
      transition: transform 0.32s cubic-bezier(0.4, 0, 0.2, 1);
      visibility: hidden;
    }
    :host([side="left"]) .panel {
      right: auto; left: 0;
      border-left: 0; border-right: 1px solid var(--ga-border, #1a1a1a);
      box-shadow: 16px 0 40px rgba(0, 0, 0, 0.4);
      transform: translateX(-100%);
    }
    :host([open]) .panel { transform: translateX(0); visibility: visible; }

    .head {
      display: flex; align-items: center; justify-content: space-between; gap: 12px;
      padding: 18px 20px; border-bottom: 1px solid var(--ga-border, #1a1a1a);
      font-weight: 600; color: var(--ga-fg, #ededed);
    }
    .body { flex: 1; overflow: auto; padding: 20px; color: var(--ga-muted, #878787); line-height: 1.55; }
    .foot { padding: 16px 20px; border-top: 1px solid var(--ga-border, #1a1a1a); }
    .foot { display: none; }
    .foot.show { display: block; }
    .close {
      flex: none; background: none; border: 0; cursor: pointer;
      color: var(--ga-muted, #878787); font-size: 22px; line-height: 1; padding: 2px 6px;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .close:hover { color: var(--ga-fg, #ededed); }
  `;

  template() {
    const title = this.attr("title");
    return /* html */ `
      <div class="scrim" part="scrim"></div>
      <div class="panel" part="panel" role="dialog" aria-modal="true">
        <div class="head" part="header">
          <span class="title"><slot name="header">${esc(title)}</slot></span>
          <button class="close" aria-label="Close">&times;</button>
        </div>
        <div class="body" part="body"><slot></slot></div>
        <div class="foot" part="footer"><slot name="footer"></slot></div>
      </div>
    `;
  }

  connectedCallback() {
    super.connectedCallback();
    this._key = (e) => { if (e.key === "Escape" && this.open) this.close(); };
    document.addEventListener("keydown", this._key);
    this.shadowRoot.addEventListener("slotchange", () => this._syncFooter());
  }

  disconnectedCallback() {
    if (this._key) document.removeEventListener("keydown", this._key);
  }

  render() {
    super.render();
    this.$(".close")?.addEventListener("click", () => this.close());
    this.$(".scrim")?.addEventListener("click", () => this.close());
    this._syncFooter();
  }

  _syncFooter() {
    const slot = this.$('slot[name="footer"]');
    const foot = this.$(".foot");
    if (slot && foot) foot.classList.toggle("show", slot.assignedNodes().length > 0);
  }

  get open() { return this.hasFlag("open"); }
  set open(v) { this.toggleAttribute("open", !!v); }
  show() { if (!this.open) { this.setAttribute("open", ""); this.emit("open"); } }
  close() { if (this.open) { this.removeAttribute("open"); this.emit("close"); } }
  toggle() { this.open ? this.close() : this.show(); }
}

define("ga-panel", GaPanel);
