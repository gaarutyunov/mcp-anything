/**
 * `<ga-avatar>` — a user/image avatar with initials fallback.
 *
 * Attributes:
 *   src    image URL
 *   name   used for alt text and initials fallback
 *   size   "sm" | "md" | "lg"
 *   square boolean — rounded square instead of circle
 */
export class GaAvatar extends GaElement {
    static observed: string[];
    _initials(name: any): any;
}
import { GaElement } from "../../core/base-element.js";
