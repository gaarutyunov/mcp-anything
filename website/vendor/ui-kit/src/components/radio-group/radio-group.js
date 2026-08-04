import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-radio-group>` — a single-select control rendered as a row of
 * segmented pills (not circular radio dots). It is a radio group in behaviour:
 * exactly one item is selected at a time.
 *
 * Configure with an `items` JSON attribute and read/set the selected id via the
 * reflected `value` attribute (and the `.value` property — it is
 * form-associated, so it participates in native <form> submission):
 *
 *   <ga-radio-group value="ai"
 *     items='[{"id":"human","label":"Human"},{"id":"ai","label":"AI"}]'>
 *   </ga-radio-group>
 *
 * An item WITH `href` renders as an anchor (navigation — e.g. a Human/AI
 * markdown switch); an item WITHOUT `href` renders as a button that sets
 * `value` and emits `change` with { value }.
 *
 * The selected item is a filled foreground pill; the others are muted outline
 * pills. Roving tabindex + arrow-key navigation for accessibility.
 *
 * Attributes: items (JSON: { id, label, href? }[]), value (selected id).
 * Events: `change` with { value } detail (button items only).
 */
export class GaRadioGroup extends GaElement {
  static formAssociated = true;
  static observed = ["items", "value"];

  static styles = /* css */ `
    :host { display: inline-block; }
    .group {
      display: inline-flex;
      gap: var(--ga-space-1, 4px);
      padding: 3px;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-full, 9999px);
    }
    .item {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: var(--ga-space-2, 8px);
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      line-height: 1;
      white-space: nowrap;
      text-decoration: none;
      padding: 7px 16px;
      border: 1px solid transparent;
      border-radius: var(--ga-radius-full, 9999px);
      background: transparent;
      color: var(--ga-muted, #878787);
      cursor: pointer;
      transition: background var(--ga-transition, 0.18s ease),
        color var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease);
    }
    .item:hover { color: var(--ga-fg, #ededed); }
    .item[aria-checked="true"],
    .item[aria-current="page"] {
      background: var(--ga-fg, #ededed);
      color: var(--ga-bg, #000);
      border-color: var(--ga-fg, #ededed);
    }
    .item:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
  `;

  constructor() {
    super();
    this._internals = this.attachInternals?.();
  }

  _parse() {
    try { return JSON.parse(this.attr("items", "[]")); }
    catch { return []; }
  }

  template() {
    const items = this._parse();
    const value = this.attr("value") || items[0]?.id;
    const inner = items.map((it) => {
      const selected = it.id === value;
      const tab = selected ? "0" : "-1";
      if (it.href) {
        return `<a class="item" part="item" data-id="${esc(it.id)}"
          href="${esc(it.href)}" role="radio"
          aria-checked="${selected}"
          ${selected ? `aria-current="page"` : ""}
          tabindex="${tab}">${esc(it.label)}</a>`;
      }
      return `<button class="item" part="item" type="button" data-id="${esc(it.id)}"
        role="radio" aria-checked="${selected}" tabindex="${tab}">${esc(it.label)}</button>`;
    }).join("");
    return /* html */ `<div class="group" part="group" role="radiogroup">${inner}</div>`;
  }

  render() {
    super.render();
    this._internals?.setFormValue(this.value);
    const nodes = [...this.shadowRoot.querySelectorAll(".item")];
    nodes.forEach((node) => {
      if (node.tagName === "BUTTON") {
        node.addEventListener("click", () => this._select(node.dataset.id));
      }
      node.addEventListener("keydown", (e) => this._onKey(e, nodes));
    });
  }

  _onKey(e, nodes) {
    const dir = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!dir) return;
    e.preventDefault();
    const i = nodes.indexOf(e.currentTarget);
    const next = nodes[(i + dir + nodes.length) % nodes.length];
    if (!next) return;
    next.focus();
    // Buttons select on move (radio semantics); anchors only move focus.
    if (next.tagName === "BUTTON") this._select(next.dataset.id);
  }

  _select(id) {
    if (id == null || id === this.attr("value")) return;
    this.setAttribute("value", id);
    this.emit("change", { value: id });
  }

  get value() {
    return this.attr("value") || this._parse()[0]?.id || "";
  }
  set value(v) { this.setAttribute("value", v); }
}

define("ga-radio-group", GaRadioGroup);
