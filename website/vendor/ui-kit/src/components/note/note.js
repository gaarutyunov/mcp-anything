import { GaElement, define, esc } from "../../core/base-element.js";

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
  static observed = ["tone", "title"];

  static styles = /* css */ `
    :host { display: block; }
    .note {
      --_c: var(--ga-accent, #54a2ff);
      margin: 0;
      padding: 14px 16px;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1a1a1a);
      border-left: 3px solid var(--_c);
      border-radius: var(--ga-radius, 6px);
      font-size: 15px;
      line-height: 1.55;
      color: var(--ga-muted, #878787);
    }
    :host([tone="neutral"]) .note { --_c: var(--ga-dim, #454545); }
    :host([tone="info"])    .note { --_c: var(--ga-blue, #54a2ff); }
    :host([tone="success"]) .note { --_c: var(--ga-green, #00c758); }
    :host([tone="warning"]) .note { --_c: var(--ga-amber, #fcbb00); }
    :host([tone="error"])   .note,
    :host([tone="danger"])  .note { --_c: var(--ga-red, #ff6568); }

    .title { margin: 0 0 3px; font-size: 14px; font-weight: 600; color: var(--ga-fg, #ededed); }
  `;

  template() {
    const title = this.attr("title");
    return /* html */ `
      <div class="note" part="note">
        ${title ? `<div class="title" part="title">${esc(title)}</div>` : ""}
        <slot></slot>
      </div>
    `;
  }
}

define("ga-note", GaNote);
