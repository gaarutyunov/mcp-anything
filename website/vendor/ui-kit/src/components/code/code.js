import { GaElement, define, esc } from "../../core/base-element.js";

/**
 * `<ga-code>` — a copyable code / command block.
 *
 * The code text is the default slot. By default the block shows a copy button
 * that writes the text to the clipboard and briefly flips to a check mark:
 *
 *   <ga-code prompt="$">npm install @gaarutyunov/ui-kit</ga-code>
 *
 * When `href` is set the block renders as a link with a trailing ↗ external
 * arrow instead of a copy button (no clipboard action):
 *
 *   <ga-code href="https://example.com/install.sh">curl … | sh</ga-code>
 *
 * Attributes:
 *   prompt   optional leading glyph (e.g. "$", "›")
 *   href     render as an external link (↗) instead of a copy button
 *   target   (link) forwarded to the anchor
 *   rel      (link) forwarded to the anchor
 *
 * Events: `copy` with { text } detail when copied.
 */
export class GaCode extends GaElement {
  static observed = ["prompt", "href", "target", "rel"];

  static styles = /* css */ `
    :host { display: block; }
    .block {
      display: flex;
      align-items: center;
      gap: var(--ga-space-3, 12px);
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-sm, 14px);
      line-height: 1.5;
      color: var(--ga-fg, #ededed);
      text-decoration: none;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: 10px 12px;
    }
    a.block { transition: border-color var(--ga-transition, 0.18s ease),
      background var(--ga-transition, 0.18s ease); }
    a.block:hover { border-color: var(--ga-dim, #454545); background: var(--ga-bg-elev-hover, #1f1f1f); }
    a.block:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .prompt { color: var(--ga-muted, #878787); user-select: none; flex: none; }
    .text {
      flex: 1 1 auto;
      min-width: 0;
      overflow-x: auto;
      white-space: pre;
      -ms-overflow-style: none;
      scrollbar-width: none;
    }
    .text::-webkit-scrollbar { display: none; }
    .action {
      flex: none;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 28px; height: 28px;
      margin: -4px -2px -4px 0;
      color: var(--ga-muted, #878787);
      background: transparent;
      border: none;
      border-radius: var(--ga-radius, 6px);
      cursor: pointer;
      font: inherit;
      transition: color var(--ga-transition, 0.18s ease),
        background var(--ga-transition, 0.18s ease);
    }
    button.action:hover { color: var(--ga-fg, #ededed); background: var(--ga-bg-elev-hover, #1f1f1f); }
    button.action:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .action.copied { color: var(--ga-green, #00c758); }
    .arrow { flex: none; color: var(--ga-muted, #878787); }
    svg { display: block; }
  `;

  _pass(name, out = name) {
    return this.hasAttribute(name)
      ? ` ${out}="${esc(this.getAttribute(name))}"`
      : "";
  }

  template() {
    const href = this.attr("href");
    const prompt = this.attr("prompt");
    const promptEl = prompt ? `<span class="prompt" part="prompt" aria-hidden="true">${esc(prompt)}</span>` : "";
    const codeEl = `<code class="text" part="text"><slot></slot></code>`;

    if (href) {
      return /* html */ `
        <a class="block" part="block"
          href="${esc(href)}"${this._pass("target")}${this._pass("rel")}>
          ${promptEl}
          ${codeEl}
          <span class="arrow" part="arrow" aria-hidden="true">${ARROW}</span>
        </a>
      `;
    }
    return /* html */ `
      <div class="block" part="block">
        ${promptEl}
        ${codeEl}
        <button class="action" part="copy" type="button" aria-label="Copy to clipboard">
          <span class="ico">${COPY}</span>
        </button>
      </div>
    `;
  }

  render() {
    super.render();
    const btn = this.$("button.action");
    btn?.addEventListener("click", () => this._copy(btn));
  }

  _copy(btn) {
    const text = (this.textContent || "").trim();
    const done = () => {
      btn.classList.add("copied");
      btn.querySelector(".ico").innerHTML = CHECK;
      this.emit("copy", { text });
      clearTimeout(this._t);
      this._t = setTimeout(() => {
        btn.classList.remove("copied");
        const ico = btn.querySelector(".ico");
        if (ico) ico.innerHTML = COPY;
      }, 1500);
    };
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(() => {});
    } else {
      done();
    }
  }

  disconnectedCallback() {
    clearTimeout(this._t);
  }
}

const COPY = /* html */ `<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <rect x="9" y="9" width="13" height="13" rx="2"></rect>
  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>`;

const CHECK = /* html */ `<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
  <path d="M20 6 9 17l-5-5"></path></svg>`;

const ARROW = /* html */ `<svg width="14" height="14" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <path d="M7 17 17 7"></path><path d="M7 7h10v10"></path></svg>`;

define("ga-code", GaCode);
