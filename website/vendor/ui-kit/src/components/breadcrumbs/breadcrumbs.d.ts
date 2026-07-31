/**
 * `<ga-breadcrumbs>` — a monospace breadcrumb trail (matches the CV / skill
 * navigation on garutyunov.com). Configure with an `items` JSON attribute:
 *
 *   <ga-breadcrumbs items='[
 *     {"label":"Home","href":"/"},
 *     {"label":"Skills","href":"/skills"},
 *     {"label":"TypeScript"}
 *   ]'></ga-breadcrumbs>
 *
 * The last item is the current page — rendered in the foreground colour with no
 * link. Earlier items are muted links, separated by "/".
 *
 * Attributes: items (JSON: { label, href? }[]).
 */
export class GaBreadcrumbs extends GaElement {
    static observed: string[];
    _parse(): any;
}
import { GaElement } from "../../core/base-element.js";
