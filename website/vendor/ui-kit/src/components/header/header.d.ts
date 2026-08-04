/**
 * `<ga-header>` — a sticky app header.
 *
 * Modeled on the garutyunov.com header: a solid 56px bar with a brand link on
 * the left and slotted nav actions on the right. Slotted links are muted and
 * brighten to the foreground on hover; the brand does the reverse.
 *
 * Attributes:
 *   brand   brand text (or use the `brand` slot)
 *   href    brand link target
 *   static  boolean — disable sticky positioning (handy when embedding)
 *
 * Slots: `brand` (overrides the attribute), default (right-aligned actions).
 */
export class GaHeader extends GaElement {
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
