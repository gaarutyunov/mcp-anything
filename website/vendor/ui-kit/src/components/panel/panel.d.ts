/**
 * `<ga-panel>` — a slide-in drawer.
 *
 * Ported from stereoscope's `.curtain`: a fixed panel that slides in from the
 * edge with a backdrop scrim. Close via the × button, the scrim, or Escape.
 *
 * Attributes:
 *   open   boolean — reflected; toggles visibility
 *   side   "right" (default) | "left"
 *   title  optional header text (overridden by the `header` slot)
 *
 * Slots: `header`, default (body), `footer`.
 * Methods: show() / close() / toggle().
 * Events: `open`, `close`.
 */
export class GaPanel extends GaElement {
    static observed: string[];
    _key: ((e: any) => void) | undefined;
    disconnectedCallback(): void;
    _syncFooter(): void;
    set open(v: boolean);
    get open(): boolean;
    show(): void;
    close(): void;
    toggle(): void;
}
import { GaElement } from "../../core/base-element.js";
