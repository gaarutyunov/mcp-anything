/**
 * `<ga-fab>` — a floating action button.
 *
 * Ported from stereoscope's `.curtain-toggle`: a fixed 56px accent circle,
 * bottom-right by default, respecting safe-area insets. Clicks bubble normally.
 *
 * Attributes:
 *   color     "" | "green" | "amber" | "purple" | "red"
 *   position  "bottom-right" (default) | "bottom-left" | "static"
 *   label     accessible label (default "Action")
 *
 * Slot: default (icon/glyph — defaults to +).
 */
export class GaFab extends GaElement {
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
