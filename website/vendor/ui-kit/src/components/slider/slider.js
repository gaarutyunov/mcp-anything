import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-slider>` — a range slider.
 *
 * Built on a native range input (stereoscope styles ranges with
 * `accent-color`); this adds a themed track/thumb and an optional label + live
 * value readout. Form-associated.
 *
 * Attributes: min, max, step, value, label, disabled
 * Events: `input`, `change` — both with { value }.
 */
export class GaSlider extends GaElement {
  static formAssociated = true;
  static observed = ["min", "max", "step", "value", "label", "disabled"];

  static styles = /* css */ `
    :host { display: block; }
    .wrap { display: flex; flex-direction: column; gap: 8px; }
    :host([disabled]) .wrap { opacity: 0.5; pointer-events: none; }
    .top { display: flex; align-items: baseline; justify-content: space-between; }
    .label { font-size: var(--ga-fs-sm, 14px); font-weight: 500; color: var(--ga-fg, #ededed); }
    .val { font-family: var(--ga-font-mono, ui-monospace, monospace); font-size: var(--ga-fs-sm, 14px); color: var(--ga-muted, #878787); }

    input[type="range"] {
      -webkit-appearance: none; appearance: none;
      width: 100%; height: 6px; margin: 6px 0;
      border-radius: var(--ga-radius-full, 9999px);
      background: var(--ga-bg-elev-hover, #1f1f1f);
      accent-color: var(--ga-accent, #54a2ff);
      cursor: pointer; outline: none;
    }
    input[type="range"]:focus-visible { box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    input[type="range"]::-webkit-slider-thumb {
      -webkit-appearance: none; appearance: none;
      width: 18px; height: 18px; border-radius: 50%;
      background: var(--ga-accent, #54a2ff);
      border: 2px solid var(--ga-bg, #000);
      box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
      cursor: pointer;
    }
    input[type="range"]::-moz-range-thumb {
      width: 16px; height: 16px; border: 2px solid var(--ga-bg, #000); border-radius: 50%;
      background: var(--ga-accent, #54a2ff); cursor: pointer;
    }
    input[type="range"]::-moz-range-track { height: 6px; border-radius: 9999px; background: var(--ga-bg-elev-hover, #1f1f1f); }
  `;

  constructor() {
    super();
    this._internals = this.attachInternals?.();
  }

  template() {
    const label = this.attr("label");
    const value = this.attr("value", "50");
    return /* html */ `
      <div class="wrap">
        ${label ? `<div class="top"><span class="label">${esc(label)}</span><span class="val">${esc(value)}</span></div>` : ""}
        <input type="range"
          min="${esc(this.attr("min", "0"))}"
          max="${esc(this.attr("max", "100"))}"
          step="${esc(this.attr("step", "1"))}"
          value="${esc(value)}"
          ${this.hasFlag("disabled") ? "disabled" : ""} />
      </div>
    `;
  }

  render() {
    super.render();
    const input = this.$("input");
    const val = this.$(".val");
    if (!input) return;
    this._internals?.setFormValue(input.value);
    input.addEventListener("input", () => {
      // Update the readout in place — do NOT re-render (would interrupt drag).
      this._value = input.value;
      if (val) val.textContent = input.value;
      this._internals?.setFormValue(input.value);
      this.emit("input", { value: input.value });
    });
    input.addEventListener("change", () => this.emit("change", { value: input.value }));
  }

  get value() { return this.$("input")?.value ?? this._value ?? this.attr("value"); }
  set value(v) { this._value = v; this.setAttribute("value", v); }
}

define("ga-slider", GaSlider);
