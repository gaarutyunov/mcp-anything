/**
 * `<ga-button>` — the kit's primary action element.
 *
 * Attributes:
 *   variant     "primary" | "secondary" | "ghost" | "danger"  (default secondary)
 *   size        "sm" | "md" | "lg"                              (default md)
 *   href        render as a link instead of a button
 *   download    (link) filename hint / force-download           — forwarded to <a>
 *   target      (link) "_blank" | "_self" | …                   — forwarded to <a>
 *   rel         (link) e.g. "noopener noreferrer"               — forwarded to <a>
 *   type        (button) "button" | "submit" | "reset"          — forwarded to <button>
 *   name        (button) form control name                      — forwarded to <button>
 *   aria-label  accessible label                                — forwarded to <a>/<button>
 *   disabled    boolean
 *   loading     boolean — shows a spinner and blocks clicks
 *   block       boolean — full width
 *
 * Slots: default (label), `start` / `end` (icons).
 */
export class GaButton extends GaElement {
    static observed: string[];
    disconnectedCallback(): void;
    _guard: (e: any) => void;
    /** Forward `name` from the host as attribute `out` on the inner element. */
    _pass(name: any, out?: any): string;
}
import { GaElement } from "../../core/base-element.js";
