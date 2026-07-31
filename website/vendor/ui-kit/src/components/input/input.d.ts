/**
 * `<ga-input>` — a labelled text field. Form-associated: participates in
 * native <form> submission via ElementInternals.
 *
 * Attributes:
 *   label, placeholder, type, value, name, hint, error
 *   disabled, required (boolean)
 *
 * Events: `input`, `change` (re-dispatched with { value } detail).
 */
export class GaInput extends GaElement {
    static formAssociated: boolean;
    static observed: string[];
    _internals: ElementInternals;
    _value: any;
    set value(v: any);
    get value(): any;
}
import { GaElement } from "../../core/base-element.js";
