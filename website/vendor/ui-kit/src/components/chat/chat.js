import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-chat>` — a scrollable transcript with a header, a composer footer, and
 * scroll-follow that knows when to stop.
 *
 *   <ga-chat empty-text="Ask the coach anything.">
 *     <span slot="header">Coach</span>
 *     <ga-chat-message role="user">Log 3x5 at 100kg</ga-chat-message>
 *     <ga-chat-message role="assistant">Logged.</ga-chat-message>
 *     <form slot="footer">…</form>
 *   </ga-chat>
 *
 * **Follow only while you are already at the bottom.** A transcript that yanks
 * itself down while you are reading history is the worst thing a chat UI does,
 * so following is conditional: new content pins to the newest message only when
 * the view is there already. Scroll up and it stops; a jump-to-latest button
 * appears saying newer messages are waiting, and activating it resumes
 * following. That button is a real `<button>` in the shadow root — focusable,
 * keyboard-activated and announced — not a decorative overlay.
 *
 * Attributes: empty-text, height (CSS length for the transcript).
 * Slots: `header`, (default) — the messages, `footer` — the composer.
 */
export class GaChat extends GaElement {
  static observed = ["empty-text", "height"];

  static styles = /* css */ `
    :host { display: block; }
    .shell {
      display: flex;
      flex-direction: column;
      min-height: 0;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1f1f1f);
      border-radius: var(--ga-radius-lg, 8px);
      overflow: hidden;
    }
    .header {
      flex: none;
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
      border-bottom: 1px solid var(--ga-border, #1f1f1f);
      padding: var(--ga-space-3, 12px) var(--ga-space-4, 16px);
    }
    .header.empty, .footer.empty { display: none; }

    .area { position: relative; }
    .log {
      height: var(--chat-height, 360px);
      overflow-y: auto;
      overscroll-behavior: contain;
      display: flex;
      flex-direction: column;
      gap: var(--ga-space-3, 12px);
      padding: var(--ga-space-4, 16px);
      scroll-behavior: smooth;
    }
    @media (prefers-reduced-motion: reduce) { .log { scroll-behavior: auto; } }

    .placeholder {
      margin: auto;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-muted, #878787);
      text-align: center;
    }
    .placeholder[hidden] { display: none; }

    .jump {
      position: absolute;
      left: 50%;
      bottom: var(--ga-space-3, 12px);
      transform: translateX(-50%);
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-family: inherit;
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 500;
      color: var(--ga-bg, #000);
      background: var(--ga-fg, #ededed);
      border: 0;
      border-radius: var(--ga-radius-full, 9999px);
      padding: 7px 13px;
      cursor: pointer;
      box-shadow: var(--ga-shadow, 0 8px 24px rgba(0, 0, 0, 0.4));
    }
    .jump[hidden] { display: none; }
    .jump:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .jump svg { width: 12px; height: 12px; }

    .footer {
      flex: none;
      border-top: 1px solid var(--ga-border, #1f1f1f);
      padding: var(--ga-space-3, 12px) var(--ga-space-4, 16px);
    }
  `;

  constructor() {
    super();
    // A fresh transcript starts at the newest message, so it starts following.
    this._following = true;
    this._observer = null;
  }

  template() {
    return /* html */ `
      <div class="shell" part="shell">
        <div class="header empty" part="header"><slot name="header"></slot></div>
        <div class="area">
          <div class="log" part="log" role="log" aria-live="polite" aria-relevant="additions"
            style="--chat-height:${esc(this.attr("height", "360px"))}" tabindex="0">
            <div class="placeholder" part="empty" hidden>${esc(this.attr("empty-text", "No messages yet."))}</div>
            <slot></slot>
          </div>
          <button class="jump" part="jump" type="button" hidden>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <path d="M8 3v10M3.5 8.5L8 13l4.5-4.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            Newer messages
          </button>
        </div>
        <div class="footer empty" part="footer"><slot name="footer"></slot></div>
      </div>
    `;
  }

  render() {
    super.render();
    const log = this.$(".log");
    const jump = this.$(".jump");
    if (!log || !jump) return;

    log.addEventListener("scroll", () => this._onScroll());
    jump.addEventListener("click", () => this.scrollToLatest());

    // `:empty` cannot see whether a slot has assigned nodes, so the header and
    // footer collapse on assignment rather than in CSS.
    this.shadowRoot.querySelectorAll("slot").forEach((slot) => {
      slot.addEventListener("slotchange", () => this._onContentChanged());
    });

    // Messages are light-DOM children: a *new* turn is a slotchange, but a
    // streaming turn only mutates text, which slotchange never sees. Watch the
    // subtree too, or the transcript stops following mid-answer.
    this._observer?.disconnect();
    this._observer = new MutationObserver(() => this._onContentChanged());
    // attributes too: a turn going pending → streaming → sent changes height
    // without adding a node or editing text, and the transcript has to follow
    // that as well.
    this._observer.observe(this, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
    });

    this._onContentChanged();
    // The first paint lands at the newest message without animating there.
    requestAnimationFrame(() => this._scrollToLatest({ smooth: false }));
  }

  disconnectedCallback() {
    this._observer?.disconnect();
  }

  _messageCount() {
    return [...this.children].filter((el) => el.slot !== "header" && el.slot !== "footer").length;
  }

  _onContentChanged() {
    const placeholder = this.$(".placeholder");
    if (placeholder) placeholder.hidden = this._messageCount() > 0;

    // Collapse the chrome that has nothing slotted into it.
    for (const name of ["header", "footer"]) {
      const slot = this.shadowRoot.querySelector(`slot[name="${name}"]`);
      const box = this.$(`.${name}`);
      if (slot && box) box.classList.toggle("empty", slot.assignedNodes().length === 0);
    }

    // Automatic follow is instant: a smooth scroll restarted on every token of
    // a streaming reply never catches up, and animates continuously while it
    // tries. Smooth is for the jump button, where it is a deliberate move.
    if (this._following) this._scrollToLatest({ smooth: false });
    this._syncJump();
  }

  _onScroll() {
    const log = this.$(".log");
    if (!log) return;
    // A few pixels of slack: momentum scrolling and sub-pixel heights mean
    // "at the bottom" is rarely exact.
    this._following = log.scrollHeight - log.scrollTop - log.clientHeight < 24;
    this._syncJump();
  }

  _scrollToLatest({ smooth = true } = {}) {
    const log = this.$(".log");
    if (!log) return;
    if (smooth) {
      log.scrollTop = log.scrollHeight;
      return;
    }
    const previous = log.style.scrollBehavior;
    log.style.scrollBehavior = "auto";
    log.scrollTop = log.scrollHeight;
    log.style.scrollBehavior = previous;
  }

  _syncJump() {
    const jump = this.$(".jump");
    if (!jump) return;
    jump.hidden = this._following || this._messageCount() === 0;
  }

  /** Scroll to the newest message and resume following. */
  scrollToLatest() {
    this._following = true;
    this._scrollToLatest();
    this._syncJump();
  }
}

define("ga-chat", GaChat);
