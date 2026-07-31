/**
 * Anchor `panel` to `anchor`.
 *
 * @param {HTMLElement} anchor  the trigger the panel hangs off
 * @param {HTMLElement} panel   the panel element, inside the same shadow root
 * @param {{ onDismiss?: (reason: string) => void }} [opts]
 *   onDismiss runs when the popup closes for a reason that is not an explicit
 *   `close()` — an outside click, Escape, or the anchor scrolling out of view.
 *   The caller decides what that means (restore focus, commit a value, …).
 */
export function createPopup(anchor: HTMLElement, panel: HTMLElement, opts?: {
    onDismiss?: (reason: string) => void;
}): {
    show: () => void;
    close: () => void;
    reposition: () => void;
    readonly open: boolean;
    /** Release listeners without invoking onDismiss — for disconnection. */
    destroy(): void;
};
/** Whether this browser has the Popover API. */
export const SUPPORTS_POPOVER: boolean;
