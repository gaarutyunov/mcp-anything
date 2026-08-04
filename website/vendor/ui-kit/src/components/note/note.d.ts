/**
 * `<ga-note>` — an inline note / callout with a colored left strip.
 *
 * Ported from stereoscope's `.converter-note`: a solid elevated surface with a
 * 3px accent strip down the left edge. Tones recolor the strip.
 *
 * Attributes:
 *   tone   "info" | "success" | "warning" | "error" | "neutral" (default info)
 *   title  optional heading
 *
 * Slot: default (message body).
 */
export class GaNote extends GaElement {
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
