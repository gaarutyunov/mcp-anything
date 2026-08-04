import { GaElement, define, esc } from "../../core/base-element.js";
import "../icon/icon.js";

/**
 * `<ga-file-drop>` — a drag-and-drop file upload area.
 *
 * Ported from stereoscope's `.drop`: a dashed surface that highlights with the
 * accent color while dragging over it. Click to browse or drop files.
 *
 * Attributes:
 *   accept    string — passed to the native file input
 *   multiple  boolean — allow multiple files
 *   label     string — primary prompt text
 *
 * Slot: default (secondary hint text).
 * Events: `files` with { files: File[] }.
 */
export class GaFileDrop extends GaElement {
  static observed = ["accept", "multiple", "label"];

  static styles = /* css */ `
    :host { display: block; }
    .drop {
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 8px;
      border: 1px dashed var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: 34px 18px;
      text-align: center;
      color: var(--ga-muted, #878787);
      cursor: pointer;
      background: var(--ga-bg-elev, #1a1a1a);
      transition: border-color 0.15s, background 0.15s, color 0.15s;
    }
    .drop:hover { background: var(--ga-bg-elev-hover, #1f1f1f); }
    .drop.dragging {
      border-color: var(--ga-accent, #54a2ff);
      color: var(--ga-fg, #ededed);
      background: color-mix(in srgb, var(--ga-accent, #54a2ff) 8%, transparent);
    }
    .icon { opacity: 0.85; }
    .label { font-size: var(--ga-fs-sm, 14px); }
    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-dim, #454545); }
    .hint:empty { display: none; }
    input { display: none; }
  `;

  template() {
    const label = this.attr("label", "Drop files here or click to browse");
    return /* html */ `
      <label class="drop" part="drop">
        <ga-icon class="icon" name="upload" size="24"></ga-icon>
        <span class="label">${esc(label)}</span>
        <span class="hint"><slot></slot></span>
        <input type="file" ${this.hasFlag("multiple") ? "multiple" : ""} accept="${esc(this.attr("accept"))}" />
      </label>
    `;
  }

  render() {
    super.render();
    const drop = this.$(".drop");
    const input = this.$("input");
    input.addEventListener("change", () => this._emit(input.files));
    ["dragenter", "dragover"].forEach((e) =>
      drop.addEventListener(e, (ev) => { ev.preventDefault(); drop.classList.add("dragging"); }));
    ["dragleave", "dragend", "drop"].forEach((e) =>
      drop.addEventListener(e, (ev) => { ev.preventDefault(); drop.classList.remove("dragging"); }));
    drop.addEventListener("drop", (ev) => {
      if (ev.dataTransfer?.files?.length) this._emit(ev.dataTransfer.files);
    });
  }

  _emit(fileList) {
    if (fileList && fileList.length) this.emit("files", { files: Array.from(fileList) });
  }
}

define("ga-file-drop", GaFileDrop);
