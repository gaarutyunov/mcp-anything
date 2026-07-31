/**
 * `<ga-code>` — a copyable code / command block.
 *
 * The code text is the default slot. By default the block shows a copy button
 * that writes the text to the clipboard and briefly flips to a check mark:
 *
 *   <ga-code prompt="$">npm install @gaarutyunov/ui-kit</ga-code>
 *
 * When `href` is set the block renders as a link with a trailing ↗ external
 * arrow instead of a copy button (no clipboard action):
 *
 *   <ga-code href="https://example.com/install.sh">curl … | sh</ga-code>
 *
 * Attributes:
 *   prompt   optional leading glyph (e.g. "$", "›")
 *   href     render as an external link (↗) instead of a copy button
 *   target   (link) forwarded to the anchor
 *   rel      (link) forwarded to the anchor
 *
 * Events: `copy` with { text } detail when copied.
 */
export class GaCode extends GaElement {
    static observed: string[];
    _pass(name: any, out?: any): string;
    _copy(btn: any): void;
    _t: number | undefined;
    disconnectedCallback(): void;
}
import { GaElement } from "../../core/base-element.js";
