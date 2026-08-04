/** A `YYYY-MM-DD` string, and nothing else. */
export function isISODate(v: any): boolean;
/** Parse `YYYY-MM-DD` to a UTC-midnight Date. */
export function utcDate(iso: any): Date;
/** Render a UTC Date back to `YYYY-MM-DD`. */
export function isoOf(date: any): any;
export function addDays(date: any, days: any): Date;
/** Today as `YYYY-MM-DD` in the *local* calendar — what "today" means to a user. */
export function todayISO(): any;
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
    static observed: string[];
    _focusDate: string;
    _wantFocus: boolean;
    get _locale(): string | undefined;
    get _firstDay(): number;
    /** The month on display, as YYYY-MM. */
    get _month(): any;
    _isDisabled(iso: any): boolean;
    /**
     * The single day that holds tabindex="0" (roving tabindex).
     *
     * Prefers a day that is actually selectable: an explicit focus target, then
     * the value, then today, then the first in-range day of the month — so
     * tabbing into a month that begins before `min` does not land on a dead cell.
     */
    _tabDate(): any;
    _shiftMonth(step: any): void;
    _onKey(e: any): void;
    /** Move the roving focus, changing month if the target left the grid. */
    _focusTo(iso: any): void;
    _select(iso: any): void;
    set value(v: string);
    get value(): string;
}
import { GaElement } from "../../core/base-element.js";
