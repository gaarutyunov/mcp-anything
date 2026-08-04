import { GaElement, define } from "../../core/base-element.js";

/**
 * `<ga-bottom-sheet>` — a draggable sheet that rises from the bottom of the
 * screen with snap points, à la Google Maps.
 *
 * Drag the grab handle (or the header) between three detents — `peek`, `half`,
 * `full` — and drag below `peek` to dismiss. Persistent (no backdrop).
 *
 * Attributes:
 *   open   boolean — reflected; whether the sheet is visible
 *   snap   "peek" | "half" | "full"  (reflected; default "half")
 *
 * Slots: `header` (sits under the handle), default (scrollable body).
 * Methods: show(snap?) / close() / snapTo(snap).
 * Events: `open`, `close`, `snapchange` ({ snap }).
 */
export class GaBottomSheet extends GaElement {
  static observed = ["open", "snap"];

  static styles = /* css */ `
    :host { display: contents; }
    .sheet {
      position: fixed; left: 0; right: 0; bottom: 0; z-index: 50;
      width: min(560px, 100%); height: 88vh; margin: 0 auto;
      display: flex; flex-direction: column;
      background: var(--ga-bg, #000);
      border: 1px solid var(--ga-border, #1a1a1a); border-bottom: 0;
      border-radius: 16px 16px 0 0;
      box-shadow: 0 -16px 40px rgba(0, 0, 0, 0.4);
      transform: translateY(100%);
      transition: transform 0.32s cubic-bezier(0.4, 0, 0.2, 1);
      touch-action: none;
    }
    .sheet.dragging { transition: none; }

    .grip { flex: none; display: flex; justify-content: center; padding: 10px 0 6px; cursor: grab; }
    .grip:active { cursor: grabbing; }
    .bar { width: 40px; height: 5px; border-radius: 9999px; background: var(--ga-border-strong, #2a2a2a); }

    .head { flex: none; padding: 4px 20px 12px; color: var(--ga-fg, #ededed); cursor: grab; }
    .head:active { cursor: grabbing; }
    .head:empty { display: none; }

    .body { flex: 1; overflow-y: auto; padding: 0 20px 24px; color: var(--ga-muted, #878787); line-height: 1.55; }
  `;

  template() {
    return /* html */ `
      <div class="sheet" part="sheet">
        <div class="grip" part="handle"><span class="bar"></span></div>
        <div class="head" part="header"><slot name="header"></slot></div>
        <div class="body" part="body"><slot></slot></div>
      </div>
    `;
  }

  connectedCallback() {
    super.connectedCallback();
    this._onResize = () => this._apply();
    window.addEventListener("resize", this._onResize);
    this._onMove = (e) => this._move(e);
    this._onUp = () => this._up();
    window.addEventListener("pointermove", this._onMove);
    window.addEventListener("pointerup", this._onUp);
  }

  disconnectedCallback() {
    window.removeEventListener("resize", this._onResize);
    window.removeEventListener("pointermove", this._onMove);
    window.removeEventListener("pointerup", this._onUp);
  }

  // Reposition on attribute changes instead of re-rendering the DOM.
  attributeChangedCallback() {
    if (this._mounted) this._apply();
  }

  render() {
    super.render();
    const grip = this.$(".grip");
    const head = this.$(".head");
    for (const el of [grip, head]) el?.addEventListener("pointerdown", (e) => this._down(e));
    // Position after layout so offsetHeight is known.
    requestAnimationFrame(() => this._apply());
  }

  get open() { return this.hasFlag("open"); }
  get snap() { return this.attr("snap", "half"); }

  show(snap) { if (snap) this.setAttribute("snap", snap); this.setAttribute("open", ""); this._apply(); this.emit("open"); }
  close() { this.removeAttribute("open"); this._apply(); this.emit("close"); }
  snapTo(snap) { this.setAttribute("snap", snap); this._apply(); this.emit("snapchange", { snap }); }

  _snaps() {
    const h = this.$(".sheet")?.offsetHeight || window.innerHeight * 0.88;
    const vh = window.innerHeight;
    return { full: 0, half: Math.max(0, h - vh * 0.45), peek: Math.max(0, h - 128), closed: h };
  }

  _currentY() {
    const m = /translateY\(([-0-9.]+)px\)/.exec(this.$(".sheet")?.style.transform || "");
    return m ? parseFloat(m[1]) : this._snaps().closed;
  }

  _apply() {
    const sheet = this.$(".sheet");
    if (!sheet) return;
    const s = this._snaps();
    const y = this.open ? (s[this.snap] ?? s.half) : s.closed;
    sheet.style.transform = `translateY(${y}px)`;
  }

  _down(e) {
    this._dragging = true;
    this._startY = e.clientY;
    this._startTf = this._currentY();
    this.$(".sheet")?.classList.add("dragging");
  }

  _move(e) {
    if (!this._dragging) return;
    const s = this._snaps();
    const y = Math.min(s.closed, Math.max(0, this._startTf + (e.clientY - this._startY)));
    this.$(".sheet").style.transform = `translateY(${y}px)`;
  }

  _up() {
    if (!this._dragging) return;
    this._dragging = false;
    this.$(".sheet")?.classList.remove("dragging");
    const s = this._snaps();
    const y = this._currentY();
    if (y > s.peek + 80) { this.close(); return; }
    let best = "full";
    for (const name of ["full", "half", "peek"]) {
      if (Math.abs(s[name] - y) < Math.abs(s[best] - y)) best = name;
    }
    if (best !== this.snap) { this.setAttribute("snap", best); this.emit("snapchange", { snap: best }); }
    this._apply();
  }
}

define("ga-bottom-sheet", GaBottomSheet);
