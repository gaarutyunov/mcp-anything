import { GaElement, define } from "../../core/base-element.js";
import { ICONS } from "../../core/icons.js";

/**
 * `<ga-icon>` — a simple line icon that strokes with `currentColor`, so it
 * inherits the surrounding text color.
 *
 * Attributes:
 *   name  icon id (see the Icons page / ICON_NAMES)
 *   size  pixel size (default 20)
 */
export class GaIcon extends GaElement {
  static observed = ["name", "size"];

  static styles = /* css */ `
    :host { display: inline-flex; line-height: 0; }
    svg {
      display: block;
      stroke: currentColor; fill: none;
      stroke-width: 2; stroke-linecap: round; stroke-linejoin: round;
    }
  `;

  template() {
    const inner = ICONS[this.attr("name")] || "";
    const s = Number(this.attr("size")) || 20;
    return /* html */ `<svg viewBox="0 0 24 24" width="${s}" height="${s}" part="svg" aria-hidden="true">${inner}</svg>`;
  }
}

define("ga-icon", GaIcon);
