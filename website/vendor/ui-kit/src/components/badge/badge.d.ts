/**
 * `<ga-badge>` — a small status / category pill.
 *
 * Styled after the skill chips on garutyunov.com: a subtle outline pill
 * (`border` + `muted` text, transparent fill). Colored variants tint the text
 * and border; `solid` fills it.
 *
 * Attributes:
 *   color  "default" | "blue" | "green" | "amber" | "purple" | "red"
 *   solid  boolean — filled instead of subtle outline
 *   size   "sm" | "md"
 *
 * Slot: default (text).
 */
export class GaBadge extends GaElement {
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
