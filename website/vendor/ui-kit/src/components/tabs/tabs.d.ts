/**
 * `<ga-tabs>` — a tab group. Configure with a `tabs` JSON attribute and place
 * matching panels inside, keyed by `slot`:
 *
 *   <ga-tabs tabs='[{"id":"a","label":"One"},{"id":"b","label":"Two"}]'>
 *     <div slot="a">First panel</div>
 *     <div slot="b">Second panel</div>
 *   </ga-tabs>
 *
 * Attributes: tabs (JSON), active (id). Events: `change` with { id }.
 */
export class GaTabs extends GaElement {
    static observed: string[];
    _parse(): any;
    _select(id: any): void;
}
import { GaElement } from "../../core/base-element.js";
