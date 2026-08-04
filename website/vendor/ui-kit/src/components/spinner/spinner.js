import { GaElement, define } from "../../core/base-element.js";

/**
 * `<ga-spinner>` — an indeterminate loading indicator.
 * Attributes: size ("sm" | "md" | "lg"), color (any --ga-* color name).
 */
export class GaSpinner extends GaElement {
  static observed = ["size", "color"];

  static styles = /* css */ `
    :host { display: inline-flex; }
    .spinner {
      width: 20px; height: 20px;
      border: 2px solid color-mix(in srgb, currentColor 25%, transparent);
      border-top-color: currentColor;
      border-radius: 50%;
      color: var(--ga-accent, #54a2ff);
      animation: spin 0.7s linear infinite;
    }
    :host([size="sm"]) .spinner { width: 14px; height: 14px; }
    :host([size="lg"]) .spinner { width: 32px; height: 32px; border-width: 3px; }
    :host([color="green"])  .spinner { color: var(--ga-green, #00c758); }
    :host([color="amber"])  .spinner { color: var(--ga-amber, #fcbb00); }
    :host([color="purple"]) .spinner { color: var(--ga-purple, #ac4bff); }
    :host([color="red"])    .spinner { color: var(--ga-red, #ff6568); }
    :host([color="fg"])     .spinner { color: var(--ga-fg, #ededed); }
    @keyframes spin { to { transform: rotate(360deg); } }
  `;

  template() {
    return /* html */ `<div class="spinner" part="spinner" role="status" aria-label="Loading"></div>`;
  }
}

define("ga-spinner", GaSpinner);
