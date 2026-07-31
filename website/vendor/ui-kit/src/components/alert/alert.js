import { GaElement, define, esc } from "../../core/base-element.js";

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
  static observed = ["tone", "title", "dismissible"];

  static styles = /* css */ `
    :host { display: block; }
    .alert {
      --_c: var(--ga-muted, #878787);
      display: flex;
      gap: var(--ga-space-3, 12px);
      padding: var(--ga-space-4, 16px);
      border: 1px solid color-mix(in srgb, var(--_c) 35%, transparent);
      border-left-width: 3px;
      border-radius: var(--ga-radius, 6px);
      background: color-mix(in srgb, var(--_c) 8%, transparent);
      color: var(--ga-fg, #ededed);
      font-size: var(--ga-fs-sm, 14px);
      line-height: 1.5;
    }
    :host([tone="info"])    .alert { --_c: var(--ga-blue, #54a2ff); }
    :host([tone="success"]) .alert { --_c: var(--ga-green, #00c758); }
    :host([tone="warning"]) .alert { --_c: var(--ga-amber, #fcbb00); }
    :host([tone="danger"])  .alert { --_c: var(--ga-red, #ff6568); }

    .dot { flex: none; width: 8px; height: 8px; margin-top: 6px; border-radius: 50%; background: var(--_c); }
    .content { flex: 1; min-width: 0; }
    .title { font-weight: 600; color: var(--_c); margin-bottom: 2px; }
    .close {
      flex: none;
      background: none; border: none; cursor: pointer;
      color: var(--ga-muted, #878787);
      font-size: 18px; line-height: 1; padding: 0 4px;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .close:hover { color: var(--ga-fg, #ededed); }
  `;

  template() {
    const title = this.attr("title");
    const close = this.hasFlag("dismissible")
      ? `<button class="close" part="close" aria-label="Dismiss">&times;</button>` : "";
    return /* html */ `
      <div class="alert" part="alert" role="alert">
        <span class="dot" aria-hidden="true"></span>
        <div class="content">
          ${title ? `<div class="title" part="title">${esc(title)}</div>` : ""}
          <slot></slot>
        </div>
        ${close}
      </div>
    `;
  }

  render() {
    super.render();
    this.$(".close")?.addEventListener("click", () => {
      this.emit("dismiss");
      this.remove();
    });
  }
}

define("ga-alert", GaAlert);
