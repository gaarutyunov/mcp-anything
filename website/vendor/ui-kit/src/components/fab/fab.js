import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-fab>` — a floating action button.
 *
 * Ported from stereoscope's `.curtain-toggle`: a fixed 56px accent circle,
 * bottom-right by default, respecting safe-area insets. Clicks bubble normally.
 *
 * Attributes:
 *   color     "" | "green" | "amber" | "purple" | "red"
 *   position  "bottom-right" (default) | "bottom-left" | "static"
 *   label     accessible label (default "Action")
 *
 * Slot: default (icon/glyph — defaults to +).
 */
export class GaFab extends GaElement {
  static observed = ["color", "position", "label"];

  static styles = /* css */ `
    :host { display: contents; }
    .fab {
      --_c: var(--ga-accent, #54a2ff);
      position: fixed;
      right: max(20px, env(safe-area-inset-right));
      bottom: max(20px, env(safe-area-inset-bottom));
      z-index: 40;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 56px;
      height: 56px;
      padding: 0;
      font-size: 22px;
      line-height: 1;
      background: var(--_c);
      color: var(--ga-accent-contrast, #000);
      border: 0;
      border-radius: 50%;
      box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
      cursor: pointer;
      transition: filter 0.15s ease, transform 0.15s ease;
    }
    .fab:hover { filter: brightness(1.08); }
    .fab:active { transform: translateY(1px); }
    .fab:focus-visible { outline: 2px solid var(--ga-fg, #ededed); outline-offset: 3px; }

    :host([position="bottom-left"]) .fab { left: max(20px, env(safe-area-inset-left)); right: auto; }
    :host([position="static"]) .fab { position: static; }

    :host([color="green"])  .fab { --_c: var(--ga-green, #00c758); }
    :host([color="amber"])  .fab { --_c: var(--ga-amber, #fcbb00); }
    :host([color="purple"]) .fab { --_c: var(--ga-purple, #ac4bff); }
    :host([color="red"])    .fab { --_c: var(--ga-red, #ff6568); }
  `;

  template() {
    return /* html */ `
      <button class="fab" part="fab" aria-label="${esc(this.attr("label", "Action"))}">
        <slot>+</slot>
      </button>
    `;
  }
}

define("ga-fab", GaFab);
