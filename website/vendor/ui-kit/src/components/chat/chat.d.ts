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
    static observed: string[];
    _following: boolean;
    _observer: MutationObserver | null;
    disconnectedCallback(): void;
    _messageCount(): number;
    _onContentChanged(): void;
    _onScroll(): void;
    _scrollToLatest({ smooth }?: {
        smooth?: boolean | undefined;
    }): void;
    _syncJump(): void;
    /** Scroll to the newest message and resume following. */
    scrollToLatest(): void;
}
import { GaElement } from "../../core/base-element.js";
