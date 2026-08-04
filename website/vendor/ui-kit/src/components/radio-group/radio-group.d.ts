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
    static formAssociated: boolean;
    static observed: string[];
    _internals: ElementInternals;
    _parse(): any;
    _onKey(e: any, nodes: any): void;
    _select(id: any): void;
    set value(v: any);
    get value(): any;
}
import { GaElement } from "../../core/base-element.js";
