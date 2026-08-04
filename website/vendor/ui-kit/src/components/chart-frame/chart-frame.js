import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-chart-frame>` — the furniture around a chart: title, legend, loading
 * and empty states, and a responsive plot area.
 *
 * **It draws no data.** The kit is zero-dependency and is not going to grow a
 * charting engine; what an app actually re-implements every time is the frame,
 * not the line. Slot in whatever plots — an inline `<svg>`, a canvas, a
 * charting library's node — and take the palette from the `--ga-chart-*`
 * tokens so every chart in the house agrees on series colours.
 *
 *   <ga-chart-frame title="Volume by week"
 *     legend='[{"label":"Squat"},{"label":"Bench"}]'>
 *     <svg viewBox="0 0 400 160">…</svg>
 *   </ga-chart-frame>
 *
 * Legend swatches take `--ga-chart-1…8` in series order unless an entry names
 * its own `color`.
 *
 * Attributes:
 *   title, legend (JSON: { label, color? }[]), height (CSS length),
 *   empty-text, loading, empty (boolean)
 *
 * Slots: (default) — the plot; `footer` — a caption or axis note.
 */
export class GaChartFrame extends GaElement {
  static observed = ["title", "legend", "height", "empty-text", "loading", "empty"];

  static styles = /* css */ `
    :host { display: block; }
    .frame {
      display: flex;
      flex-direction: column;
      gap: var(--ga-space-3, 12px);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1f1f1f);
      border-radius: var(--ga-radius-lg, 8px);
      padding: var(--ga-space-4, 16px);
    }
    /* The caption and the legend are siblings of the plot (the caption has to
       be a direct child of <figure>), so the frame lays out the header row. */
    .frame > .title { order: -2; }
    .frame > .legend { order: -1; }
    .title {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
    }
    .legend {
      display: flex;
      flex-wrap: wrap;
      gap: var(--ga-space-3, 12px);
      margin: 0;
      padding: 0;
      list-style: none;
    }
    .legend li {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-size: var(--ga-fs-xs, 12px);
      color: var(--ga-chart-label, #878787);
    }
    .swatch {
      width: 10px;
      height: 10px;
      border-radius: 2px;
      background: var(--swatch);
      flex: none;
    }
    .plot {
      position: relative;
      min-height: var(--plot-height, 180px);
    }
    .plot ::slotted(svg),
    .plot ::slotted(canvas) { display: block; width: 100%; height: auto; }
    .state {
      position: absolute;
      inset: 0;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: var(--ga-space-2, 8px);
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-muted, #878787);
      background: var(--ga-bg-elev, #1a1a1a);
    }
    .spinner {
      width: 16px;
      height: 16px;
      border: 2px solid var(--ga-border-strong, #2a2a2a);
      border-top-color: var(--ga-accent, #54a2ff);
      border-radius: 50%;
      animation: spin 0.7s linear infinite;
    }
    @keyframes spin { to { transform: rotate(360deg); } }
    .footer {
      font-size: var(--ga-fs-xs, 12px);
      color: var(--ga-muted, #878787);
    }
  `;

  /**
   * Legend entries, normalised to `{ label, color? }`.
   *
   * A malformed entry (a bare string, a null, a number) is coerced rather than
   * thrown away or allowed through as-is, so `template()` never has to guess
   * what it is holding.
   */
  _legend() {
    let parsed;
    try {
      parsed = JSON.parse(this.attr("legend", "[]"));
    } catch {
      return [];
    }
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((entry) => entry != null)
      .map((entry) =>
        typeof entry === "object"
          ? { label: String(entry.label ?? ""), color: entry.color ? String(entry.color) : "" }
          : { label: String(entry), color: "" }
      );
  }

  template() {
    const title = this.attr("title");
    const legend = this._legend();
    const loading = this.hasFlag("loading");
    const empty = this.hasFlag("empty");
    const height = this.attr("height", "180px");

    const items = legend
      .map((s, i) => {
        // Series colours cycle through the eight palette tokens; past eight,
        // colour alone is not carrying the distinction anyway.
        const color = s.color || `var(--ga-chart-${(i % 8) + 1})`;
        return `<li><span class="swatch" style="--swatch:${esc(color)}"></span>${esc(s.label ?? "")}</li>`;
      })
      .join("");

    let state = "";
    if (loading) {
      state = `<div class="state" part="state" role="status">
        <span class="spinner" aria-hidden="true"></span> Loading…
      </div>`;
    } else if (empty) {
      state = `<div class="state" part="state" role="status">${esc(this.attr("empty-text", "No data"))}</div>`;
    }

    return /* html */ `
      <figure class="frame" part="frame" style="--plot-height:${esc(height)}">
        ${/* <figcaption> must be a direct child of <figure> — nesting it in a
              wrapper drops the figure's accessible name. The legend keeps its
              own container; the caption sits beside it and the two are laid
              out by .frame. */ ""}
        ${title ? `<figcaption class="title" part="title">${esc(title)}</figcaption>` : ""}
        ${items ? `<ul class="legend" part="legend">${items}</ul>` : ""}
        <div class="plot" part="plot" aria-busy="${loading}">
          <slot></slot>
          ${state}
        </div>
        <div class="footer" part="footer"><slot name="footer"></slot></div>
      </figure>
    `;
  }
}

define("ga-chart-frame", GaChartFrame);
