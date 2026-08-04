import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-tabs>` — a tab group. Configure with a `tabs` JSON attribute and place
 * matching panels inside, keyed by `slot`:
 *
 *   <ga-tabs tabs='[{"id":"a","label":"One"},{"id":"b","label":"Two"}]'>
 *     <div slot="a">First panel</div>
 *     <div slot="b">Second panel</div>
 *   </ga-tabs>
 *
 * Attributes: tabs (JSON), active (id). Events: `change` with { id }.
 */
export class GaTabs extends GaElement {
  static observed = ["tabs", "active"];

  static styles = /* css */ `
    :host { display: block; }
    .list {
      display: flex;
      gap: var(--ga-space-1, 4px);
      border-bottom: 1px solid var(--ga-border, #1a1a1a);
    }
    .tab {
      position: relative;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      color: var(--ga-muted, #878787);
      background: none;
      border: none;
      padding: 10px 14px;
      cursor: pointer;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .tab:hover { color: var(--ga-fg, #ededed); }
    .tab[aria-selected="true"] { color: var(--ga-fg, #ededed); }
    .tab[aria-selected="true"]::after {
      content: "";
      position: absolute;
      left: 8px; right: 8px; bottom: -1px;
      height: 2px;
      background: var(--ga-accent, #54a2ff);
      border-radius: 2px;
    }
    .tab:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
      border-radius: var(--ga-radius, 6px);
    }
    .panels { padding-top: var(--ga-space-4, 16px); }
  `;

  _parse() {
    try { return JSON.parse(this.attr("tabs", "[]")); }
    catch { return []; }
  }

  template() {
    const tabs = this._parse();
    const active = this.attr("active") || tabs[0]?.id;
    const buttons = tabs.map((t) => `
      <button class="tab" part="tab" role="tab" data-id="${esc(t.id)}"
        aria-selected="${t.id === active}" tabindex="${t.id === active ? "0" : "-1"}">
        ${esc(t.label)}
      </button>`).join("");
    const panels = tabs.map((t) => `
      <div role="tabpanel" ${t.id === active ? "" : "hidden"}>
        <slot name="${esc(t.id)}"></slot>
      </div>`).join("");
    return /* html */ `
      <div class="list" part="list" role="tablist">${buttons}</div>
      <div class="panels" part="panels">${panels}</div>
    `;
  }

  render() {
    super.render();
    this.shadowRoot.querySelectorAll(".tab").forEach((btn) => {
      btn.addEventListener("click", () => this._select(btn.dataset.id));
    });
  }

  _select(id) {
    if (id === this.attr("active")) return;
    this.setAttribute("active", id);
    this.emit("change", { id });
  }
}

define("ga-tabs", GaTabs);
