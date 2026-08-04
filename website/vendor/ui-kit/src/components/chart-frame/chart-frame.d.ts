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
    static observed: string[];
    /**
     * Legend entries, normalised to `{ label, color? }`.
     *
     * A malformed entry (a bare string, a null, a number) is coerced rather than
     * thrown away or allowed through as-is, so `template()` never has to guess
     * what it is holding.
     */
    _legend(): {
        label: string;
        color: string;
    }[];
}
import { GaElement } from "../../core/base-element.js";
