import { GaElement, define, esc } from "../../core/base-element.js";
import { createPopup } from "../../core/popup.js";

/**
 * `<ga-select>` — a listbox select. Form-associated: participates in native
 * <form> submission via ElementInternals.
 *
 * Options come from a JSON `options` attribute or from slotted `<option>`
 * children (the light-DOM form degrades without JavaScript and is what a
 * server-rendered page should use):
 *
 *   <ga-select label="Unit" value="kg"
 *     options='[{"value":"kg","label":"Kilograms"},{"value":"lb","label":"Pounds"}]'>
 *   </ga-select>
 *
 *   <ga-select label="Unit">
 *     <option value="kg" selected>Kilograms</option>
 *     <option value="lb">Pounds</option>
 *   </ga-select>
 *
 * `filterable` adds a search field in the popup. Filtering is local by default;
 * an app that owns the data listens for the debounced `filter` event and
 * replaces `options` itself, which is how an async source is driven.
 *
 * Attributes:
 *   options (JSON: { value, label, disabled? }[]), value, multiple, filterable,
 *   placeholder, label, hint, error, name, disabled, required (boolean)
 *
 * Slots: (default) — `<option>` elements, an alternative to `options`.
 *
 * Events:
 *   `change` — selection committed. detail: { value } (array when `multiple`).
 *   `input`  — same payload, fired alongside `change`.
 *   `filter` — debounced typing in the filter field. detail: { text }.
 */
export class GaSelect extends GaElement {
  static formAssociated = true;
  static observed = [
    "options", "value", "multiple", "filterable", "placeholder",
    "label", "hint", "error", "name", "disabled", "required",
  ];

  static styles = /* css */ `
    :host { display: block; position: relative; }
    .field { display: flex; flex-direction: column; gap: 6px; }
    label {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      color: var(--ga-fg, #ededed);
    }
    .req { color: var(--ga-red, #ff6568); margin-left: 2px; }

    .trigger {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--ga-space-2, 8px);
      width: 100%;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      text-align: left;
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: 10px 12px;
      cursor: pointer;
      transition: border-color var(--ga-transition, 0.18s ease),
        box-shadow var(--ga-transition, 0.18s ease);
    }
    .trigger:hover { border-color: var(--ga-muted, #878787); }
    .trigger:focus-visible {
      outline: none;
      border-color: var(--ga-accent, #54a2ff);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--ga-accent, #54a2ff) 25%, transparent);
    }
    :host([disabled]) .trigger { opacity: 0.5; cursor: not-allowed; }
    :host([error]) .trigger { border-color: var(--ga-red, #ff6568); }
    .placeholder { color: var(--ga-dim, #454545); }
    .caret { flex: none; width: 14px; height: 14px; color: var(--ga-muted, #878787); }
    .trigger[aria-expanded="true"] .caret { transform: rotate(180deg); }

    .panel {
      z-index: var(--ga-z-overlay, 900);
      box-sizing: border-box;
      max-height: 280px;
      overflow: auto;
      padding: 4px;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      box-shadow: var(--ga-shadow, 0 8px 24px rgba(0, 0, 0, 0.5));
    }
    .panel[hidden] { display: none; }
    .panel:popover-open { display: block; }
    /* The top layer paints its own backdrop; we want none. */
    .panel::backdrop { background: transparent; }

    .filter {
      width: 100%;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg, #000);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-sm, 4px);
      padding: 7px 9px;
      margin-bottom: 4px;
    }
    .filter:focus { outline: none; border-color: var(--ga-accent, #54a2ff); }

    .opt {
      display: flex;
      align-items: center;
      gap: var(--ga-space-2, 8px);
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      border-radius: var(--ga-radius-sm, 4px);
      padding: 8px 10px;
      cursor: pointer;
    }
    .opt[aria-selected="true"] { color: var(--ga-accent, #54a2ff); }
    .opt.active { background: var(--ga-bg-elev-hover, #232323); }
    .opt[aria-disabled="true"] { opacity: 0.4; cursor: not-allowed; }
    .tick { flex: none; width: 14px; height: 14px; opacity: 0; }
    .opt[aria-selected="true"] .tick { opacity: 1; }
    .empty {
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-muted, #878787);
      padding: 10px;
    }

    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-muted, #878787); }
    .error { font-size: var(--ga-fs-xs, 12px); color: var(--ga-red, #ff6568); }
  `;

  constructor() {
    super();
    this._internals = this.attachInternals?.();
    this._open = false;
    this._active = -1;
    this._filterText = "";
    this._typeahead = "";
    this._typeaheadAt = 0;
    this._filterTimer = 0;
    this._popup = null;
    // null means "read the attribute"; an array means the attribute is a mirror.
    this._values = null;
    this._reflecting = false;
  }

  attributeChangedCallback(name, oldValue, newValue) {
    // An external `value=` assignment replaces the selection, so the array
    // stops being authoritative — but our own mirror must not do that, or a
    // value containing a comma would be split straight back apart.
    if (name === "value" && !this._reflecting) this._values = null;
    super.attributeChangedCallback(name, oldValue, newValue);
  }

  /* --- options ---------------------------------------------------------- */

  /** Options from the JSON attribute, falling back to slotted <option>s. */
  _allOptions() {
    const raw = this.getAttribute("options");
    if (raw) {
      try {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) return parsed.map(normalise);
      } catch {
        /* fall through to the light DOM */
      }
    }
    return [...this.querySelectorAll("option")].map((el) => ({
      value: el.value ?? el.textContent.trim(),
      label: el.textContent.trim(),
      disabled: el.disabled,
    }));
  }

  /** Options after the local filter. */
  _visibleOptions() {
    const all = this._allOptions();
    const text = this._filterText.trim().toLowerCase();
    if (!text) return all;
    return all.filter((o) => o.label.toLowerCase().includes(text));
  }

  get multiple() {
    return this.hasFlag("multiple");
  }

  /**
   * Selected values, always as an array — the single-value case is length 1.
   *
   * The internal array is authoritative and the `value` attribute mirrors it,
   * because the mirror is lossy: multi-select joins on a comma, so an option
   * value that *contains* a comma would split into two on the way back. Going
   * through `_values` means selection and the `.value` property round-trip
   * such a value correctly; only assigning the comma-joined attribute from
   * outside cannot (documented on the attribute).
   */
  _selected() {
    if (this._values) return this._values;
    const raw = this.attr("value");
    if (!raw) return [];
    return this.multiple ? raw.split(",").filter(Boolean) : [raw];
  }

  /** Set the selection and mirror it to the attribute. */
  _setSelected(values) {
    this._values = values;
    this._reflecting = true;
    this.setAttribute("value", values.join(","));
    this._reflecting = false;
  }

  /* --- template --------------------------------------------------------- */

  _summary() {
    const selected = this._selected();
    const all = this._allOptions();
    if (!selected.length) {
      const ph = this.attr("placeholder", "Select…");
      return `<span class="placeholder">${esc(ph)}</span>`;
    }
    if (this.multiple && selected.length > 1) {
      return `<span>${selected.length} selected</span>`;
    }
    const match = all.find((o) => o.value === selected[0]);
    return `<span>${esc(match ? match.label : selected[0])}</span>`;
  }

  template() {
    const label = this.attr("label");
    const error = this.attr("error");
    const hint = this.attr("hint");
    const req = this.hasFlag("required") ? `<span class="req">*</span>` : "";
    const disabled = this.hasFlag("disabled");

    return /* html */ `
      <div class="field">
        ${label ? `<label part="label" id="lbl">${esc(label)}${req}</label>` : ""}
        <button class="trigger" part="trigger" type="button"
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded="${this._open}"
          aria-controls="listbox"
          ${label ? `aria-labelledby="lbl"` : ""}
          aria-invalid="${error ? "true" : "false"}"
          ${disabled ? "disabled" : ""}>
          ${this._summary()}
          <svg class="caret" viewBox="0 0 16 16" aria-hidden="true" fill="none"
            stroke="currentColor" stroke-width="1.5">
            <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <div class="panel" part="panel" id="panel" ${this._open ? "" : "hidden"}>
          ${this.hasFlag("filterable")
            ? `<input class="filter" part="filter" type="text" autocomplete="off"
                 placeholder="Filter…" aria-label="Filter options"
                 value="${esc(this._filterText)}" />`
            : ""}
          <div id="listbox" role="listbox"
            aria-multiselectable="${this.multiple}"
            ${label ? `aria-labelledby="lbl"` : ""}>${this._rows()}</div>
        </div>
        ${error ? `<span class="error" part="error">${esc(error)}</span>`
          : hint ? `<span class="hint" part="hint">${esc(hint)}</span>` : ""}
      </div>
      <slot hidden></slot>
    `;
  }

  /** The option rows alone, so filtering can repaint them without a re-render. */
  _rows() {
    const options = this._visibleOptions();
    if (!options.length) {
      return `<div class="empty" role="option" aria-disabled="true">No matches</div>`;
    }
    const selected = this._selected();
    return options
      .map((o, i) => {
        const isSelected = selected.includes(o.value);
        return `<div class="opt${i === this._active ? " active" : ""}"
          part="option" role="option" id="opt-${i}" data-value="${esc(o.value)}"
          aria-selected="${isSelected}"
          ${o.disabled ? `aria-disabled="true"` : ""}>
          <svg class="tick" viewBox="0 0 16 16" aria-hidden="true" fill="none"
            stroke="currentColor" stroke-width="2">
            <path d="M3 8.5l3.5 3.5L13 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span>${esc(o.label)}</span>
        </div>`;
      })
      .join("");
  }

  /* --- lifecycle -------------------------------------------------------- */

  render() {
    super.render();
    this._internals?.setFormValue(this._formValue());

    const trigger = this.$(".trigger");
    const panel = this.$(".panel");
    if (!trigger || !panel) return;

    this._popup?.destroy();
    this._popup = createPopup(trigger, panel, {
      onDismiss: () => {
        this._open = false;
        this._active = -1;
        this._syncOpenState();
        trigger.focus();
      },
    });
    if (this._open) this._popup.show();

    trigger.addEventListener("click", () => this._toggle());
    trigger.addEventListener("keydown", (e) => this._onTriggerKey(e));

    const filter = this.$(".filter");
    filter?.addEventListener("input", () => this._onFilterInput(filter.value));
    filter?.addEventListener("keydown", (e) => this._onTriggerKey(e));

    this._bindRows();

    // Slotted <option>s can arrive after the first render — but re-rendering
    // replaces the <slot> element, which fires slotchange again, which renders
    // again: an infinite loop that locks the tab. Repaint the rows and the
    // trigger summary instead; neither replaces the slot.
    this.$("slot")?.addEventListener("slotchange", () => this._onSlotChange());
  }

  /** Slotted options changed: refresh what is derived from them, not the tree. */
  _onSlotChange() {
    const summary = this.$(".trigger");
    if (summary) {
      const caret = summary.querySelector(".caret");
      summary.innerHTML = this._summary() + (caret ? caret.outerHTML : "");
    }
    this._repaintRows();
  }

  disconnectedCallback() {
    this._popup?.destroy();
    clearTimeout(this._filterTimer);
  }

  _bindRows() {
    this.shadowRoot.querySelectorAll(".opt").forEach((row) => {
      row.addEventListener("click", () => {
        if (row.getAttribute("aria-disabled") === "true") return;
        this._commit(row.dataset.value);
      });
      // pointerdown must not steal focus from the trigger/filter.
      row.addEventListener("pointerdown", (e) => e.preventDefault());
    });
  }

  /** Repaint only the rows — keeps the filter field's focus and caret. */
  _repaintRows() {
    const listbox = this.$("#listbox");
    if (!listbox) return;
    listbox.innerHTML = this._rows();
    this._bindRows();
    this._syncActive();
    this._popup?.reposition();
  }

  /* --- open / close ----------------------------------------------------- */

  _toggle() {
    if (this.hasFlag("disabled")) return;
    this._open ? this._close() : this._openPanel();
  }

  _openPanel() {
    if (this._open) return;
    this._open = true;
    // Land the active option on the current selection, so Up/Down starts there.
    const selected = this._selected();
    const options = this._visibleOptions();
    this._active = Math.max(0, options.findIndex((o) => selected.includes(o.value)));
    this._syncOpenState();
    this._popup?.show();
    this._repaintRows();
    const filter = this.$(".filter");
    if (filter) filter.focus();
  }

  _close({ focusTrigger = true } = {}) {
    if (!this._open) return;
    this._open = false;
    this._active = -1;
    this._popup?.close();
    this._syncOpenState();
    if (focusTrigger) this.$(".trigger")?.focus();
  }

  /** Reflect open state without re-rendering (which would drop focus). */
  _syncOpenState() {
    const trigger = this.$(".trigger");
    const panel = this.$(".panel");
    trigger?.setAttribute("aria-expanded", String(this._open));
    if (panel) panel.hidden = !this._open;
    this._syncActive();
  }

  _syncActive() {
    const trigger = this.$(".trigger");
    const rows = [...this.shadowRoot.querySelectorAll(".opt")];
    rows.forEach((row, i) => row.classList.toggle("active", i === this._active));
    const active = rows[this._active];
    if (this._open && active) {
      trigger?.setAttribute("aria-activedescendant", active.id);
      active.scrollIntoView({ block: "nearest" });
    } else {
      trigger?.removeAttribute("aria-activedescendant");
    }
  }

  /* --- keyboard --------------------------------------------------------- */

  _onTriggerKey(e) {
    const options = this._visibleOptions();

    if (!this._open) {
      if (["Enter", " ", "ArrowDown", "ArrowUp"].includes(e.key) ||
          (e.altKey && e.key === "ArrowDown")) {
        e.preventDefault();
        this._openPanel();
      }
      return;
    }

    switch (e.key) {
      case "Escape":
        e.preventDefault();
        this._close();
        return;
      case "Tab": {
        // Tab commits the active option and lets focus move on.
        const option = enabledAt(options, this._active);
        if (option) this._commit(option.value, { keepOpen: false });
        else this._close({ focusTrigger: false });
        return;
      }
      case "Enter": {
        e.preventDefault();
        const option = enabledAt(options, this._active);
        if (option) this._commit(option.value);
        return;
      }
      case " ": {
        // Space types into the filter field rather than selecting.
        if (this.hasFlag("filterable") && e.target === this.$(".filter")) return;
        e.preventDefault();
        const option = enabledAt(options, this._active);
        if (option) this._commit(option.value);
        return;
      }
      case "ArrowDown":
        e.preventDefault();
        this._move(1, options);
        return;
      case "ArrowUp":
        e.preventDefault();
        this._move(-1, options);
        return;
      case "Home":
        e.preventDefault();
        this._moveTo(0, options, 1);
        return;
      case "End":
        e.preventDefault();
        this._moveTo(options.length - 1, options, -1);
        return;
      case "PageDown":
        e.preventDefault();
        this._moveTo(Math.min(options.length - 1, this._active + 10), options, -1);
        return;
      case "PageUp":
        e.preventDefault();
        this._moveTo(Math.max(0, this._active - 10), options, 1);
        return;
      default:
        break;
    }

    // Type-ahead, but not while a filter field is taking the keystrokes.
    if (!this.hasFlag("filterable") && e.key.length === 1 && !e.metaKey && !e.ctrlKey) {
      this._onTypeahead(e.key, options);
    }
  }

  /** Move `dir` steps from the active row, skipping disabled options. */
  _move(dir, options) {
    if (!options.length) return;
    let i = this._active;
    for (let step = 0; step < options.length; step++) {
      i = (i + dir + options.length) % options.length;
      if (!options[i].disabled) {
        this._active = i;
        this._syncActive();
        return;
      }
    }
  }

  /** Jump to `index`, then skip forward in `dir` if it landed on a disabled row. */
  _moveTo(index, options, dir) {
    if (!options.length) return;
    let i = Math.max(0, Math.min(options.length - 1, index));
    for (let step = 0; step < options.length; step++) {
      if (!options[i].disabled) {
        this._active = i;
        this._syncActive();
        return;
      }
      i = (i + dir + options.length) % options.length;
    }
  }

  _onTypeahead(char, options) {
    const now = Date.now();
    this._typeahead = now - this._typeaheadAt > 800 ? char : this._typeahead + char;
    this._typeaheadAt = now;
    const needle = this._typeahead.toLowerCase();
    const found = options.findIndex(
      (o) => !o.disabled && o.label.toLowerCase().startsWith(needle)
    );
    if (found >= 0) {
      this._active = found;
      this._syncActive();
    }
  }

  /* --- filtering -------------------------------------------------------- */

  _onFilterInput(text) {
    this._filterText = text;
    this._active = 0;
    this._repaintRows();
    // Debounced, so an app fetching options is not hit on every keystroke.
    clearTimeout(this._filterTimer);
    this._filterTimer = setTimeout(() => this.emit("filter", { text }), 200);
  }

  /* --- selection -------------------------------------------------------- */

  _commit(value, { keepOpen = this.multiple } = {}) {
    if (value == null) return;
    let next;
    if (this.multiple) {
      const selected = this._selected();
      const values = selected.includes(value)
        ? selected.filter((v) => v !== value)
        : [...selected, value];
      this._setSelected(values);
      next = values;
    } else {
      next = value;
      this._setSelected([value]);
    }

    this._internals?.setFormValue(this._formValue());
    this.emit("input", { value: next });
    this.emit("change", { value: next });

    if (keepOpen) {
      // Multiple stays open; the attribute change already re-rendered, so
      // re-open the panel and put the active row back where it was.
      this._openPanel();
    } else {
      this._close();
    }
  }

  _formValue() {
    const selected = this._selected();
    if (!this.multiple) return selected[0] ?? "";
    const data = new FormData();
    const name = this.attr("name");
    if (name) selected.forEach((v) => data.append(name, v));
    return data;
  }

  /* --- properties ------------------------------------------------------- */

  get value() {
    const selected = this._selected();
    return this.multiple ? selected : selected[0] ?? "";
  }

  set value(v) {
    if (Array.isArray(v)) {
      this._setSelected(v.map(String));
      return;
    }
    const next = String(v ?? "");
    this._setSelected(next ? [next] : []);
  }

  get options() {
    return this._allOptions();
  }

  set options(list) {
    this.setAttribute("options", JSON.stringify(list ?? []));
  }
}

/** The option at `index`, unless it is disabled — the keyboard must not commit one. */
function enabledAt(options, index) {
  const option = options[index];
  return option && !option.disabled ? option : null;
}

/** Accept both { value, label } and a bare string. */
function normalise(o) {
  if (typeof o === "string") return { value: o, label: o, disabled: false };
  return {
    value: String(o.value ?? o.id ?? ""),
    label: String(o.label ?? o.value ?? o.id ?? ""),
    disabled: Boolean(o.disabled),
  };
}

define("ga-select", GaSelect);
