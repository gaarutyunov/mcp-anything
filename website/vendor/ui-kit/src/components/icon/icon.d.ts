/**
 * `<ga-icon>` — a simple line icon that strokes with `currentColor`, so it
 * inherits the surrounding text color.
 *
 * Attributes:
 *   name  icon id (see the Icons page / ICON_NAMES)
 *   size  pixel size (default 20)
 */
export class GaIcon extends GaElement {
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
