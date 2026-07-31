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
    static formAssociated: boolean;
    static observed: string[];
    _internals: ElementInternals;
    _open: boolean;
    _active: number;
    _filterText: string;
    _typeahead: string;
    _typeaheadAt: number;
    _filterTimer: number;
    _popup: {
        show: () => void;
        close: () => void;
        reposition: () => void;
        readonly open: boolean;
        destroy(): void;
    } | null;
    _values: any;
    _reflecting: boolean;
    attributeChangedCallback(name: any, oldValue: any, newValue: any): void;
    /** Options from the JSON attribute, falling back to slotted <option>s. */
    _allOptions(): {
        value: string;
        label: string;
        disabled: boolean;
    }[];
    /** Options after the local filter. */
    _visibleOptions(): {
        value: string;
        label: string;
        disabled: boolean;
    }[];
    get multiple(): boolean;
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
    _selected(): any;
    /** Set the selection and mirror it to the attribute. */
    _setSelected(values: any): void;
    _summary(): string;
    /** The option rows alone, so filtering can repaint them without a re-render. */
    _rows(): string;
    /** Slotted options changed: refresh what is derived from them, not the tree. */
    _onSlotChange(): void;
    disconnectedCallback(): void;
    _bindRows(): void;
    /** Repaint only the rows — keeps the filter field's focus and caret. */
    _repaintRows(): void;
    _toggle(): void;
    _openPanel(): void;
    _close({ focusTrigger }?: {
        focusTrigger?: boolean | undefined;
    }): void;
    /** Reflect open state without re-rendering (which would drop focus). */
    _syncOpenState(): void;
    _syncActive(): void;
    _onTriggerKey(e: any): void;
    /** Move `dir` steps from the active row, skipping disabled options. */
    _move(dir: any, options: any): void;
    /** Jump to `index`, then skip forward in `dir` if it landed on a disabled row. */
    _moveTo(index: any, options: any, dir: any): void;
    _onTypeahead(char: any, options: any): void;
    _onFilterInput(text: any): void;
    _commit(value: any, { keepOpen }?: {
        keepOpen?: boolean | undefined;
    }): void;
    _formValue(): any;
    set value(v: any);
    get value(): any;
    set options(list: {
        value: string;
        label: string;
        disabled: boolean;
    }[]);
    get options(): {
        value: string;
        label: string;
        disabled: boolean;
    }[];
}
import { GaElement } from "../../core/base-element.js";
