import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-calendar>` — a month grid for picking one date.
 *
 *   <ga-calendar value="2026-03-14" min="2026-01-01" max="2026-12-31"></ga-calendar>
 *
 * Dates are plain `YYYY-MM-DD` strings throughout — never `Date` objects across
 * the boundary. A `Date` carries a time and a timezone, and `new Date("2026-03-14")`
 * parses as **UTC midnight**, which is the previous day in every timezone west
 * of Greenwich. Keeping the calendar-date string as the value means the date a
 * user picks is the date the form submits, in Kiritimati and in Niue alike.
 * Month arithmetic runs on UTC dates internally, where those shifts cannot
 * happen, and only ever leaves as a string.
 *
 * Attributes:
 *   value (YYYY-MM-DD), month (YYYY-MM, the month on display), locale,
 *   first-day (0 = Sunday … 6 = Saturday, default 1 = Monday),
 *   min, max (YYYY-MM-DD), disabled (boolean)
 *
 * Events: `change` with { value } detail.
 */
export class GaCalendar extends GaElement {
  static observed = ["value", "month", "locale", "first-day", "min", "max", "disabled"];

  static styles = /* css */ `
    :host {
      display: inline-block;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: var(--ga-space-3, 12px);
    }
    .head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--ga-space-2, 8px);
      margin-bottom: var(--ga-space-2, 8px);
    }
    .title {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
    }
    .nav {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 28px;
      height: 28px;
      color: var(--ga-muted, #878787);
      background: transparent;
      border: 1px solid transparent;
      border-radius: var(--ga-radius-sm, 4px);
      cursor: pointer;
    }
    .nav:hover { color: var(--ga-fg, #ededed); background: var(--ga-bg-elev-hover, #232323); }
    .nav:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    .nav svg { width: 14px; height: 14px; }

    table { border-collapse: collapse; }
    th {
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 500;
      color: var(--ga-muted, #878787);
      padding: 4px 0;
      width: 34px;
    }
    td { padding: 1px; }
    .day {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: transparent;
      border: 1px solid transparent;
      border-radius: var(--ga-radius-sm, 4px);
      cursor: pointer;
    }
    .day:hover:not([aria-disabled="true"]) { background: var(--ga-bg-elev-hover, #232323); }
    .day.outside { color: var(--ga-dim, #454545); }
    .day.today { border-color: var(--ga-border-strong, #2a2a2a); font-weight: 600; }
    .day[aria-selected="true"] {
      background: var(--ga-fg, #ededed);
      color: var(--ga-bg, #000);
      border-color: var(--ga-fg, #ededed);
      font-weight: 600;
    }
    /* aria-disabled rather than the disabled attribute: a disabled grid cell
       must stay focusable, or arrow-key navigation dead-ends on it (WAI-ARIA
       grid pattern). Selection is guarded in _select instead. */
    .day[aria-disabled="true"] { opacity: 0.3; cursor: not-allowed; }
    .day:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    :host([disabled]) { opacity: 0.5; pointer-events: none; }
  `;

  constructor() {
    super();
    // The day the roving tabindex sits on, as YYYY-MM-DD.
    this._focusDate = "";
    this._wantFocus = false;
  }

  get _locale() {
    return this.attr("locale") || undefined;
  }

  get _firstDay() {
    const n = Number(this.attr("first-day", "1"));
    return Number.isInteger(n) && n >= 0 && n <= 6 ? n : 1;
  }

  /** The month on display, as YYYY-MM. */
  get _month() {
    const explicit = this.attr("month");
    if (/^\d{4}-\d{2}$/.test(explicit)) return explicit;
    const value = this.attr("value");
    if (isISODate(value)) return value.slice(0, 7);
    return todayISO().slice(0, 7);
  }

  _isDisabled(iso) {
    const min = this.attr("min");
    const max = this.attr("max");
    if (isISODate(min) && iso < min) return true;
    if (isISODate(max) && iso > max) return true;
    return false;
  }

  template() {
    const month = this._month;
    const [year, mon] = month.split("-").map(Number);
    const selected = this.attr("value");
    const today = todayISO();

    const monthLabel = new Intl.DateTimeFormat(this._locale, {
      month: "long",
      year: "numeric",
      timeZone: "UTC",
    }).format(utcDate(`${month}-01`));

    // Weekday headers, rotated to the configured first day.
    const dayNames = weekdayNames(this._locale, this._firstDay);

    const first = utcDate(`${month}-01`);
    const lead = (first.getUTCDay() - this._firstDay + 7) % 7;
    const start = addDays(first, -lead);

    let cells = "";
    for (let week = 0; week < 6; week++) {
      let row = "";
      for (let d = 0; d < 7; d++) {
        const date = addDays(start, week * 7 + d);
        const iso = isoOf(date);
        const outside = date.getUTCMonth() + 1 !== mon || date.getUTCFullYear() !== year;
        const disabled = this._isDisabled(iso);
        const isSelected = iso === selected;
        const classes = ["day"];
        if (outside) classes.push("outside");
        if (iso === today) classes.push("today");
        row += `<td role="gridcell">
          <button class="${classes.join(" ")}" part="day" type="button"
            data-iso="${iso}"
            tabindex="${iso === this._tabDate() ? "0" : "-1"}"
            aria-selected="${isSelected}"
            ${disabled ? `aria-disabled="true"` : ""}
            aria-label="${esc(longDate(iso, this._locale))}">${date.getUTCDate()}</button>
        </td>`;
      }
      cells += `<tr role="row">${row}</tr>`;
    }

    return /* html */ `
      <div class="head">
        <button class="nav" part="prev" type="button" data-step="-1" aria-label="Previous month">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M10 3L5 8l5 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <div class="title" part="title" aria-live="polite">${esc(monthLabel)}</div>
        <button class="nav" part="next" type="button" data-step="1" aria-label="Next month">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M6 3l5 5-5 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
      </div>
      <table role="grid" aria-label="${esc(monthLabel)}">
        <thead><tr role="row">
          ${dayNames.map((n) => `<th role="columnheader" abbr="${esc(n.long)}" scope="col">${esc(n.short)}</th>`).join("")}
        </tr></thead>
        <tbody>${cells}</tbody>
      </table>
    `;
  }

  /**
   * The single day that holds tabindex="0" (roving tabindex).
   *
   * Prefers a day that is actually selectable: an explicit focus target, then
   * the value, then today, then the first in-range day of the month — so
   * tabbing into a month that begins before `min` does not land on a dead cell.
   */
  _tabDate() {
    const month = this._month;
    const candidates = [this._focusDate, this.attr("value"), todayISO()];
    for (const iso of candidates) {
      if (isISODate(iso) && iso.slice(0, 7) === month && !this._isDisabled(iso)) return iso;
    }
    const days = new Date(Date.UTC(Number(month.slice(0, 4)), Number(month.slice(5, 7)), 0)).getUTCDate();
    for (let d = 1; d <= days; d++) {
      const iso = `${month}-${String(d).padStart(2, "0")}`;
      if (!this._isDisabled(iso)) return iso;
    }
    // Every day in this month is out of range; the first one still needs a stop.
    return `${month}-01`;
  }

  render() {
    super.render();

    this.shadowRoot.querySelectorAll(".nav").forEach((btn) => {
      btn.addEventListener("click", () => this._shiftMonth(Number(btn.dataset.step)));
    });
    this.shadowRoot.querySelectorAll(".day").forEach((btn) => {
      btn.addEventListener("click", () => this._select(btn.dataset.iso));
      btn.addEventListener("keydown", (e) => this._onKey(e));
    });

    if (this._wantFocus) {
      this._wantFocus = false;
      this.shadowRoot.querySelector(`.day[data-iso="${this._tabDate()}"]`)?.focus();
    }
  }

  _shiftMonth(step) {
    const [y, m] = this._month.split("-").map(Number);
    const next = new Date(Date.UTC(y, m - 1 + step, 1));
    this.setAttribute("month", isoOf(next).slice(0, 7));
  }

  _onKey(e) {
    const steps = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 };
    const iso = e.currentTarget.dataset.iso;

    if (steps[e.key] !== undefined) {
      e.preventDefault();
      this._focusTo(isoOf(addDays(utcDate(iso), steps[e.key])));
      return;
    }
    if (e.key === "Home" || e.key === "End") {
      e.preventDefault();
      const date = utcDate(iso);
      const dow = (date.getUTCDay() - this._firstDay + 7) % 7;
      this._focusTo(isoOf(addDays(date, e.key === "Home" ? -dow : 6 - dow)));
      return;
    }
    if (e.key === "PageUp" || e.key === "PageDown") {
      e.preventDefault();
      const [y, m, d] = iso.split("-").map(Number);
      const step = e.key === "PageUp" ? -1 : 1;
      // Clamp to the last day of the target month, so 31 Mar + 1 month is
      // 30 Apr rather than spilling into May.
      const lastDay = new Date(Date.UTC(y, m + step, 0)).getUTCDate();
      this._focusTo(isoOf(new Date(Date.UTC(y, m - 1 + step, Math.min(d, lastDay)))));
    }
  }

  /** Move the roving focus, changing month if the target left the grid. */
  _focusTo(iso) {
    this._focusDate = iso;
    this._wantFocus = true;
    if (iso.slice(0, 7) !== this._month) {
      this.setAttribute("month", iso.slice(0, 7)); // triggers render
    } else {
      this.render();
    }
  }

  _select(iso) {
    if (!iso || this._isDisabled(iso) || this.hasFlag("disabled")) return;
    this._focusDate = iso;
    this.setAttribute("value", iso);
    this.emit("change", { value: iso });
  }

  get value() {
    return this.attr("value");
  }
  set value(v) {
    this.setAttribute("value", v ?? "");
  }
}

/* --- date helpers, all UTC so no timezone can shift a calendar date ------ */

/** A `YYYY-MM-DD` string, and nothing else. */
export function isISODate(v) {
  return typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v);
}

/** Parse `YYYY-MM-DD` to a UTC-midnight Date. */
export function utcDate(iso) {
  const [y, m, d] = iso.split("-").map(Number);
  return new Date(Date.UTC(y, (m || 1) - 1, d || 1));
}

/** Render a UTC Date back to `YYYY-MM-DD`. */
export function isoOf(date) {
  return date.toISOString().slice(0, 10);
}

export function addDays(date, days) {
  return new Date(date.getTime() + days * 86400000);
}

/** Today as `YYYY-MM-DD` in the *local* calendar — what "today" means to a user. */
export function todayISO() {
  const now = new Date();
  return isoOf(new Date(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate())));
}

function longDate(iso, locale) {
  return new Intl.DateTimeFormat(locale, { dateStyle: "long", timeZone: "UTC" })
    .format(utcDate(iso));
}

/** Weekday names starting at `firstDay`, from Intl rather than a hard-coded list. */
function weekdayNames(locale, firstDay) {
  const short = new Intl.DateTimeFormat(locale, { weekday: "short", timeZone: "UTC" });
  const long = new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" });
  // 2024-01-07 was a Sunday, so it anchors day 0 without a lookup table.
  const sunday = Date.UTC(2024, 0, 7);
  return Array.from({ length: 7 }, (_, i) => {
    const date = new Date(sunday + ((i + firstDay) % 7) * 86400000);
    return { short: short.format(date), long: long.format(date) };
  });
}

define("ga-calendar", GaCalendar);
