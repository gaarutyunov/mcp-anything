/**
 * `<ga-switch>` — an accessible on/off toggle.
 *
 * Attributes: checked, disabled (boolean), label
 * Events: `change` with { checked } detail.
 */
export class GaSwitch extends GaElement {
    static formAssociated: boolean;
    static observed: string[];
    toggle(): void;
    set checked(v: boolean);
    get checked(): boolean;
}
import { GaElement } from "../../core/base-element.js";
