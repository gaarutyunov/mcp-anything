/**
 * `<ga-bottom-nav>` — a mobile-app bottom navigation bar.
 *
 * A fixed bar of icon + label destinations, one active at a time. Configure
 * with an `items` JSON attribute; the icon is any glyph/emoji.
 *
 *   <ga-bottom-nav active="explore"
 *     items='[{"id":"explore","label":"Explore","icon":"🧭"}, ...]'>
 *   </ga-bottom-nav>
 *
 * Attributes:
 *   items   JSON: { id, label, icon }[]
 *   active  active item id (reflected)
 *   static  boolean — render inline instead of fixed (for embedding/demos)
 *
 * Events: `change` ({ id }).
 */
export class GaBottomNav extends GaElement {
    static observed: string[];
    _parse(): any;
    _select(id: any): void;
}
import { GaElement } from "../../core/base-element.js";
