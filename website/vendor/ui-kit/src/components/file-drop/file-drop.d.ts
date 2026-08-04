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
    static observed: string[];
    _emit(fileList: any): void;
}
import { GaElement } from "../../core/base-element.js";
