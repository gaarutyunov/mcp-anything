/* =========================================================================
   GaElement — a tiny base class for the kit's Web Components.

   Zero dependencies. Provides:
     - a Shadow DOM root styled with a plain <style> element (works in every
       browser that supports Shadow DOM, including all Safari versions — no
       reliance on constructable stylesheets / adoptedStyleSheets),
     - attribute -> re-render reactivity,
     - small helpers (`$`, `emit`).

   Custom-element tag names are namespaced `ga-*`.

   Note on styling: earlier versions used `adoptedStyleSheets`. That API is
   only supported in Safari 16.4+ and can fail silently on older WebKit,
   leaving components unstyled with no console error. Injecting a <style> tag
   is universally supported, so we use that instead.
   ========================================================================= */

/** Shared reset applied to every component's shadow root. */
const RESET = /* css */ `
  :host {
    box-sizing: border-box;
    font-family: var(--ga-font-sans, ui-sans-serif, system-ui, sans-serif);
  }
  :host([hidden]) { display: none !important; }
  *, *::before, *::after { box-sizing: inherit; }
  @media (prefers-reduced-motion: reduce) {
    * { transition-duration: 0.001ms !important; animation-duration: 0.001ms !important; }
  }
`;

export class GaElement extends HTMLElement {
  /** Subclasses override these. */
  static styles = "";
  static observed = [];

  static get observedAttributes() {
    return this.observed;
  }

  /** Lazily build (once per subclass) the combined reset + component CSS. */
  static get _css() {
    if (!Object.prototype.hasOwnProperty.call(this, "_cssCache")) {
      this._cssCache = RESET + (this.styles || "");
    }
    return this._cssCache;
  }

  constructor() {
    super();
    this.attachShadow({ mode: "open", delegatesFocus: true });
    this._mounted = false;
  }

  connectedCallback() {
    this._mounted = true;
    this.render();
  }

  attributeChangedCallback() {
    if (this._mounted) this.render();
  }

  /** Subclasses implement this and return an HTML string. */
  template() {
    return "";
  }

  render() {
    this.shadowRoot.innerHTML =
      "<style>" + this.constructor._css + "</style>" + this.template();
  }

  /** Query inside the shadow root. */
  $(selector) {
    return this.shadowRoot.querySelector(selector);
  }

  /** Dispatch a composed, bubbling CustomEvent. */
  emit(name, detail) {
    this.dispatchEvent(
      new CustomEvent(name, { detail, bubbles: true, composed: true })
    );
  }

  /** Boolean attribute reader. */
  hasFlag(name) {
    return this.hasAttribute(name);
  }

  /** Attribute reader with a default. */
  attr(name, fallback = "") {
    return this.getAttribute(name) ?? fallback;
  }
}

/** Define a custom element once (no-op if already defined). */
export function define(tag, ctor) {
  if (!customElements.get(tag)) customElements.define(tag, ctor);
}

/** Escape interpolated text destined for innerHTML. */
export function esc(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
