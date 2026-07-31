import { GaElement, define } from "../../core/base-element.js";

/** `<ga-kbd>` — renders a keyboard key. Slot: default (key label). */
export class GaKbd extends GaElement {
  static styles = /* css */ `
    :host { display: inline-block; }
    kbd {
      display: inline-block;
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-xs, 12px);
      line-height: 1;
      color: var(--ga-muted, #878787);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-bottom-width: 2px;
      border-radius: var(--ga-radius, 6px);
      padding: 4px 7px;
      min-width: 1em;
      text-align: center;
    }
  `;

  template() {
    return /* html */ `<kbd part="kbd"><slot></slot></kbd>`;
  }
}

define("ga-kbd", GaKbd);
