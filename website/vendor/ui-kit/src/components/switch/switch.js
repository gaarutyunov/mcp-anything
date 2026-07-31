import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-switch>` — an accessible on/off toggle.
 *
 * Attributes: checked, disabled (boolean), label
 * Events: `change` with { checked } detail.
 */
export class GaSwitch extends GaElement {
  static formAssociated = true;
  static observed = ["checked", "disabled", "label"];

  static styles = /* css */ `
    :host { display: inline-block; }
    .wrap {
      display: inline-flex;
      align-items: center;
      gap: var(--ga-space-3, 12px);
      cursor: pointer;
      user-select: none;
    }
    :host([disabled]) .wrap { opacity: 0.5; cursor: not-allowed; }
    button {
      position: relative;
      flex: none;
      width: 40px; height: 24px;
      padding: 0;
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-full, 9999px);
      background: var(--ga-bg-elev, #1a1a1a);
      cursor: inherit;
      transition: background var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease);
    }
    button:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .knob {
      position: absolute;
      top: 2px; left: 2px;
      width: 18px; height: 18px;
      border-radius: 50%;
      background: var(--ga-muted, #878787);
      transition: transform var(--ga-transition, 0.18s ease),
        background var(--ga-transition, 0.18s ease);
    }
    :host([checked]) button {
      background: var(--ga-accent, #54a2ff);
      border-color: var(--ga-accent, #54a2ff);
    }
    :host([checked]) .knob {
      transform: translateX(16px);
      background: var(--ga-accent-contrast, #000);
    }
    .label { font-size: var(--ga-fs-sm, 14px); color: var(--ga-fg, #ededed); }
  `;

  template() {
    const checked = this.hasFlag("checked");
    const label = this.attr("label");
    return /* html */ `
      <label class="wrap">
        <button
          part="track"
          type="button"
          role="switch"
          aria-checked="${checked}"
          ${this.hasFlag("disabled") ? "disabled" : ""}
        ><span class="knob" part="knob"></span></button>
        ${label ? `<span class="label">${esc(label)}</span>` : ""}
      </label>
    `;
  }

  render() {
    super.render();
    this.$("button")?.addEventListener("click", () => this.toggle());
  }

  toggle() {
    if (this.hasFlag("disabled")) return;
    const next = !this.hasFlag("checked");
    this.toggleAttribute("checked", next);
    this.emit("change", { checked: next });
  }

  get checked() { return this.hasFlag("checked"); }
  set checked(v) { this.toggleAttribute("checked", !!v); }
}

define("ga-switch", GaSwitch);
