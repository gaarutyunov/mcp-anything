/* =========================================================================
   popup — anchor a panel to a trigger.

   Internal plumbing shared by `ga-select` and `ga-date-input` (and, later,
   `ga-combobox`). NOT exported from index.js: it is not part of the kit's
   public surface, and nothing outside the kit should depend on its shape.

   Two things make an anchored panel hard, and this handles both:

     - **Clipping.** A panel inside a scroll container or a card with
       `overflow: hidden` gets cut off. The native Popover API solves it by
       promoting the panel to the *top layer*, above everything, ignoring
       ancestor overflow and z-index entirely. `popover="manual"` (rather than
       "auto") because dismissal is ours to drive: "auto" light-dismisses on
       any outside pointerdown, which would fight the trigger's own click.
     - **Position.** The top layer is positioned relative to the viewport, so
       the panel is `position: fixed` and placed from the anchor's
       `getBoundingClientRect()`, flipping above the anchor when there is more
       room there than below.

   Where `popover` is unsupported (Safari < 17, Firefox < 125), the panel falls
   back to `position: absolute` inside the host. It then *can* be clipped by an
   overflowing ancestor — the trade-off is accepted rather than hidden, because
   the alternative (reparenting the panel to <body>) breaks Shadow DOM styling
   and event retargeting.
   ========================================================================= */

/** Whether this browser has the Popover API. */
export const SUPPORTS_POPOVER =
  typeof HTMLElement !== "undefined" &&
  Object.prototype.hasOwnProperty.call(HTMLElement.prototype, "popover");

/** Gap between the anchor and the panel, in px. */
const OFFSET = 4;

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
export function createPopup(anchor, panel, opts = {}) {
  let open = false;
  const onDismiss = opts.onDismiss || (() => {});

  if (SUPPORTS_POPOVER) panel.setAttribute("popover", "manual");

  function position() {
    const rect = anchor.getBoundingClientRect();
    const panelHeight = panel.offsetHeight || 0;
    const below = window.innerHeight - rect.bottom;
    // Flip above only when below genuinely cannot hold the panel *and* above
    // has more room — otherwise a panel near the bottom of a tall viewport
    // would flip for no reason.
    const flip = below < panelHeight + OFFSET && rect.top > below;

    panel.style.minWidth = `${rect.width}px`;
    if (SUPPORTS_POPOVER) {
      panel.style.position = "fixed";
      panel.style.left = `${rect.left}px`;
      panel.style.top = flip ? "auto" : `${rect.bottom + OFFSET}px`;
      panel.style.bottom = flip ? `${window.innerHeight - rect.top + OFFSET}px` : "auto";
      panel.style.margin = "0";
    } else {
      // Absolute inside the host, which the component styles as the
      // positioning context.
      panel.style.position = "absolute";
      panel.style.left = "0";
      panel.style.top = flip ? "auto" : "100%";
      panel.style.bottom = flip ? "100%" : "auto";
      panel.style.marginTop = flip ? "0" : `${OFFSET}px`;
      panel.style.marginBottom = flip ? `${OFFSET}px` : "0";
    }
    panel.dataset.placement = flip ? "top" : "bottom";
  }

  function onDocumentPointerDown(e) {
    // composedPath sees through the shadow boundary, so a click on the panel
    // or the anchor is recognised even though both live in a shadow root.
    const path = e.composedPath();
    if (path.includes(panel) || path.includes(anchor)) return;
    dismiss("outside");
  }

  function onKeyDown(e) {
    if (e.key === "Escape") {
      e.stopPropagation();
      dismiss("escape");
    }
  }

  function onViewportChange() {
    const rect = anchor.getBoundingClientRect();
    const visible =
      rect.bottom > 0 &&
      rect.top < window.innerHeight &&
      rect.right > 0 &&
      rect.left < window.innerWidth;
    if (!visible) {
      dismiss("scroll");
      return;
    }
    position();
  }

  function listen(on) {
    const fn = on ? "addEventListener" : "removeEventListener";
    // Capture, so the panel closes before a click reaches an app handler.
    document[fn]("pointerdown", onDocumentPointerDown, true);
    document[fn]("keydown", onKeyDown, true);
    // Capture again for scroll: scroll does not bubble from inner containers.
    window[fn]("scroll", onViewportChange, true);
    window[fn]("resize", onViewportChange);
  }

  function show() {
    if (open) return;
    open = true;
    panel.hidden = false;
    if (SUPPORTS_POPOVER) {
      try {
        panel.showPopover();
      } catch {
        // Already open, or the panel is disconnected. Neither is fatal.
      }
    }
    position();
    listen(true);
  }

  function close() {
    if (!open) return;
    open = false;
    listen(false);
    if (SUPPORTS_POPOVER) {
      try {
        panel.hidePopover();
      } catch {
        /* already closed */
      }
    }
    panel.hidden = true;
  }

  function dismiss(reason) {
    close();
    onDismiss(reason);
  }

  return {
    show,
    close,
    reposition: position,
    get open() {
      return open;
    },
    /** Release listeners without invoking onDismiss — for disconnection. */
    destroy() {
      if (open) close();
    },
  };
}
