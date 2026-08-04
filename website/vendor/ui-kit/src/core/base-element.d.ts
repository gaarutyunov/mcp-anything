/** Define a custom element once (no-op if already defined). */
export function define(tag: any, ctor: any): void;
/** Escape interpolated text destined for innerHTML. */
export function esc(value: any): string;
export class GaElement extends HTMLElement {
    /** Subclasses override these. */
    static styles: string;
    static observed: any[];
    static get observedAttributes(): any[];
    /** Lazily build (once per subclass) the combined reset + component CSS. */
    static get _css(): string | undefined;
    _mounted: boolean;
    connectedCallback(): void;
    attributeChangedCallback(): void;
    /** Subclasses implement this and return an HTML string. */
    template(): string;
    render(): void;
    /** Query inside the shadow root. */
    $(selector: any): any;
    /** Dispatch a composed, bubbling CustomEvent. */
    emit(name: any, detail: any): void;
    /** Boolean attribute reader. */
    hasFlag(name: any): boolean;
    /** Attribute reader with a default. */
    attr(name: any, fallback?: string): string;
}
