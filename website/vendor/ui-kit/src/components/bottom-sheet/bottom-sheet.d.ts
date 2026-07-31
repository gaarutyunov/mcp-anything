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
    static observed: string[];
    _onResize: (() => void) | undefined;
    _onMove: ((e: any) => void) | undefined;
    _onUp: (() => void) | undefined;
    disconnectedCallback(): void;
    get open(): boolean;
    get snap(): string;
    show(snap: any): void;
    close(): void;
    snapTo(snap: any): void;
    _snaps(): {
        full: number;
        half: number;
        peek: number;
        closed: any;
    };
    _currentY(): any;
    _apply(): void;
    _down(e: any): void;
    _dragging: boolean | undefined;
    _startY: any;
    _startTf: any;
    _move(e: any): void;
    _up(): void;
}
import { GaElement } from "../../core/base-element.js";
