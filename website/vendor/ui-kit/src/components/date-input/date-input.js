import { GaElement, define, esc } from "../../core/base-element.js";
import { createPopup } from "../../core/popup.js";
import { isISODate, utcDate, todayISO } from "../calendar/calendar.js";
import "../calendar/calendar.js";

/**
 * `<ga-date-input>` — a text field with a calendar picker. Form-associated;
 * the submitted value is always `YYYY-MM-DD`.
 *
 *   <ga-date-input label="Session date" value="2026-03-14" max="2026-12-31"></ga-date-input>
 *
 * Typing is **lenient**: `YYYY-MM-DD` always parses, and the locale's own
 * numeric order (`14/03/2026`, `3/14/2026`) is parsed best-effort by reading
 * the order out of `Intl.DateTimeFormat` rather than guessing. Anything else,
 * or a date outside min/max, sets an error state and leaves the value alone —
 * a half-typed date must never silently become a different valid one.
 *
 * Attributes:
 *   value (YYYY-MM-DD), label, placeholder, hint, error, name, locale,
 *   min, max, first-day, disabled, required (boolean)
 *
 * Events:
 *   `change` — a date was committed. detail: { value } as YYYY-MM-DD, or "".
 *   `input`  — fires while typing. detail: { value, text } — `value` is the
 *              parsed, in-range date or "", and `text` is the raw field
 *              contents, so `value` never carries half-typed input.
 */
export class GaDateInput extends GaElement {
  static formAssociated = true;
  static observed = [
    "value", "label", "placeholder", "hint", "error", "name",
    "locale", "min", "max", "first-day", "disabled", "required",
  ];

  static styles = /* css */ `
    :host { display: block; position: relative; }
    .field { display: flex; flex-direction: column; gap: 6px; }
    label { font-size: var(--ga-fs-sm, 14px); font-weight: 500; color: var(--ga-fg, #ededed); }
    .req { color: var(--ga-red, #ff6568); margin-left: 2px; }

    .control {
      display: flex;
      align-items: center;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      transition: border-color var(--ga-transition, 0.18s ease),
        box-shadow var(--ga-transition, 0.18s ease);
    }
    .control:hover { border-color: var(--ga-muted, #878787); }
    .control:focus-within {
      border-color: var(--ga-accent, #54a2ff);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--ga-accent, #54a2ff) 25%, transparent);
    }
    :host([error]) .control, .control.invalid { border-color: var(--ga-red, #ff6568); }
    :host([disabled]) .control { opacity: 0.5; }

    input {
      flex: 1;
      min-width: 0;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: transparent;
      border: 0;
      padding: 10px 12px;
    }
    input:focus { outline: none; }
    input::placeholder { color: var(--ga-dim, #454545); }

    .open {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 34px;
      align-self: stretch;
      color: var(--ga-muted, #878787);
      background: transparent;
      border: 0;
      border-left: 1px solid var(--ga-border, #1f1f1f);
      border-radius: 0 var(--ga-radius, 6px) var(--ga-radius, 6px) 0;
      cursor: pointer;
    }
    .open:hover { color: var(--ga-fg, #ededed); }
    .open:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    .open svg { width: 15px; height: 15px; }

    .panel {
      z-index: var(--ga-z-overlay, 900);
      box-sizing: border-box;
      padding: 0;
      border: 0;
      background: transparent;
    }
    .panel[hidden] { display: none; }
    .panel::backdrop { background: transparent; }

    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-muted, #878787); }
    .error { font-size: var(--ga-fs-xs, 12px); color: var(--ga-red, #ff6568); }
  `;

  constructor() {
    super();
    this._internals = this.attachInternals?.();
    this._open = false;
    this._invalid = false;
    this._popup = null;
  }

  get _locale() {
    return this.attr("locale") || undefined;
  }

  template() {
    const label = this.attr("label");
    const error = this.attr("error");
    const hint = this.attr("hint");
    const req = this.hasFlag("required") ? `<span class="req">*</span>` : "";
    const value = this.attr("value");
    const placeholder = this.attr("placeholder") || localePattern(this._locale);

    return /* html */ `
      <div class="field">
        ${label ? `<label part="label" id="lbl">${esc(label)}${req}</label>` : ""}
        <div class="control" part="control">
          <input part="input" type="text" inputmode="numeric" autocomplete="off"
            value="${esc(value)}"
            placeholder="${esc(placeholder)}"
            ${label ? `aria-labelledby="lbl"` : ""}
            aria-invalid="${error ? "true" : "false"}"
            ${this.hasFlag("disabled") ? "disabled" : ""}
            ${this.hasFlag("required") ? "required" : ""} />
          <button class="open" part="open" type="button"
            aria-label="Choose date" aria-haspopup="dialog"
            aria-expanded="${this._open}"
            ${this.hasFlag("disabled") ? "disabled" : ""}>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
              <rect x="2" y="3" width="12" height="11" rx="2"/>
              <path d="M2 6.5h12M5.5 1.5v3M10.5 1.5v3" stroke-linecap="round"/>
            </svg>
          </button>
        </div>
        <div class="panel" part="panel" role="dialog" aria-label="Choose date" ${this._open ? "" : "hidden"}>
          <ga-calendar
            ${isISODate(value) ? `value="${esc(value)}"` : ""}
            ${this.attr("min") ? `min="${esc(this.attr("min"))}"` : ""}
            ${this.attr("max") ? `max="${esc(this.attr("max"))}"` : ""}
            ${this.attr("locale") ? `locale="${esc(this.attr("locale"))}"` : ""}
            ${this.attr("first-day") ? `first-day="${esc(this.attr("first-day"))}"` : ""}
          ></ga-calendar>
        </div>
        ${error ? `<span class="error" part="error">${esc(error)}</span>`
          : hint ? `<span class="hint" part="hint">${esc(hint)}</span>` : ""}
      </div>
    `;
  }

  render() {
    super.render();
    this._internals?.setFormValue(this.attr("value"));

    const input = this.$("input");
    const button = this.$(".open");
    const panel = this.$(".panel");
    const calendar = this.$("ga-calendar");
    if (!input || !button || !panel) return;

    this._popup?.destroy();
    this._popup = createPopup(button, panel, {
      onDismiss: () => {
        this._open = false;
        this._syncOpen();
        button.focus();
      },
    });
    if (this._open) this._popup.show();

    button.addEventListener("click", () => this._toggle());

    input.addEventListener("input", () => {
      // Do not reflect to the attribute on every keystroke: that re-renders
      // the shadow tree and drops the caret (same reason as ga-input).
      //
      // `value` stays the contract it claims to be — a YYYY-MM-DD date or "" —
      // so a listener never receives half-typed text under that name. What was
      // typed rides alongside as `text`, for a caller that wants it.
      const parsed = parseDate(input.value.trim(), this._locale);
      const usable = parsed && !this._outOfRange(parsed) ? parsed : "";
      this.emit("input", { value: usable, text: input.value });
    });
    input.addEventListener("change", () => this._commitTyped(input.value));
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        this._commitTyped(input.value);
      }
      if (e.key === "ArrowDown" && e.altKey) {
        e.preventDefault();
        this._openPanel();
      }
    });

    // render() rebuilds the shadow tree, which drops the invalid styling and
    // leaves ElementInternals holding validity for an <input> that no longer
    // exists. Reapply it against the new one.
    this._setInvalid(this._invalid);

    calendar?.addEventListener("change", (e) => {
      e.stopPropagation(); // the calendar's event is internal; ours is the API
      this._commit(e.detail.value);
      this._close();
    });
  }

  disconnectedCallback() {
    this._popup?.destroy();
  }

  _toggle() {
    if (this.hasFlag("disabled")) return;
    this._open ? this._close() : this._openPanel();
  }

  _openPanel() {
    if (this._open || this.hasFlag("disabled")) return;
    this._open = true;
    this._syncOpen();
    this._popup?.show();
    // Focus the selected day, or today, so the grid is immediately navigable.
    const calendar = this.$("ga-calendar");
    const target = isISODate(this.attr("value")) ? this.attr("value") : todayISO();
    calendar?.shadowRoot?.querySelector(`.day[data-iso="${target}"]`)?.focus();
  }

  _close({ focusButton = true } = {}) {
    if (!this._open) return;
    this._open = false;
    this._popup?.close();
    this._syncOpen();
    if (focusButton) this.$(".open")?.focus();
  }

  _syncOpen() {
    this.$(".open")?.setAttribute("aria-expanded", String(this._open));
    const panel = this.$(".panel");
    if (panel) panel.hidden = !this._open;
  }

  /** Parse what was typed; adopt it only if it is a real, in-range date. */
  _commitTyped(text) {
    const trimmed = String(text ?? "").trim();
    if (!trimmed) {
      this._setInvalid(false);
      this._commit("");
      return;
    }
    const iso = parseDate(trimmed, this._locale);
    if (!iso || this._outOfRange(iso)) {
      // Keep what the user typed on screen and flag it, rather than reverting
      // or adopting a different date.
      this._setInvalid(true);
      return;
    }
    this._setInvalid(false);
    this._commit(iso);
  }

  _outOfRange(iso) {
    const min = this.attr("min");
    const max = this.attr("max");
    return (isISODate(min) && iso < min) || (isISODate(max) && iso > max);
  }

  _setInvalid(invalid) {
    this._invalid = invalid;
    const input = this.$("input");
    this.$(".control")?.classList.toggle("invalid", invalid);
    input?.setAttribute("aria-invalid", String(invalid || Boolean(this.attr("error"))));

    // A required field with nothing in it is invalid too, and for a different
    // reason than an unparseable one — the form needs to be told which.
    const missing = this.hasFlag("required") && !this.attr("value");
    if (invalid) {
      this._internals?.setValidity?.({ badInput: true }, "Enter a valid date.", input ?? undefined);
    } else if (missing) {
      this._internals?.setValidity?.({ valueMissing: true }, "Choose a date.", input ?? undefined);
    } else {
      this._internals?.setValidity?.({}, "");
    }
  }

  _commit(iso) {
    this.setAttribute("value", iso);
    this._internals?.setFormValue(iso);
    this.emit("input", { value: iso });
    this.emit("change", { value: iso });
  }

  get value() {
    return this.attr("value");
  }
  set value(v) {
    this.setAttribute("value", v ?? "");
  }
}

/* --- parsing ------------------------------------------------------------- */

/**
 * Parse a typed date to `YYYY-MM-DD`, or null.
 *
 * ISO is accepted unconditionally. Otherwise the three numbers are mapped
 * using the locale's *own* field order, read from Intl rather than assumed —
 * `14/03/2026` is day-first in en-GB and nonsense in en-US, and only the
 * locale knows which.
 */
export function parseDate(text, locale) {
  const iso = text.match(/^(\d{4})-(\d{1,2})-(\d{1,2})$/);
  if (iso) return build(+iso[1], +iso[2], +iso[3]);

  const parts = text.split(/[^\d]+/).filter(Boolean).map(Number);
  if (parts.length !== 3 || parts.some(Number.isNaN)) return null;

  const order = localeOrder(locale);
  const fields = {};
  order.forEach((name, i) => {
    fields[name] = parts[i];
  });
  let { year, month, day } = fields;
  if (year == null || month == null || day == null) return null;
  if (year < 100) year += year < 50 ? 2000 : 1900; // two-digit years
  return build(year, month, day);
}

function build(year, month, day) {
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  const date = new Date(Date.UTC(year, month - 1, day));
  // Rejects 31 February and friends: the roll-over changes the month.
  if (date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) return null;
  return date.toISOString().slice(0, 10);
}

/** The locale's numeric field order, e.g. ["day","month","year"] for en-GB. */
function localeOrder(locale) {
  try {
    const parts = new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      timeZone: "UTC",
    }).formatToParts(utcDate("2026-03-14"));
    const order = parts
      .filter((p) => ["year", "month", "day"].includes(p.type))
      .map((p) => p.type);
    if (order.length === 3) return order;
  } catch {
    /* fall through */
  }
  return ["year", "month", "day"];
}

/** A placeholder in the locale's own order, e.g. "DD/MM/YYYY". */
function localePattern(locale) {
  const token = { year: "YYYY", month: "MM", day: "DD" };
  const order = localeOrder(locale);
  const sep = order[0] === "year" ? "-" : "/";
  return order.map((f) => token[f]).join(sep);
}

define("ga-date-input", GaDateInput);
