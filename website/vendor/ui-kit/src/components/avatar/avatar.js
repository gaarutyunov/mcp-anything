import { GaElement, define, esc } from "../../core/base-element.js";

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
  static observed = ["src", "name", "size", "square"];

  static styles = /* css */ `
    :host { display: inline-block; }
    .avatar {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 40px; height: 40px;
      overflow: hidden;
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-full, 9999px);
      user-select: none;
    }
    :host([square]) .avatar { border-radius: var(--ga-radius, 6px); }
    :host([size="sm"]) .avatar { width: 28px; height: 28px; font-size: 11px; }
    :host([size="lg"]) .avatar { width: 64px; height: 64px; font-size: var(--ga-fs-lg, 20px); }
    img { width: 100%; height: 100%; object-fit: cover; display: block; }
  `;

  _initials(name) {
    const parts = name.trim().split(/\s+/).slice(0, 2);
    return parts.map((p) => p[0]?.toUpperCase() ?? "").join("") || "?";
  }

  template() {
    const src = this.attr("src");
    const name = this.attr("name", "");
    const inner = src
      ? `<img src="${esc(src)}" alt="${esc(name)}" loading="lazy" />`
      : `<span aria-hidden="true">${esc(this._initials(name))}</span>`;
    return /* html */ `<div class="avatar" part="avatar" role="img" aria-label="${esc(name)}">${inner}</div>`;
  }
}

define("ga-avatar", GaAvatar);
