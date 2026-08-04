/**
 * Parse a typed date to `YYYY-MM-DD`, or null.
 *
 * ISO is accepted unconditionally. Otherwise the three numbers are mapped
 * using the locale's *own* field order, read from Intl rather than assumed —
 * `14/03/2026` is day-first in en-GB and nonsense in en-US, and only the
 * locale knows which.
 */
export function parseDate(text: any, locale: any): string | null;
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
    static formAssociated: boolean;
    static observed: string[];
    _internals: ElementInternals;
    _open: boolean;
    _invalid: boolean;
    _popup: {
        show: () => void;
        close: () => void;
        reposition: () => void;
        readonly open: boolean;
        destroy(): void;
    } | null;
    get _locale(): string | undefined;
    disconnectedCallback(): void;
    _toggle(): void;
    _openPanel(): void;
    _close({ focusButton }?: {
        focusButton?: boolean | undefined;
    }): void;
    _syncOpen(): void;
    /** Parse what was typed; adopt it only if it is a real, in-range date. */
    _commitTyped(text: any): void;
    _outOfRange(iso: any): boolean;
    _setInvalid(invalid: any): void;
    _commit(iso: any): void;
    set value(v: string);
    get value(): string;
}
import { GaElement } from "../../core/base-element.js";
