import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-chat-message>` — one turn in a transcript.
 *
 *   <ga-chat-message role="user" author="You" time="09:12">Log 3x5 at 100kg</ga-chat-message>
 *   <ga-chat-message role="assistant" state="streaming">Logged. That is…</ga-chat-message>
 *
 * `role` picks the alignment and treatment; `state` says whether the turn is
 * settled. A streaming turn marks its body `aria-live="polite"` so a screen
 * reader hears the text as it arrives, and a pending one is announced once —
 * an assistant turn that silently grows is invisible to anyone not watching.
 *
 * Attributes:
 *   role (`user` | `assistant` | `system`), state (`sent` | `pending` |
 *   `streaming` | `error`), author, time
 *
 * Slots: (default) — the message body.
 */
export class GaChatMessage extends GaElement {
  static observed = ["role", "state", "author", "time"];

  static styles = /* css */ `
    :host { display: block; }
    .row { display: flex; flex-direction: column; gap: 4px; max-width: 100%; }
    :host([role="user"]) .row { align-items: flex-end; }

    .meta {
      display: flex;
      align-items: baseline;
      gap: var(--ga-space-2, 8px);
      font-size: var(--ga-fs-xs, 12px);
      color: var(--ga-muted, #878787);
      padding: 0 2px;
    }
    .bubble {
      max-width: min(52ch, 100%);
      font-size: var(--ga-fs-sm, 14px);
      line-height: 1.55;
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1f1f1f);
      border-radius: var(--ga-radius-lg, 8px);
      padding: 10px 13px;
      overflow-wrap: anywhere;
    }
    :host([role="user"]) .bubble {
      background: var(--ga-fg, #ededed);
      border-color: var(--ga-fg, #ededed);
      color: var(--ga-bg, #000);
    }
    :host([role="system"]) .row { align-items: center; }
    :host([role="system"]) .bubble {
      background: transparent;
      border: 0;
      color: var(--ga-muted, #878787);
      font-size: var(--ga-fs-xs, 12px);
      text-align: center;
      padding: 4px 0;
    }
    :host([state="error"]) .bubble {
      border-color: var(--ga-red, #ff6568);
      color: var(--ga-red, #ff6568);
    }
    :host([state="pending"]) .bubble { opacity: 0.6; }

    .dots { display: inline-flex; gap: 3px; vertical-align: middle; }
    .dots i {
      width: 4px; height: 4px; border-radius: 50%;
      background: currentColor;
      animation: blink 1.2s infinite ease-in-out;
    }
    .dots i:nth-child(2) { animation-delay: 0.15s; }
    .dots i:nth-child(3) { animation-delay: 0.3s; }
    @keyframes blink { 0%, 60%, 100% { opacity: 0.25; } 30% { opacity: 1; } }

    /* Visible to assistive technology, not on screen. */
    .sr {
      position: absolute;
      width: 1px;
      height: 1px;
      margin: -1px;
      padding: 0;
      overflow: hidden;
      clip: rect(0 0 0 0);
      clip-path: inset(50%);
      white-space: nowrap;
      border: 0;
    }
    .caret {
      display: inline-block;
      width: 2px;
      height: 1em;
      background: currentColor;
      vertical-align: text-bottom;
      margin-left: 1px;
      animation: blink 1s step-end infinite;
    }
  `;

  template() {
    const role = this.attr("role", "assistant");
    const state = this.attr("state", "sent");
    const author = this.attr("author");
    const time = this.attr("time");

    // A pending turn has no body yet, so the dots *are* the body.
    // Described by, never labelled by: an aria-label on the bubble *replaces*
    // the message text as its accessible name, so a screen reader would read
    // "Coach" instead of what Coach said.
    const status = statusFor(role, state, author);

    const body =
      state === "pending"
        ? `<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>`
        : `<slot></slot>${state === "streaming" ? `<span class="caret" aria-hidden="true"></span>` : ""}`;

    // Live only while text is arriving; a settled turn re-announcing itself on
    // every re-render would be noise.
    const live = state === "streaming" || state === "pending" ? `aria-live="polite"` : "";

    // One role, resolved by precedence: a system notice stays a note even when
    // it failed, and only a non-system failure is an alert. Emitting both
    // attributes left the browser to pick, which is not a decision to delegate.
    const aria =
      role === "system" ? `role="note"` : state === "error" ? `role="alert"` : "";

    return /* html */ `
      <div class="row" part="row">
        ${author || time
          ? `<div class="meta" part="meta">
              ${author ? `<span>${esc(author)}</span>` : ""}
              ${time ? `<time>${esc(time)}</time>` : ""}
            </div>`
          : ""}
        <div class="bubble" part="bubble" ${live} ${aria}
          ${status ? `aria-describedby="status"` : ""}>${body}${
            status ? `<span id="status" class="sr">${esc(status)}</span>` : ""
          }</div>
      </div>
    `;
  }
}

/** Extra context for a turn that is not simply sent — appended, not substituted. */
function statusFor(role, state, author) {
  const who = author || { user: "You", assistant: "Assistant", system: "System" }[role] || role;
  if (state === "pending") return `${who} is replying`;
  if (state === "error") return `${who}, failed to send`;
  return "";
}

define("ga-chat-message", GaChatMessage);
