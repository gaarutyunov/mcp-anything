/**
 * `<ga-card>` — an elevated surface / container.
 *
 * Styled after the "pet projects" cards on garutyunov.com: a translucent
 * surface with a subtle border that lightens on hover (color transitions
 * only — no lift, no shadow), and a slotted title that turns accent-blue when
 * the card is interactive.
 *
 * Attributes:
 *   interactive  boolean — hover affordance + pointer cursor
 *   href         optional — makes the whole card a link
 *   padding      "none" | "sm" | "md" | "lg"  (default md)
 *
 * Slots: `header`, default (body), `footer`.
 */
export class GaCard extends GaElement {
    static observed: string[];
    _sync: (() => void) | undefined;
    _toggleSlots(): void;
}
import { GaElement } from "../../core/base-element.js";
