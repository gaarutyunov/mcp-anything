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
    static observed: string[];
    _scope: string | undefined;
    disconnectedCallback(): void;
    _sheet: HTMLStyleElement | null | undefined;
    _parse(): any;
    _applyCellRules(cols: any): void;
}
import { GaElement } from "../../core/base-element.js";
