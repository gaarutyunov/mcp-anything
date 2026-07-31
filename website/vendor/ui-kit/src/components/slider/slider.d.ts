/**
 * `<ga-slider>` — a range slider.
 *
 * Built on a native range input (stereoscope styles ranges with
 * `accent-color`); this adds a themed track/thumb and an optional label + live
 * value readout. Form-associated.
 *
 * Attributes: min, max, step, value, label, disabled
 * Events: `input`, `change` — both with { value }.
 */
export class GaSlider extends GaElement {
    static formAssociated: boolean;
    static observed: string[];
    _internals: ElementInternals;
    _value: any;
    set value(v: any);
    get value(): any;
}
import { GaElement } from "../../core/base-element.js";
