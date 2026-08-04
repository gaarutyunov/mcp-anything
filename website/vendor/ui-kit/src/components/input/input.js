import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-input>` — a labelled text field. Form-associated: participates in
 * native <form> submission via ElementInternals.
 *
 * Attributes:
 *   label, placeholder, type, value, name, hint, error
 *   disabled, required (boolean)
 *
 * Events: `input`, `change` (re-dispatched with { value } detail).
 */
export class GaInput extends GaElement {
  static formAssociated = true;
  static observed = [
    "label", "placeholder", "type", "value", "name",
    "hint", "error", "disabled", "required",
  ];

  static styles = /* css */ `
    :host { display: block; }
    .field { display: flex; flex-direction: column; gap: 6px; }
    label {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      color: var(--ga-fg, #ededed);
    }
    .req { color: var(--ga-red, #ff6568); margin-left: 2px; }
    input {
      width: 100%;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: 10px 12px;
      transition: border-color var(--ga-transition, 0.18s ease),
        box-shadow var(--ga-transition, 0.18s ease);
    }
    input::placeholder { color: var(--ga-dim, #454545); }
    input:hover { border-color: var(--ga-muted, #878787); }
    input:focus {
      outline: none;
      border-color: var(--ga-accent, #54a2ff);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--ga-accent, #54a2ff) 25%, transparent);
    }
    :host([disabled]) input { opacity: 0.5; cursor: not-allowed; }
    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-muted, #878787); }
    .error { font-size: var(--ga-fs-xs, 12px); color: var(--ga-red, #ff6568); }
    :host([error]) input { border-color: var(--ga-red, #ff6568); }
  `;

  constructor() {
    super();
    this._internals = this.attachInternals?.();
  }

  template() {
    const label = this.attr("label");
    const error = this.attr("error");
    const hint = this.attr("hint");
    const req = this.hasFlag("required") ? `<span class="req">*</span>` : "";
    return /* html */ `
      <div class="field">
        ${label ? `<label part="label">${esc(label)}${req}</label>` : ""}
        <input
          part="input"
          type="${esc(this.attr("type", "text"))}"
          placeholder="${esc(this.attr("placeholder"))}"
          value="${esc(this.attr("value"))}"
          ${this.hasFlag("disabled") ? "disabled" : ""}
          ${this.hasFlag("required") ? "required" : ""}
          aria-invalid="${error ? "true" : "false"}"
        />
        ${error ? `<span class="error" part="error">${esc(error)}</span>`
          : hint ? `<span class="hint" part="hint">${esc(hint)}</span>` : ""}
      </div>
    `;
  }

  render() {
    super.render();
    const input = this.$("input");
    if (!input) return;
    input.addEventListener("input", () => {
      // Keep the underlying property + form value in sync, but do NOT reflect
      // back to the observed `value` attribute — that would re-render the
      // shadow tree on every keystroke and drop focus.
      this._value = input.value;
      this._internals?.setFormValue(input.value);
      this.emit("input", { value: input.value });
    });
    input.addEventListener("change", () => this.emit("change", { value: input.value }));
  }

  get value() { return this.$("input")?.value ?? this._value ?? this.attr("value"); }
  set value(v) { this._value = v; this.setAttribute("value", v); }
}

define("ga-input", GaInput);
