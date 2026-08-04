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
    static observed: string[];
}
import { GaElement } from "../../core/base-element.js";
