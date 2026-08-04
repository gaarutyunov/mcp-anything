import { GaElement, define, esc } from "../../core/base-element.js";

let _uid = 0;

/**
 * `<ga-table>` — a data table with a shared column grid.
 *
 * Columns are declared once with a `columns` JSON attribute; the component
 * renders the header and exposes the resulting grid template as the
 * `--ga-table-cols` custom property. Rows are **slotted light-DOM elements** —
 * a `<div>` or an `<a href>` (so a whole row can be a link) — each containing
 * one child element per column. Rows inherit the grid template, so they stay
 * column-aligned without a JSON-only cell model, keeping rich cells (e.g. a
 * title + tagline) possible:
 *
 *   <ga-table columns='[
 *     {"label":"#","width":"48px","align":"right","mono":true},
 *     {"label":"Skill"},
 *     {"label":"Score","align":"right","mono":true,"width":"96px"}
 *   ]'>
 *     <a href="/skills/typescript">
 *       <span>1</span>
 *       <div><strong>TypeScript</strong><div>Static types</div></div>
 *       <span>982</span>
 *     </a>
 *   </ga-table>
 *
 * Columns: { label, align?: "left"|"center"|"right", width?, mono?: boolean }.
 * Alignment / monospacing declared on a column applies to that column's cell in
 * every row (via a scoped stylesheet). Style rows with the `row` slot's
 * `::slotted` rules or hook the shadow parts: `table`, `header`, `head-cell`.
 */
export class GaTable extends GaElement {
  static observed = ["columns"];

  static styles = /* css */ `
    :host { display: block; }
    .table {
      border: 1px solid var(--ga-border, #1a1a1a);
      border-radius: var(--ga-radius-lg, 8px);
      overflow: hidden;
    }
    .head {
      display: grid;
      grid-template-columns: var(--ga-table-cols, 1fr);
      align-items: center;
      gap: var(--ga-space-4, 16px);
      padding: 10px var(--ga-space-4, 16px);
      background: color-mix(in srgb, var(--ga-bg-elev, #1a1a1a) 40%, transparent);
      border-bottom: 1px solid var(--ga-border, #1a1a1a);
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 600;
      letter-spacing: 0.02em;
      text-transform: uppercase;
      color: var(--ga-muted, #878787);
    }
    .head .mono { font-family: var(--ga-font-mono, ui-monospace, monospace); text-transform: none; }

    /* Slotted rows share the column grid and get borders + hover. */
    ::slotted(*) {
      display: grid !important;
      grid-template-columns: var(--ga-table-cols, 1fr);
      align-items: center;
      gap: var(--ga-space-4, 16px);
      padding: var(--ga-space-3, 12px) var(--ga-space-4, 16px);
      border-top: 1px solid var(--ga-border, #1a1a1a);
      color: var(--ga-fg, #ededed);
      text-decoration: none;
      transition: background var(--ga-transition, 0.18s ease);
    }
    ::slotted(:first-child) { border-top: none; }
    ::slotted(*:hover) { background: var(--ga-bg-elev-hover, #1f1f1f); }
    ::slotted(a) { cursor: pointer; }
    ::slotted(a:focus-visible) {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
  `;

  connectedCallback() {
    this._scope = "ga-table-" + (_uid++);
    this.setAttribute("data-ga-scope", this._scope);
    super.connectedCallback();
  }

  disconnectedCallback() {
    this._sheet?.remove();
    this._sheet = null;
  }

  _parse() {
    try { return JSON.parse(this.attr("columns", "[]")); }
    catch { return []; }
  }

  template() {
    const cols = this._parse();
    const tpl = cols.map((c) => c.width || "minmax(0, 1fr)").join(" ") || "1fr";
    // Expose the shared grid template; rows (light DOM) inherit it.
    this.style.setProperty("--ga-table-cols", tpl);

    const head = cols.map((c) => {
      const cls = "cell" + (c.mono ? " mono" : "");
      const style = c.align ? `text-align:${esc(c.align)}` : "";
      return `<span class="${cls}" part="head-cell" style="${style}">${esc(c.label ?? "")}</span>`;
    }).join("");

    // Scoped light-DOM rules so per-column align / mono reach the cells, which
    // live outside the shadow tree (they are the consumer's own elements).
    this._applyCellRules(cols);

    return /* html */ `
      <div class="table" part="table">
        <div class="head" part="header" role="row">${head}</div>
        <slot part="body"></slot>
      </div>
    `;
  }

  _applyCellRules(cols) {
    const rules = cols
      .map((c, i) => {
        const decls = [];
        if (c.align) decls.push(`text-align:${c.align}`);
        if (c.mono) decls.push("font-family:var(--ga-font-mono, ui-monospace, monospace);font-variant-numeric:tabular-nums");
        if (!decls.length) return "";
        return `ga-table[data-ga-scope="${this._scope}"] > *:not([slot]) > :nth-child(${i + 1})`
          + `{${decls.join(";")}}`;
      })
      .filter(Boolean)
      .join("\n");
    if (!this._sheet) {
      this._sheet = document.createElement("style");
      document.head.appendChild(this._sheet);
    }
    this._sheet.textContent = rules;
  }
}

define("ga-table", GaTable);
