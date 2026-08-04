import { GaElement, define } from "../../core/base-element.js";

/**
 * `<ga-badge>` — a small status / category pill.
 *
 * Styled after the skill chips on garutyunov.com: a subtle outline pill
 * (`border` + `muted` text, transparent fill). Colored variants tint the text
 * and border; `solid` fills it.
 *
 * Attributes:
 *   color  "default" | "blue" | "green" | "amber" | "purple" | "red"
 *   solid  boolean — filled instead of subtle outline
 *   size   "sm" | "md"
 *
 * Slot: default (text).
 */
export class GaBadge extends GaElement {
  static observed = ["color", "solid", "size"];

  static styles = /* css */ `
    :host { display: inline-block; }
    .badge {
      --_c: var(--ga-muted, #878787);
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-family: var(--ga-font-sans, ui-sans-serif, system-ui, sans-serif);
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 500;
      line-height: 1;
      padding: 2px 10px;
      border-radius: var(--ga-radius-full, 9999px);
      border: 1px solid var(--ga-border, #1a1a1a);
      color: var(--_c);
      background: transparent;
      white-space: nowrap;
    }
    :host([size="sm"]) .badge { font-size: 11px; padding: 1px 8px; }

    /* Colored variants: tint the text + border, keep the fill subtle. */
    :host([color="blue"])   .badge { --_c: var(--ga-blue, #54a2ff); }
    :host([color="green"])  .badge { --_c: var(--ga-green, #00c758); }
    :host([color="amber"])  .badge { --_c: var(--ga-amber, #fcbb00); }
    :host([color="purple"]) .badge { --_c: var(--ga-purple, #ac4bff); }
    :host([color="red"])    .badge { --_c: var(--ga-red, #ff6568); }
    :host([color="blue"])   .badge,
    :host([color="green"])  .badge,
    :host([color="amber"])  .badge,
    :host([color="purple"]) .badge,
    :host([color="red"])    .badge {
      border-color: color-mix(in srgb, var(--_c) 40%, transparent);
    }

    :host([solid]) .badge {
      background: var(--_c);
      color: var(--ga-accent-contrast, #000);
      border-color: var(--_c);
    }
  `;

  template() {
    return /* html */ `<span class="badge" part="badge"><slot></slot></span>`;
  }
}

define("ga-badge", GaBadge);
