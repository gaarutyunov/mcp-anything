/**
 * `<ga-alert>` — a callout / banner for status messages.
 *
 * Attributes:
 *   tone        "info" | "success" | "warning" | "danger" | "neutral"
 *   title       optional heading
 *   dismissible boolean — shows a close button, emits `dismiss`
 *
 * Slot: default (message body).
 */
export class GaAlert extends GaElement {
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
