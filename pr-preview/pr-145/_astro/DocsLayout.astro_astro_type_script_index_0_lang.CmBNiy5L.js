const q=`
  :host {
    box-sizing: border-box;
    font-family: var(--ga-font-sans, ui-sans-serif, system-ui, sans-serif);
  }
  :host([hidden]) { display: none !important; }
  *, *::before, *::after { box-sizing: inherit; }
  @media (prefers-reduced-motion: reduce) {
    * { transition-duration: 0.001ms !important; animation-duration: 0.001ms !important; }
  }
`;class l extends HTMLElement{static styles="";static observed=[];static get observedAttributes(){return this.observed}static get _css(){return Object.prototype.hasOwnProperty.call(this,"_cssCache")||(this._cssCache=q+(this.styles||"")),this._cssCache}constructor(){super(),this.attachShadow({mode:"open",delegatesFocus:!0}),this._mounted=!1}connectedCallback(){this._mounted=!0,this.render()}attributeChangedCallback(){this._mounted&&this.render()}template(){return""}render(){this.shadowRoot.innerHTML="<style>"+this.constructor._css+"</style>"+this.template()}$(t){return this.shadowRoot.querySelector(t)}emit(t,e){this.dispatchEvent(new CustomEvent(t,{detail:e,bubbles:!0,composed:!0}))}hasFlag(t){return this.hasAttribute(t)}attr(t,e=""){return this.getAttribute(t)??e}}function d(i,t){customElements.get(i)||customElements.define(i,t)}function o(i){return String(i??"").replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;")}class R extends l{static observed=["variant","size","href","download","target","rel","type","name","aria-label","disabled","loading","block"];static styles=`
    :host { display: inline-block; }
    :host([block]) { display: block; }

    .btn {
      --_bg: var(--ga-bg-elev, #1a1a1a);
      --_fg: var(--ga-fg, #ededed);
      --_bd: var(--ga-border-strong, #2a2a2a);
      --_bg-hover: var(--ga-bg-elev-hover, #1f1f1f);

      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: var(--ga-space-2, 8px);
      width: 100%;
      font-family: inherit;
      font-weight: 500;
      line-height: 1;
      white-space: nowrap;
      text-decoration: none;
      cursor: pointer;
      border: 1px solid var(--_bd);
      border-radius: var(--ga-radius, 6px);
      background: var(--_bg);
      color: var(--_fg);
      transition: background var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease),
        filter var(--ga-transition, 0.18s ease),
        transform var(--ga-transition, 0.18s ease);
    }
    .btn:hover { background: var(--_bg-hover); }
    .btn:active { transform: translateY(1px); }
    .btn:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }

    /* sizes */
    :host([size="sm"]) .btn { font-size: var(--ga-fs-sm, 14px); padding: 6px 12px; height: 32px; }
    .btn { font-size: var(--ga-fs-sm, 14px); padding: 8px 16px; height: 40px; }
    :host([size="lg"]) .btn { font-size: var(--ga-fs-base, 17px); padding: 12px 22px; height: 48px; }

    /* variants */
    :host([variant="primary"]) .btn {
      --_bg: var(--ga-accent, #54a2ff);
      --_fg: var(--ga-accent-contrast, #000);
      --_bd: var(--ga-accent, #54a2ff);
    }
    :host([variant="primary"]) .btn:hover { background: var(--ga-accent, #54a2ff); filter: brightness(1.1); }

    :host([variant="ghost"]) .btn {
      --_bg: transparent;
      --_bd: transparent;
    }
    :host([variant="ghost"]) .btn:hover { background: var(--ga-bg-elev, #1a1a1a); }

    :host([variant="danger"]) .btn {
      --_bg: transparent;
      --_fg: var(--ga-red, #ff6568);
      --_bd: color-mix(in srgb, var(--ga-red, #ff6568) 40%, transparent);
    }
    :host([variant="danger"]) .btn:hover {
      background: color-mix(in srgb, var(--ga-red, #ff6568) 12%, transparent);
    }

    :host([disabled]) .btn,
    :host([loading]) .btn {
      opacity: 0.5;
      pointer-events: none;
      cursor: not-allowed;
    }

    .spinner {
      width: 1em; height: 1em;
      border: 2px solid currentColor;
      border-right-color: transparent;
      border-radius: 50%;
      animation: spin 0.6s linear infinite;
    }
    @keyframes spin { to { transform: rotate(360deg); } }

    ::slotted([slot="start"]), ::slotted([slot="end"]) { display: inline-flex; }
  `;connectedCallback(){super.connectedCallback(),this.addEventListener("click",this._guard,!0)}disconnectedCallback(){this.removeEventListener("click",this._guard,!0)}_guard=t=>{(this.hasFlag("disabled")||this.hasFlag("loading"))&&(t.stopImmediatePropagation(),t.preventDefault())};_pass(t,e=t){return this.hasAttribute(t)?` ${e}="${o(this.getAttribute(t))}"`:""}template(){const t=this.attr("href"),e=t?"a":"button",a=this._pass("aria-label"),r=t?`href="${o(t)}"`+this._pass("download")+this._pass("target")+this._pass("rel")+a:`type="${o(this.attr("type","button"))}"`+this._pass("name")+a+(this.hasFlag("disabled")?" disabled":""),s=this.hasFlag("loading")?'<span class="spinner" aria-hidden="true"></span>':"";return`
      <${e} class="btn" part="button" ${r}>
        <slot name="start"></slot>
        ${s}
        <slot></slot>
        <slot name="end"></slot>
      </${e}>
    `}}d("ga-button",R);class N extends l{static formAssociated=!0;static observed=["items","value"];static styles=`
    :host { display: inline-block; }
    .group {
      display: inline-flex;
      gap: var(--ga-space-1, 4px);
      padding: 3px;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-full, 9999px);
    }
    .item {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: var(--ga-space-2, 8px);
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      line-height: 1;
      white-space: nowrap;
      text-decoration: none;
      padding: 7px 16px;
      border: 1px solid transparent;
      border-radius: var(--ga-radius-full, 9999px);
      background: transparent;
      color: var(--ga-muted, #878787);
      cursor: pointer;
      transition: background var(--ga-transition, 0.18s ease),
        color var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease);
    }
    .item:hover { color: var(--ga-fg, #ededed); }
    .item[aria-checked="true"],
    .item[aria-current="page"] {
      background: var(--ga-fg, #ededed);
      color: var(--ga-bg, #000);
      border-color: var(--ga-fg, #ededed);
    }
    .item:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
  `;constructor(){super(),this._internals=this.attachInternals?.()}_parse(){try{return JSON.parse(this.attr("items","[]"))}catch{return[]}}template(){const t=this._parse(),e=this.attr("value")||t[0]?.id;return`<div class="group" part="group" role="radiogroup">${t.map(r=>{const s=r.id===e,n=s?"0":"-1";return r.href?`<a class="item" part="item" data-id="${o(r.id)}"
          href="${o(r.href)}" role="radio"
          aria-checked="${s}"
          ${s?'aria-current="page"':""}
          tabindex="${n}">${o(r.label)}</a>`:`<button class="item" part="item" type="button" data-id="${o(r.id)}"
        role="radio" aria-checked="${s}" tabindex="${n}">${o(r.label)}</button>`}).join("")}</div>`}render(){super.render(),this._internals?.setFormValue(this.value);const t=[...this.shadowRoot.querySelectorAll(".item")];t.forEach(e=>{e.tagName==="BUTTON"&&e.addEventListener("click",()=>this._select(e.dataset.id)),e.addEventListener("keydown",a=>this._onKey(a,t))})}_onKey(t,e){const a={ArrowRight:1,ArrowDown:1,ArrowLeft:-1,ArrowUp:-1}[t.key];if(!a)return;t.preventDefault();const r=e.indexOf(t.currentTarget),s=e[(r+a+e.length)%e.length];s&&(s.focus(),s.tagName==="BUTTON"&&this._select(s.dataset.id))}_select(t){t==null||t===this.attr("value")||(this.setAttribute("value",t),this.emit("change",{value:t}))}get value(){return this.attr("value")||this._parse()[0]?.id||""}set value(t){this.setAttribute("value",t)}}d("ga-radio-group",N);class H extends l{static observed=["prompt","href","target","rel"];static styles=`
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
  `;_pass(t,e=t){return this.hasAttribute(t)?` ${e}="${o(this.getAttribute(t))}"`:""}template(){const t=this.attr("href"),e=this.attr("prompt"),a=e?`<span class="prompt" part="prompt" aria-hidden="true">${o(e)}</span>`:"",r='<code class="text" part="text"><slot></slot></code>';return t?`
        <a class="block" part="block"
          href="${o(t)}"${this._pass("target")}${this._pass("rel")}>
          ${a}
          ${r}
          <span class="arrow" part="arrow" aria-hidden="true">${I}</span>
        </a>
      `:`
      <div class="block" part="block">
        ${a}
        ${r}
        <button class="action" part="copy" type="button" aria-label="Copy to clipboard">
          <span class="ico">${D}</span>
        </button>
      </div>
    `}render(){super.render();const t=this.$("button.action");t?.addEventListener("click",()=>this._copy(t))}_copy(t){const e=(this.textContent||"").trim(),a=()=>{t.classList.add("copied"),t.querySelector(".ico").innerHTML=U,this.emit("copy",{text:e}),clearTimeout(this._t),this._t=setTimeout(()=>{t.classList.remove("copied");const r=t.querySelector(".ico");r&&(r.innerHTML=D)},1500)};navigator.clipboard?.writeText?navigator.clipboard.writeText(e).then(a).catch(()=>{}):a()}disconnectedCallback(){clearTimeout(this._t)}}const D=`<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <rect x="9" y="9" width="13" height="13" rx="2"></rect>
  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>`,U=`<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
  <path d="M20 6 9 17l-5-5"></path></svg>`,I=`<svg width="14" height="14" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <path d="M7 17 17 7"></path><path d="M7 7h10v10"></path></svg>`;d("ga-code",H);class P extends l{static observed=["items"];static styles=`
    :host { display: block; }
    ol {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: var(--ga-space-2, 8px);
      margin: 0;
      padding: 0;
      list-style: none;
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-sm, 14px);
    }
    li { display: inline-flex; align-items: center; gap: var(--ga-space-2, 8px); }
    a {
      color: var(--ga-muted, #878787);
      text-decoration: none;
      transition: color var(--ga-transition, 0.18s ease);
    }
    a:hover { color: var(--ga-fg, #ededed); }
    a:focus-visible {
      outline: none;
      border-radius: var(--ga-radius, 6px);
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .current { color: var(--ga-fg, #ededed); }
    .sep { color: var(--ga-dim, #454545); user-select: none; }
  `;_parse(){try{return JSON.parse(this.attr("items","[]"))}catch{return[]}}template(){const t=this._parse(),e=t.length-1;return`<nav aria-label="Breadcrumb" part="nav"><ol part="list">${t.map((r,s)=>{const n=s>0?'<span class="sep" part="separator" aria-hidden="true">/</span>':"",p=s===e,h=o(r.label),v=!p&&r.href?`<a part="link" href="${o(r.href)}">${h}</a>`:`<span class="current" part="current" ${p?'aria-current="page"':""}>${h}</span>`;return`<li>${n}${v}</li>`}).join("")}</ol></nav>`}}d("ga-breadcrumbs",P);let G=0;class B extends l{static observed=["columns"];static styles=`
    :host { display: block; }
    .table {
      border: 1px solid var(--ga-border, #1a1a1a);
      border-radius: var(--ga-radius-lg, 8px);
      overflow: hidden;
    }
    .head {
      display: grid;
      grid-template-columns: var(--ga-table-cols, 1fr);
      align-items: center;
      gap: var(--ga-space-4, 16px);
      padding: 10px var(--ga-space-4, 16px);
      background: color-mix(in srgb, var(--ga-bg-elev, #1a1a1a) 40%, transparent);
      border-bottom: 1px solid var(--ga-border, #1a1a1a);
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 600;
      letter-spacing: 0.02em;
      text-transform: uppercase;
      color: var(--ga-muted, #878787);
    }
    .head .mono { font-family: var(--ga-font-mono, ui-monospace, monospace); text-transform: none; }

    /* Slotted rows share the column grid and get borders + hover. */
    ::slotted(*) {
      display: grid !important;
      grid-template-columns: var(--ga-table-cols, 1fr);
      align-items: center;
      gap: var(--ga-space-4, 16px);
      padding: var(--ga-space-3, 12px) var(--ga-space-4, 16px);
      border-top: 1px solid var(--ga-border, #1a1a1a);
      color: var(--ga-fg, #ededed);
      text-decoration: none;
      transition: background var(--ga-transition, 0.18s ease);
    }
    ::slotted(:first-child) { border-top: none; }
    ::slotted(*:hover) { background: var(--ga-bg-elev-hover, #1f1f1f); }
    ::slotted(a) { cursor: pointer; }
    ::slotted(a:focus-visible) {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
  `;connectedCallback(){this._scope="ga-table-"+G++,this.setAttribute("data-ga-scope",this._scope),super.connectedCallback()}disconnectedCallback(){this._sheet?.remove(),this._sheet=null}_parse(){try{return JSON.parse(this.attr("columns","[]"))}catch{return[]}}template(){const t=this._parse(),e=t.map(r=>r.width||"minmax(0, 1fr)").join(" ")||"1fr";this.style.setProperty("--ga-table-cols",e);const a=t.map(r=>{const s="cell"+(r.mono?" mono":""),n=r.align?`text-align:${o(r.align)}`:"";return`<span class="${s}" part="head-cell" style="${n}">${o(r.label??"")}</span>`}).join("");return this._applyCellRules(t),`
      <div class="table" part="table">
        <div class="head" part="header" role="row">${a}</div>
        <slot part="body"></slot>
      </div>
    `}_applyCellRules(t){const e=t.map((a,r)=>{const s=[];return a.align&&s.push(`text-align:${a.align}`),a.mono&&s.push("font-family:var(--ga-font-mono, ui-monospace, monospace);font-variant-numeric:tabular-nums"),s.length?`ga-table[data-ga-scope="${this._scope}"] > *:not([slot]) > :nth-child(${r+1}){${s.join(";")}}`:""}).filter(Boolean).join(`
`);this._sheet||(this._sheet=document.createElement("style"),document.head.appendChild(this._sheet)),this._sheet.textContent=e}}d("ga-table",B);class V extends l{static observed=["color","solid","size"];static styles=`
    :host { display: inline-block; }
    .badge {
      --_c: var(--ga-muted, #878787);
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-family: var(--ga-font-sans, ui-sans-serif, system-ui, sans-serif);
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 500;
      line-height: 1;
      padding: 2px 10px;
      border-radius: var(--ga-radius-full, 9999px);
      border: 1px solid var(--ga-border, #1a1a1a);
      color: var(--_c);
      background: transparent;
      white-space: nowrap;
    }
    :host([size="sm"]) .badge { font-size: 11px; padding: 1px 8px; }

    /* Colored variants: tint the text + border, keep the fill subtle. */
    :host([color="blue"])   .badge { --_c: var(--ga-blue, #54a2ff); }
    :host([color="green"])  .badge { --_c: var(--ga-green, #00c758); }
    :host([color="amber"])  .badge { --_c: var(--ga-amber, #fcbb00); }
    :host([color="purple"]) .badge { --_c: var(--ga-purple, #ac4bff); }
    :host([color="red"])    .badge { --_c: var(--ga-red, #ff6568); }
    :host([color="blue"])   .badge,
    :host([color="green"])  .badge,
    :host([color="amber"])  .badge,
    :host([color="purple"]) .badge,
    :host([color="red"])    .badge {
      border-color: color-mix(in srgb, var(--_c) 40%, transparent);
    }

    :host([solid]) .badge {
      background: var(--_c);
      color: var(--ga-accent-contrast, #000);
      border-color: var(--_c);
    }
  `;template(){return'<span class="badge" part="badge"><slot></slot></span>'}}d("ga-badge",V);class Y extends l{static observed=["interactive","href","padding"];static styles=`
    :host { display: block; }
    .card {
      display: flex;
      flex-direction: column;
      gap: var(--ga-space-3, 12px);
      color: var(--ga-fg, #ededed);
      text-decoration: none;
      background: color-mix(in srgb, var(--ga-bg-elev, #1a1a1a) 30%, transparent);
      border: 1px solid var(--ga-border, #1a1a1a);
      border-radius: var(--ga-radius-lg, 8px);
      overflow: hidden;
      transition: background var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease);
    }
    :host([interactive]) .card,
    :host([href]) .card { cursor: pointer; }
    :host([interactive]) .card:hover,
    :host([href]) .card:hover {
      background: color-mix(in srgb, var(--ga-bg-elev, #1a1a1a) 60%, transparent);
      border-color: var(--ga-dim, #454545);
    }
    :host([href]) .card:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }

    /* Slotted title turns accent-blue on hover (like the project cards). */
    ::slotted(h3), ::slotted(strong) { transition: color var(--ga-transition, 0.18s ease); }
    :host([interactive]) .card:hover ::slotted(h3),
    :host([interactive]) .card:hover ::slotted(strong),
    :host([href]) .card:hover ::slotted(h3),
    :host([href]) .card:hover ::slotted(strong) { color: var(--ga-accent, #54a2ff); }

    .body { padding: var(--ga-space-5, 20px); }
    :host([padding="none"]) .body { padding: 0; }
    :host([padding="sm"]) .body { padding: var(--ga-space-3, 12px); }
    :host([padding="lg"]) .body { padding: var(--ga-space-8, 32px); }

    .header, .footer { display: none; }
    .header.show, .footer.show { display: block; }
    .header {
      padding: var(--ga-space-4, 16px) var(--ga-space-5, 20px);
      border-bottom: 1px solid var(--ga-border, #1a1a1a);
      font-weight: 600;
    }
    .footer {
      padding: var(--ga-space-4, 16px) var(--ga-space-5, 20px);
      border-top: 1px solid var(--ga-border, #1a1a1a);
      color: var(--ga-muted, #878787);
      font-size: var(--ga-fs-sm, 14px);
    }
    /* Collapse the gap when only the body is present. */
    .card:not(:has(.header.show)):not(:has(.footer.show)) { gap: 0; }
  `;connectedCallback(){super.connectedCallback(),this._sync=()=>this._toggleSlots(),this.shadowRoot.addEventListener("slotchange",this._sync)}_toggleSlots(){for(const t of["header","footer"]){const e=this.$(`slot[name="${t}"]`),a=this.$(`.${t}`);e&&a&&a.classList.toggle("show",e.assignedNodes().length>0)}}template(){const t=this.attr("href"),e=t?"a":"div",a=t?`href="${t}"`:"";return`
      <${e} class="card" part="card" ${a}>
        <div class="header" part="header"><slot name="header"></slot></div>
        <div class="body" part="body"><slot></slot></div>
        <div class="footer" part="footer"><slot name="footer"></slot></div>
      </${e}>
    `}}d("ga-card",Y);class K extends l{static observed=["src","name","size","square"];static styles=`
    :host { display: inline-block; }
    .avatar {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 40px; height: 40px;
      overflow: hidden;
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-full, 9999px);
      user-select: none;
    }
    :host([square]) .avatar { border-radius: var(--ga-radius, 6px); }
    :host([size="sm"]) .avatar { width: 28px; height: 28px; font-size: 11px; }
    :host([size="lg"]) .avatar { width: 64px; height: 64px; font-size: var(--ga-fs-lg, 20px); }
    img { width: 100%; height: 100%; object-fit: cover; display: block; }
  `;_initials(t){return t.trim().split(/\s+/).slice(0,2).map(a=>a[0]?.toUpperCase()??"").join("")||"?"}template(){const t=this.attr("src"),e=this.attr("name",""),a=t?`<img src="${o(t)}" alt="${o(e)}" loading="lazy" />`:`<span aria-hidden="true">${o(this._initials(e))}</span>`;return`<div class="avatar" part="avatar" role="img" aria-label="${o(e)}">${a}</div>`}}d("ga-avatar",K);class J extends l{static formAssociated=!0;static observed=["label","placeholder","type","value","name","hint","error","disabled","required"];static styles=`
    :host { display: block; }
    .field { display: flex; flex-direction: column; gap: 6px; }
    label {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      color: var(--ga-fg, #ededed);
    }
    .req { color: var(--ga-red, #ff6568); margin-left: 2px; }
    input {
      width: 100%;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: 10px 12px;
      transition: border-color var(--ga-transition, 0.18s ease),
        box-shadow var(--ga-transition, 0.18s ease);
    }
    input::placeholder { color: var(--ga-dim, #454545); }
    input:hover { border-color: var(--ga-muted, #878787); }
    input:focus {
      outline: none;
      border-color: var(--ga-accent, #54a2ff);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--ga-accent, #54a2ff) 25%, transparent);
    }
    :host([disabled]) input { opacity: 0.5; cursor: not-allowed; }
    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-muted, #878787); }
    .error { font-size: var(--ga-fs-xs, 12px); color: var(--ga-red, #ff6568); }
    :host([error]) input { border-color: var(--ga-red, #ff6568); }
  `;constructor(){super(),this._internals=this.attachInternals?.()}template(){const t=this.attr("label"),e=this.attr("error"),a=this.attr("hint"),r=this.hasFlag("required")?'<span class="req">*</span>':"";return`
      <div class="field">
        ${t?`<label part="label">${o(t)}${r}</label>`:""}
        <input
          part="input"
          type="${o(this.attr("type","text"))}"
          placeholder="${o(this.attr("placeholder"))}"
          value="${o(this.attr("value"))}"
          ${this.hasFlag("disabled")?"disabled":""}
          ${this.hasFlag("required")?"required":""}
          aria-invalid="${e?"true":"false"}"
        />
        ${e?`<span class="error" part="error">${o(e)}</span>`:a?`<span class="hint" part="hint">${o(a)}</span>`:""}
      </div>
    `}render(){super.render();const t=this.$("input");t&&(t.addEventListener("input",()=>{this._value=t.value,this._internals?.setFormValue(t.value),this.emit("input",{value:t.value})}),t.addEventListener("change",()=>this.emit("change",{value:t.value})))}get value(){return this.$("input")?.value??this._value??this.attr("value")}set value(t){this._value=t,this.setAttribute("value",t)}}d("ga-input",J);class W extends l{static formAssociated=!0;static observed=["checked","disabled","label"];static styles=`
    :host { display: inline-block; }
    .wrap {
      display: inline-flex;
      align-items: center;
      gap: var(--ga-space-3, 12px);
      cursor: pointer;
      user-select: none;
    }
    :host([disabled]) .wrap { opacity: 0.5; cursor: not-allowed; }
    button {
      position: relative;
      flex: none;
      width: 40px; height: 24px;
      padding: 0;
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-full, 9999px);
      background: var(--ga-bg-elev, #1a1a1a);
      cursor: inherit;
      transition: background var(--ga-transition, 0.18s ease),
        border-color var(--ga-transition, 0.18s ease);
    }
    button:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .knob {
      position: absolute;
      top: 2px; left: 2px;
      width: 18px; height: 18px;
      border-radius: 50%;
      background: var(--ga-muted, #878787);
      transition: transform var(--ga-transition, 0.18s ease),
        background var(--ga-transition, 0.18s ease);
    }
    :host([checked]) button {
      background: var(--ga-accent, #54a2ff);
      border-color: var(--ga-accent, #54a2ff);
    }
    :host([checked]) .knob {
      transform: translateX(16px);
      background: var(--ga-accent-contrast, #000);
    }
    .label { font-size: var(--ga-fs-sm, 14px); color: var(--ga-fg, #ededed); }
  `;template(){const t=this.hasFlag("checked"),e=this.attr("label");return`
      <label class="wrap">
        <button
          part="track"
          type="button"
          role="switch"
          aria-checked="${t}"
          ${this.hasFlag("disabled")?"disabled":""}
        ><span class="knob" part="knob"></span></button>
        ${e?`<span class="label">${o(e)}</span>`:""}
      </label>
    `}render(){super.render(),this.$("button")?.addEventListener("click",()=>this.toggle())}toggle(){if(this.hasFlag("disabled"))return;const t=!this.hasFlag("checked");this.toggleAttribute("checked",t),this.emit("change",{checked:t})}get checked(){return this.hasFlag("checked")}set checked(t){this.toggleAttribute("checked",!!t)}}d("ga-switch",W);class X extends l{static observed=["size","color"];static styles=`
    :host { display: inline-flex; }
    .spinner {
      width: 20px; height: 20px;
      border: 2px solid color-mix(in srgb, currentColor 25%, transparent);
      border-top-color: currentColor;
      border-radius: 50%;
      color: var(--ga-accent, #54a2ff);
      animation: spin 0.7s linear infinite;
    }
    :host([size="sm"]) .spinner { width: 14px; height: 14px; }
    :host([size="lg"]) .spinner { width: 32px; height: 32px; border-width: 3px; }
    :host([color="green"])  .spinner { color: var(--ga-green, #00c758); }
    :host([color="amber"])  .spinner { color: var(--ga-amber, #fcbb00); }
    :host([color="purple"]) .spinner { color: var(--ga-purple, #ac4bff); }
    :host([color="red"])    .spinner { color: var(--ga-red, #ff6568); }
    :host([color="fg"])     .spinner { color: var(--ga-fg, #ededed); }
    @keyframes spin { to { transform: rotate(360deg); } }
  `;template(){return'<div class="spinner" part="spinner" role="status" aria-label="Loading"></div>'}}d("ga-spinner",X);class Z extends l{static observed=["tone","title","dismissible"];static styles=`
    :host { display: block; }
    .alert {
      --_c: var(--ga-muted, #878787);
      display: flex;
      gap: var(--ga-space-3, 12px);
      padding: var(--ga-space-4, 16px);
      border: 1px solid color-mix(in srgb, var(--_c) 35%, transparent);
      border-left-width: 3px;
      border-radius: var(--ga-radius, 6px);
      background: color-mix(in srgb, var(--_c) 8%, transparent);
      color: var(--ga-fg, #ededed);
      font-size: var(--ga-fs-sm, 14px);
      line-height: 1.5;
    }
    :host([tone="info"])    .alert { --_c: var(--ga-blue, #54a2ff); }
    :host([tone="success"]) .alert { --_c: var(--ga-green, #00c758); }
    :host([tone="warning"]) .alert { --_c: var(--ga-amber, #fcbb00); }
    :host([tone="danger"])  .alert { --_c: var(--ga-red, #ff6568); }

    .dot { flex: none; width: 8px; height: 8px; margin-top: 6px; border-radius: 50%; background: var(--_c); }
    .content { flex: 1; min-width: 0; }
    .title { font-weight: 600; color: var(--_c); margin-bottom: 2px; }
    .close {
      flex: none;
      background: none; border: none; cursor: pointer;
      color: var(--ga-muted, #878787);
      font-size: 18px; line-height: 1; padding: 0 4px;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .close:hover { color: var(--ga-fg, #ededed); }
  `;template(){const t=this.attr("title"),e=this.hasFlag("dismissible")?'<button class="close" part="close" aria-label="Dismiss">&times;</button>':"";return`
      <div class="alert" part="alert" role="alert">
        <span class="dot" aria-hidden="true"></span>
        <div class="content">
          ${t?`<div class="title" part="title">${o(t)}</div>`:""}
          <slot></slot>
        </div>
        ${e}
      </div>
    `}render(){super.render(),this.$(".close")?.addEventListener("click",()=>{this.emit("dismiss"),this.remove()})}}d("ga-alert",Z);class Q extends l{static styles=`
    :host { display: inline-block; }
    kbd {
      display: inline-block;
      font-family: var(--ga-font-mono, ui-monospace, monospace);
      font-size: var(--ga-fs-xs, 12px);
      line-height: 1;
      color: var(--ga-muted, #878787);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-bottom-width: 2px;
      border-radius: var(--ga-radius, 6px);
      padding: 4px 7px;
      min-width: 1em;
      text-align: center;
    }
  `;template(){return'<kbd part="kbd"><slot></slot></kbd>'}}d("ga-kbd",Q);class tt extends l{static observed=["tabs","active"];static styles=`
    :host { display: block; }
    .list {
      display: flex;
      gap: var(--ga-space-1, 4px);
      border-bottom: 1px solid var(--ga-border, #1a1a1a);
    }
    .tab {
      position: relative;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      color: var(--ga-muted, #878787);
      background: none;
      border: none;
      padding: 10px 14px;
      cursor: pointer;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .tab:hover { color: var(--ga-fg, #ededed); }
    .tab[aria-selected="true"] { color: var(--ga-fg, #ededed); }
    .tab[aria-selected="true"]::after {
      content: "";
      position: absolute;
      left: 8px; right: 8px; bottom: -1px;
      height: 2px;
      background: var(--ga-accent, #54a2ff);
      border-radius: 2px;
    }
    .tab:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
      border-radius: var(--ga-radius, 6px);
    }
    .panels { padding-top: var(--ga-space-4, 16px); }
  `;_parse(){try{return JSON.parse(this.attr("tabs","[]"))}catch{return[]}}template(){const t=this._parse(),e=this.attr("active")||t[0]?.id,a=t.map(s=>`
      <button class="tab" part="tab" role="tab" data-id="${o(s.id)}"
        aria-selected="${s.id===e}" tabindex="${s.id===e?"0":"-1"}">
        ${o(s.label)}
      </button>`).join(""),r=t.map(s=>`
      <div role="tabpanel" ${s.id===e?"":"hidden"}>
        <slot name="${o(s.id)}"></slot>
      </div>`).join("");return`
      <div class="list" part="list" role="tablist">${a}</div>
      <div class="panels" part="panels">${r}</div>
    `}render(){super.render(),this.shadowRoot.querySelectorAll(".tab").forEach(t=>{t.addEventListener("click",()=>this._select(t.dataset.id))})}_select(t){t!==this.attr("active")&&(this.setAttribute("active",t),this.emit("change",{id:t}))}}d("ga-tabs",tt);class et extends l{static observed=["tone","title"];static styles=`
    :host { display: block; }
    .note {
      --_c: var(--ga-accent, #54a2ff);
      margin: 0;
      padding: 14px 16px;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1a1a1a);
      border-left: 3px solid var(--_c);
      border-radius: var(--ga-radius, 6px);
      font-size: 15px;
      line-height: 1.55;
      color: var(--ga-muted, #878787);
    }
    :host([tone="neutral"]) .note { --_c: var(--ga-dim, #454545); }
    :host([tone="info"])    .note { --_c: var(--ga-blue, #54a2ff); }
    :host([tone="success"]) .note { --_c: var(--ga-green, #00c758); }
    :host([tone="warning"]) .note { --_c: var(--ga-amber, #fcbb00); }
    :host([tone="error"])   .note,
    :host([tone="danger"])  .note { --_c: var(--ga-red, #ff6568); }

    .title { margin: 0 0 3px; font-size: 14px; font-weight: 600; color: var(--ga-fg, #ededed); }
  `;template(){const t=this.attr("title");return`
      <div class="note" part="note">
        ${t?`<div class="title" part="title">${o(t)}</div>`:""}
        <slot></slot>
      </div>
    `}}d("ga-note",et);const at={compass:'<circle cx="12" cy="12" r="10"/><polygon points="16.24 7.76 14.12 14.12 7.76 16.24 9.88 9.88 16.24 7.76"/>',bookmark:'<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>',star:'<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>',heart:'<path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>',plus:'<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>',minus:'<line x1="5" y1="12" x2="19" y2="12"/>',check:'<polyline points="20 6 9 17 4 12"/>',x:'<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>',trash:'<polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',bell:'<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>',user:'<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',home:'<path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/>',search:'<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>',settings:'<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',menu:'<line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="18" x2="21" y2="18"/>',"chevron-right":'<polyline points="9 18 15 12 9 6"/>',"chevron-down":'<polyline points="6 9 12 15 18 9"/>',"arrow-right":'<line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/>',"external-link":'<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>',upload:'<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>',download:'<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>',image:'<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>',info:'<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>',layers:'<polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/>',sun:'<circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>',moon:'<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>',github:'<path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"/>'};class rt extends l{static observed=["name","size"];static styles=`
    :host { display: inline-flex; line-height: 0; }
    svg {
      display: block;
      stroke: currentColor; fill: none;
      stroke-width: 2; stroke-linecap: round; stroke-linejoin: round;
    }
  `;template(){const t=at[this.attr("name")]||"",e=Number(this.attr("size"))||20;return`<svg viewBox="0 0 24 24" width="${e}" height="${e}" part="svg" aria-hidden="true">${t}</svg>`}}d("ga-icon",rt);class st extends l{static observed=["accept","multiple","label"];static styles=`
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
  `;template(){const t=this.attr("label","Drop files here or click to browse");return`
      <label class="drop" part="drop">
        <ga-icon class="icon" name="upload" size="24"></ga-icon>
        <span class="label">${o(t)}</span>
        <span class="hint"><slot></slot></span>
        <input type="file" ${this.hasFlag("multiple")?"multiple":""} accept="${o(this.attr("accept"))}" />
      </label>
    `}render(){super.render();const t=this.$(".drop"),e=this.$("input");e.addEventListener("change",()=>this._emit(e.files)),["dragenter","dragover"].forEach(a=>t.addEventListener(a,r=>{r.preventDefault(),t.classList.add("dragging")})),["dragleave","dragend","drop"].forEach(a=>t.addEventListener(a,r=>{r.preventDefault(),t.classList.remove("dragging")})),t.addEventListener("drop",a=>{a.dataTransfer?.files?.length&&this._emit(a.dataTransfer.files)})}_emit(t){t&&t.length&&this.emit("files",{files:Array.from(t)})}}d("ga-file-drop",st);class it extends l{static observed=["color","position","label"];static styles=`
    :host { display: contents; }
    .fab {
      --_c: var(--ga-accent, #54a2ff);
      position: fixed;
      right: max(20px, env(safe-area-inset-right));
      bottom: max(20px, env(safe-area-inset-bottom));
      z-index: 40;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 56px;
      height: 56px;
      padding: 0;
      font-size: 22px;
      line-height: 1;
      background: var(--_c);
      color: var(--ga-accent-contrast, #000);
      border: 0;
      border-radius: 50%;
      box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
      cursor: pointer;
      transition: filter 0.15s ease, transform 0.15s ease;
    }
    .fab:hover { filter: brightness(1.08); }
    .fab:active { transform: translateY(1px); }
    .fab:focus-visible { outline: 2px solid var(--ga-fg, #ededed); outline-offset: 3px; }

    :host([position="bottom-left"]) .fab { left: max(20px, env(safe-area-inset-left)); right: auto; }
    :host([position="static"]) .fab { position: static; }

    :host([color="green"])  .fab { --_c: var(--ga-green, #00c758); }
    :host([color="amber"])  .fab { --_c: var(--ga-amber, #fcbb00); }
    :host([color="purple"]) .fab { --_c: var(--ga-purple, #ac4bff); }
    :host([color="red"])    .fab { --_c: var(--ga-red, #ff6568); }
  `;template(){return`
      <button class="fab" part="fab" aria-label="${o(this.attr("label","Action"))}">
        <slot>+</slot>
      </button>
    `}}d("ga-fab",it);class ot extends l{static observed=["open","side","title"];static styles=`
    :host { display: contents; }
    .scrim {
      position: fixed; inset: 0; z-index: 49;
      background: rgba(0, 0, 0, 0.5);
      opacity: 0; visibility: hidden;
      transition: opacity 0.32s ease, visibility 0.32s;
    }
    :host([open]) .scrim { opacity: 1; visibility: visible; }

    .panel {
      position: fixed; top: 0; right: 0; z-index: 50;
      width: min(420px, 100%); height: 100%;
      display: flex; flex-direction: column;
      background: var(--ga-bg, #000);
      border-left: 1px solid var(--ga-border, #1a1a1a);
      box-shadow: -16px 0 40px rgba(0, 0, 0, 0.4);
      transform: translateX(100%);
      transition: transform 0.32s cubic-bezier(0.4, 0, 0.2, 1);
      visibility: hidden;
    }
    :host([side="left"]) .panel {
      right: auto; left: 0;
      border-left: 0; border-right: 1px solid var(--ga-border, #1a1a1a);
      box-shadow: 16px 0 40px rgba(0, 0, 0, 0.4);
      transform: translateX(-100%);
    }
    :host([open]) .panel { transform: translateX(0); visibility: visible; }

    .head {
      display: flex; align-items: center; justify-content: space-between; gap: 12px;
      padding: 18px 20px; border-bottom: 1px solid var(--ga-border, #1a1a1a);
      font-weight: 600; color: var(--ga-fg, #ededed);
    }
    .body { flex: 1; overflow: auto; padding: 20px; color: var(--ga-muted, #878787); line-height: 1.55; }
    .foot { padding: 16px 20px; border-top: 1px solid var(--ga-border, #1a1a1a); }
    .foot { display: none; }
    .foot.show { display: block; }
    .close {
      flex: none; background: none; border: 0; cursor: pointer;
      color: var(--ga-muted, #878787); font-size: 22px; line-height: 1; padding: 2px 6px;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .close:hover { color: var(--ga-fg, #ededed); }
  `;template(){const t=this.attr("title");return`
      <div class="scrim" part="scrim"></div>
      <div class="panel" part="panel" role="dialog" aria-modal="true">
        <div class="head" part="header">
          <span class="title"><slot name="header">${o(t)}</slot></span>
          <button class="close" aria-label="Close">&times;</button>
        </div>
        <div class="body" part="body"><slot></slot></div>
        <div class="foot" part="footer"><slot name="footer"></slot></div>
      </div>
    `}connectedCallback(){super.connectedCallback(),this._key=t=>{t.key==="Escape"&&this.open&&this.close()},document.addEventListener("keydown",this._key),this.shadowRoot.addEventListener("slotchange",()=>this._syncFooter())}disconnectedCallback(){this._key&&document.removeEventListener("keydown",this._key)}render(){super.render(),this.$(".close")?.addEventListener("click",()=>this.close()),this.$(".scrim")?.addEventListener("click",()=>this.close()),this._syncFooter()}_syncFooter(){const t=this.$('slot[name="footer"]'),e=this.$(".foot");t&&e&&e.classList.toggle("show",t.assignedNodes().length>0)}get open(){return this.hasFlag("open")}set open(t){this.toggleAttribute("open",!!t)}show(){this.open||(this.setAttribute("open",""),this.emit("open"))}close(){this.open&&(this.removeAttribute("open"),this.emit("close"))}toggle(){this.open?this.close():this.show()}}d("ga-panel",ot);class nt extends l{static formAssociated=!0;static observed=["min","max","step","value","label","disabled"];static styles=`
    :host { display: block; }
    .wrap { display: flex; flex-direction: column; gap: 8px; }
    :host([disabled]) .wrap { opacity: 0.5; pointer-events: none; }
    .top { display: flex; align-items: baseline; justify-content: space-between; }
    .label { font-size: var(--ga-fs-sm, 14px); font-weight: 500; color: var(--ga-fg, #ededed); }
    .val { font-family: var(--ga-font-mono, ui-monospace, monospace); font-size: var(--ga-fs-sm, 14px); color: var(--ga-muted, #878787); }

    input[type="range"] {
      -webkit-appearance: none; appearance: none;
      width: 100%; height: 6px; margin: 6px 0;
      border-radius: var(--ga-radius-full, 9999px);
      background: var(--ga-bg-elev-hover, #1f1f1f);
      accent-color: var(--ga-accent, #54a2ff);
      cursor: pointer; outline: none;
    }
    input[type="range"]:focus-visible { box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    input[type="range"]::-webkit-slider-thumb {
      -webkit-appearance: none; appearance: none;
      width: 18px; height: 18px; border-radius: 50%;
      background: var(--ga-accent, #54a2ff);
      border: 2px solid var(--ga-bg, #000);
      box-shadow: 0 1px 3px rgba(0, 0, 0, 0.4);
      cursor: pointer;
    }
    input[type="range"]::-moz-range-thumb {
      width: 16px; height: 16px; border: 2px solid var(--ga-bg, #000); border-radius: 50%;
      background: var(--ga-accent, #54a2ff); cursor: pointer;
    }
    input[type="range"]::-moz-range-track { height: 6px; border-radius: 9999px; background: var(--ga-bg-elev-hover, #1f1f1f); }
  `;constructor(){super(),this._internals=this.attachInternals?.()}template(){const t=this.attr("label"),e=this.attr("value","50");return`
      <div class="wrap">
        ${t?`<div class="top"><span class="label">${o(t)}</span><span class="val">${o(e)}</span></div>`:""}
        <input type="range"
          min="${o(this.attr("min","0"))}"
          max="${o(this.attr("max","100"))}"
          step="${o(this.attr("step","1"))}"
          value="${o(e)}"
          ${this.hasFlag("disabled")?"disabled":""} />
      </div>
    `}render(){super.render();const t=this.$("input"),e=this.$(".val");t&&(this._internals?.setFormValue(t.value),t.addEventListener("input",()=>{this._value=t.value,e&&(e.textContent=t.value),this._internals?.setFormValue(t.value),this.emit("input",{value:t.value})}),t.addEventListener("change",()=>this.emit("change",{value:t.value})))}get value(){return this.$("input")?.value??this._value??this.attr("value")}set value(t){this._value=t,this.setAttribute("value",t)}}d("ga-slider",nt);class lt extends l{static observed=["brand","href","static"];static styles=`
    :host { display: block; }
    .hdr {
      position: sticky; top: 0; z-index: 50;
      display: flex; align-items: center; gap: 16px;
      height: 56px; padding: 0 16px;
      background: var(--ga-bg, #000);
    }
    :host([static]) .hdr { position: static; }

    .brand {
      flex: 0 1 auto; min-width: 0;
      font-size: var(--ga-fs-base, 17px); font-weight: 600;
      color: var(--ga-fg, #ededed); text-decoration: none;
      white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .brand:hover { color: var(--ga-muted, #878787); }

    .spacer { flex: 1 1 auto; }

    .actions { flex: none; display: flex; align-items: center; gap: 16px; }
    /* Slotted links live in the light DOM, so the host page's own \`a\` rules
       would otherwise win (outer tree beats ::slotted on the cascade). Use
       !important to keep the opinionated muted-nav look; consumers can still
       override with their own !important or by targeting ::part. */
    ::slotted(a) {
      color: var(--ga-muted, #878787) !important; text-decoration: none !important;
      font-size: var(--ga-fs-sm, 14px);
      transition: color var(--ga-transition, 0.18s ease);
    }
    ::slotted(a:hover) { color: var(--ga-fg, #ededed) !important; }
  `;template(){const t=this.attr("href"),e=t?"a":"div",a=t?`href="${o(t)}"`:"";return`
      <header class="hdr" part="header">
        <${e} class="brand" part="brand" ${a}><slot name="brand">${o(this.attr("brand"))}</slot></${e}>
        <div class="spacer"></div>
        <nav class="actions" part="actions"><slot></slot></nav>
      </header>
    `}}d("ga-header",lt);class dt extends l{static observed=["open","snap"];static styles=`
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
  `;template(){return`
      <div class="sheet" part="sheet">
        <div class="grip" part="handle"><span class="bar"></span></div>
        <div class="head" part="header"><slot name="header"></slot></div>
        <div class="body" part="body"><slot></slot></div>
      </div>
    `}connectedCallback(){super.connectedCallback(),this._onResize=()=>this._apply(),window.addEventListener("resize",this._onResize),this._onMove=t=>this._move(t),this._onUp=()=>this._up(),window.addEventListener("pointermove",this._onMove),window.addEventListener("pointerup",this._onUp)}disconnectedCallback(){window.removeEventListener("resize",this._onResize),window.removeEventListener("pointermove",this._onMove),window.removeEventListener("pointerup",this._onUp)}attributeChangedCallback(){this._mounted&&this._apply()}render(){super.render();const t=this.$(".grip"),e=this.$(".head");for(const a of[t,e])a?.addEventListener("pointerdown",r=>this._down(r));requestAnimationFrame(()=>this._apply())}get open(){return this.hasFlag("open")}get snap(){return this.attr("snap","half")}show(t){t&&this.setAttribute("snap",t),this.setAttribute("open",""),this._apply(),this.emit("open")}close(){this.removeAttribute("open"),this._apply(),this.emit("close")}snapTo(t){this.setAttribute("snap",t),this._apply(),this.emit("snapchange",{snap:t})}_snaps(){const t=this.$(".sheet")?.offsetHeight||window.innerHeight*.88,e=window.innerHeight;return{full:0,half:Math.max(0,t-e*.45),peek:Math.max(0,t-128),closed:t}}_currentY(){const t=/translateY\(([-0-9.]+)px\)/.exec(this.$(".sheet")?.style.transform||"");return t?parseFloat(t[1]):this._snaps().closed}_apply(){const t=this.$(".sheet");if(!t)return;const e=this._snaps(),a=this.open?e[this.snap]??e.half:e.closed;t.style.transform=`translateY(${a}px)`}_down(t){this._dragging=!0,this._startY=t.clientY,this._startTf=this._currentY(),this.$(".sheet")?.classList.add("dragging")}_move(t){if(!this._dragging)return;const e=this._snaps(),a=Math.min(e.closed,Math.max(0,this._startTf+(t.clientY-this._startY)));this.$(".sheet").style.transform=`translateY(${a}px)`}_up(){if(!this._dragging)return;this._dragging=!1,this.$(".sheet")?.classList.remove("dragging");const t=this._snaps(),e=this._currentY();if(e>t.peek+80){this.close();return}let a="full";for(const r of["full","half","peek"])Math.abs(t[r]-e)<Math.abs(t[a]-e)&&(a=r);a!==this.snap&&(this.setAttribute("snap",a),this.emit("snapchange",{snap:a})),this._apply()}}d("ga-bottom-sheet",dt);class ct extends l{static observed=["items","active"];static styles=`
    :host { display: block; }
    .nav {
      position: fixed; left: 0; right: 0; bottom: 0; z-index: 40;
      display: flex;
      background: var(--ga-bg, #000);
      border-top: 1px solid var(--ga-border, #1a1a1a);
      padding-bottom: env(safe-area-inset-bottom);
    }
    :host([static]) .nav {
      position: static;
      border: 1px solid var(--ga-border, #1a1a1a);
      border-radius: var(--ga-radius, 6px);
      padding-bottom: 0;
    }
    .item {
      flex: 1; min-width: 0;
      display: flex; flex-direction: column; align-items: center; gap: 3px;
      padding: 9px 4px 8px;
      background: none; border: 0; cursor: pointer;
      color: var(--ga-muted, #878787); font-family: inherit;
      transition: color var(--ga-transition, 0.18s ease);
    }
    .item:hover { color: var(--ga-fg, #ededed); }
    .item[aria-current="page"] { color: var(--ga-accent, #54a2ff); }
    .item:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); border-radius: var(--ga-radius, 6px); }
    .icon { font-size: 20px; line-height: 1; }
    .label { font-size: 11px; line-height: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }
  `;_parse(){try{return JSON.parse(this.attr("items","[]"))}catch{return[]}}template(){const t=this._parse(),e=this.attr("active")||t[0]?.id;return`<nav class="nav" part="nav" role="navigation">${t.map(r=>{const s=r.icon||"",n=/^[a-z][a-z0-9-]*$/.test(s)?`<ga-icon class="icon" name="${o(s)}" size="22"></ga-icon>`:`<span class="icon" aria-hidden="true">${o(s||"•")}</span>`;return`
      <button class="item" part="item" data-id="${o(r.id)}"
        ${r.id===e?'aria-current="page"':""}>
        ${n}
        <span class="label">${o(r.label)}</span>
      </button>`}).join("")}</nav>`}render(){super.render(),this.shadowRoot.querySelectorAll(".item").forEach(t=>t.addEventListener("click",()=>this._select(t.dataset.id)))}_select(t){t!==this.attr("active")&&(this.setAttribute("active",t),this.emit("change",{id:t}))}}d("ga-bottom-nav",ct);const $=typeof HTMLElement<"u"&&Object.prototype.hasOwnProperty.call(HTMLElement.prototype,"popover"),k=4;function S(i,t,e={}){let a=!1;const r=e.onDismiss||(()=>{});$&&t.setAttribute("popover","manual");function s(){const c=i.getBoundingClientRect(),g=t.offsetHeight||0,x=window.innerHeight-c.bottom,u=x<g+k&&c.top>x;t.style.minWidth=`${c.width}px`,$?(t.style.position="fixed",t.style.left=`${c.left}px`,t.style.top=u?"auto":`${c.bottom+k}px`,t.style.bottom=u?`${window.innerHeight-c.top+k}px`:"auto",t.style.margin="0"):(t.style.position="absolute",t.style.left="0",t.style.top=u?"auto":"100%",t.style.bottom=u?"100%":"auto",t.style.marginTop=u?"0":`${k}px`,t.style.marginBottom=u?`${k}px`:"0"),t.dataset.placement=u?"top":"bottom"}function n(c){const g=c.composedPath();g.includes(t)||g.includes(i)||f("outside")}function p(c){c.key==="Escape"&&(c.stopPropagation(),f("escape"))}function h(){const c=i.getBoundingClientRect();if(!(c.bottom>0&&c.top<window.innerHeight&&c.right>0&&c.left<window.innerWidth)){f("scroll");return}s()}function v(c){const g=c?"addEventListener":"removeEventListener";document[g]("pointerdown",n,!0),document[g]("keydown",p,!0),window[g]("scroll",h,!0),window[g]("resize",h)}function m(){if(!a){if(a=!0,t.hidden=!1,$)try{t.showPopover()}catch{}s(),v(!0)}}function y(){if(a){if(a=!1,v(!1),$)try{t.hidePopover()}catch{}t.hidden=!0}}function f(c){y(),r(c)}return{show:m,close:y,reposition:s,get open(){return a},destroy(){a&&y()}}}class pt extends l{static formAssociated=!0;static observed=["options","value","multiple","filterable","placeholder","label","hint","error","name","disabled","required"];static styles=`
    :host { display: block; position: relative; }
    .field { display: flex; flex-direction: column; gap: 6px; }
    label {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 500;
      color: var(--ga-fg, #ededed);
    }
    .req { color: var(--ga-red, #ff6568); margin-left: 2px; }

    .trigger {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--ga-space-2, 8px);
      width: 100%;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      text-align: left;
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: 10px 12px;
      cursor: pointer;
      transition: border-color var(--ga-transition, 0.18s ease),
        box-shadow var(--ga-transition, 0.18s ease);
    }
    .trigger:hover { border-color: var(--ga-muted, #878787); }
    .trigger:focus-visible {
      outline: none;
      border-color: var(--ga-accent, #54a2ff);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--ga-accent, #54a2ff) 25%, transparent);
    }
    :host([disabled]) .trigger { opacity: 0.5; cursor: not-allowed; }
    :host([error]) .trigger { border-color: var(--ga-red, #ff6568); }
    .placeholder { color: var(--ga-dim, #454545); }
    .caret { flex: none; width: 14px; height: 14px; color: var(--ga-muted, #878787); }
    .trigger[aria-expanded="true"] .caret { transform: rotate(180deg); }

    .panel {
      z-index: var(--ga-z-overlay, 900);
      box-sizing: border-box;
      max-height: 280px;
      overflow: auto;
      padding: 4px;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      box-shadow: var(--ga-shadow, 0 8px 24px rgba(0, 0, 0, 0.5));
    }
    .panel[hidden] { display: none; }
    .panel:popover-open { display: block; }
    /* The top layer paints its own backdrop; we want none. */
    .panel::backdrop { background: transparent; }

    .filter {
      width: 100%;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg, #000);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius-sm, 4px);
      padding: 7px 9px;
      margin-bottom: 4px;
    }
    .filter:focus { outline: none; border-color: var(--ga-accent, #54a2ff); }

    .opt {
      display: flex;
      align-items: center;
      gap: var(--ga-space-2, 8px);
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      border-radius: var(--ga-radius-sm, 4px);
      padding: 8px 10px;
      cursor: pointer;
    }
    .opt[aria-selected="true"] { color: var(--ga-accent, #54a2ff); }
    .opt.active { background: var(--ga-bg-elev-hover, #232323); }
    .opt[aria-disabled="true"] { opacity: 0.4; cursor: not-allowed; }
    .tick { flex: none; width: 14px; height: 14px; opacity: 0; }
    .opt[aria-selected="true"] .tick { opacity: 1; }
    .empty {
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-muted, #878787);
      padding: 10px;
    }

    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-muted, #878787); }
    .error { font-size: var(--ga-fs-xs, 12px); color: var(--ga-red, #ff6568); }
  `;constructor(){super(),this._internals=this.attachInternals?.(),this._open=!1,this._active=-1,this._filterText="",this._typeahead="",this._typeaheadAt=0,this._filterTimer=0,this._popup=null,this._values=null,this._reflecting=!1}attributeChangedCallback(t,e,a){t==="value"&&!this._reflecting&&(this._values=null),super.attributeChangedCallback(t,e,a)}_allOptions(){const t=this.getAttribute("options");if(t)try{const e=JSON.parse(t);if(Array.isArray(e))return e.map(ht)}catch{}return[...this.querySelectorAll("option")].map(e=>({value:e.value??e.textContent.trim(),label:e.textContent.trim(),disabled:e.disabled}))}_visibleOptions(){const t=this._allOptions(),e=this._filterText.trim().toLowerCase();return e?t.filter(a=>a.label.toLowerCase().includes(e)):t}get multiple(){return this.hasFlag("multiple")}_selected(){if(this._values)return this._values;const t=this.attr("value");return t?this.multiple?t.split(",").filter(Boolean):[t]:[]}_setSelected(t){this._values=t,this._reflecting=!0,this.setAttribute("value",t.join(",")),this._reflecting=!1}_summary(){const t=this._selected(),e=this._allOptions();if(!t.length){const r=this.attr("placeholder","Select…");return`<span class="placeholder">${o(r)}</span>`}if(this.multiple&&t.length>1)return`<span>${t.length} selected</span>`;const a=e.find(r=>r.value===t[0]);return`<span>${o(a?a.label:t[0])}</span>`}template(){const t=this.attr("label"),e=this.attr("error"),a=this.attr("hint"),r=this.hasFlag("required")?'<span class="req">*</span>':"",s=this.hasFlag("disabled");return`
      <div class="field">
        ${t?`<label part="label" id="lbl">${o(t)}${r}</label>`:""}
        <button class="trigger" part="trigger" type="button"
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded="${this._open}"
          aria-controls="listbox"
          ${t?'aria-labelledby="lbl"':""}
          aria-invalid="${e?"true":"false"}"
          ${s?"disabled":""}>
          ${this._summary()}
          <svg class="caret" viewBox="0 0 16 16" aria-hidden="true" fill="none"
            stroke="currentColor" stroke-width="1.5">
            <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <div class="panel" part="panel" id="panel" ${this._open?"":"hidden"}>
          ${this.hasFlag("filterable")?`<input class="filter" part="filter" type="text" autocomplete="off"
                 placeholder="Filter…" aria-label="Filter options"
                 value="${o(this._filterText)}" />`:""}
          <div id="listbox" role="listbox"
            aria-multiselectable="${this.multiple}"
            ${t?'aria-labelledby="lbl"':""}>${this._rows()}</div>
        </div>
        ${e?`<span class="error" part="error">${o(e)}</span>`:a?`<span class="hint" part="hint">${o(a)}</span>`:""}
      </div>
      <slot hidden></slot>
    `}_rows(){const t=this._visibleOptions();if(!t.length)return'<div class="empty" role="option" aria-disabled="true">No matches</div>';const e=this._selected();return t.map((a,r)=>{const s=e.includes(a.value);return`<div class="opt${r===this._active?" active":""}"
          part="option" role="option" id="opt-${r}" data-value="${o(a.value)}"
          aria-selected="${s}"
          ${a.disabled?'aria-disabled="true"':""}>
          <svg class="tick" viewBox="0 0 16 16" aria-hidden="true" fill="none"
            stroke="currentColor" stroke-width="2">
            <path d="M3 8.5l3.5 3.5L13 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span>${o(a.label)}</span>
        </div>`}).join("")}render(){super.render(),this._internals?.setFormValue(this._formValue());const t=this.$(".trigger"),e=this.$(".panel");if(!t||!e)return;this._popup?.destroy(),this._popup=S(t,e,{onDismiss:()=>{this._open=!1,this._active=-1,this._syncOpenState(),t.focus()}}),this._open&&this._popup.show(),t.addEventListener("click",()=>this._toggle()),t.addEventListener("keydown",r=>this._onTriggerKey(r));const a=this.$(".filter");a?.addEventListener("input",()=>this._onFilterInput(a.value)),a?.addEventListener("keydown",r=>this._onTriggerKey(r)),this._bindRows(),this.$("slot")?.addEventListener("slotchange",()=>this._onSlotChange())}_onSlotChange(){const t=this.$(".trigger");if(t){const e=t.querySelector(".caret");t.innerHTML=this._summary()+(e?e.outerHTML:"")}this._repaintRows()}disconnectedCallback(){this._popup?.destroy(),clearTimeout(this._filterTimer)}_bindRows(){this.shadowRoot.querySelectorAll(".opt").forEach(t=>{t.addEventListener("click",()=>{t.getAttribute("aria-disabled")!=="true"&&this._commit(t.dataset.value)}),t.addEventListener("pointerdown",e=>e.preventDefault())})}_repaintRows(){const t=this.$("#listbox");t&&(t.innerHTML=this._rows(),this._bindRows(),this._syncActive(),this._popup?.reposition())}_toggle(){this.hasFlag("disabled")||(this._open?this._close():this._openPanel())}_openPanel(){if(this._open)return;this._open=!0;const t=this._selected(),e=this._visibleOptions();this._active=Math.max(0,e.findIndex(r=>t.includes(r.value))),this._syncOpenState(),this._popup?.show(),this._repaintRows();const a=this.$(".filter");a&&a.focus()}_close({focusTrigger:t=!0}={}){this._open&&(this._open=!1,this._active=-1,this._popup?.close(),this._syncOpenState(),t&&this.$(".trigger")?.focus())}_syncOpenState(){const t=this.$(".trigger"),e=this.$(".panel");t?.setAttribute("aria-expanded",String(this._open)),e&&(e.hidden=!this._open),this._syncActive()}_syncActive(){const t=this.$(".trigger"),e=[...this.shadowRoot.querySelectorAll(".opt")];e.forEach((r,s)=>r.classList.toggle("active",s===this._active));const a=e[this._active];this._open&&a?(t?.setAttribute("aria-activedescendant",a.id),a.scrollIntoView({block:"nearest"})):t?.removeAttribute("aria-activedescendant")}_onTriggerKey(t){const e=this._visibleOptions();if(!this._open){(["Enter"," ","ArrowDown","ArrowUp"].includes(t.key)||t.altKey&&t.key==="ArrowDown")&&(t.preventDefault(),this._openPanel());return}switch(t.key){case"Escape":t.preventDefault(),this._close();return;case"Tab":{const a=T(e,this._active);a?this._commit(a.value,{keepOpen:!1}):this._close({focusTrigger:!1});return}case"Enter":{t.preventDefault();const a=T(e,this._active);a&&this._commit(a.value);return}case" ":{if(this.hasFlag("filterable")&&t.target===this.$(".filter"))return;t.preventDefault();const a=T(e,this._active);a&&this._commit(a.value);return}case"ArrowDown":t.preventDefault(),this._move(1,e);return;case"ArrowUp":t.preventDefault(),this._move(-1,e);return;case"Home":t.preventDefault(),this._moveTo(0,e,1);return;case"End":t.preventDefault(),this._moveTo(e.length-1,e,-1);return;case"PageDown":t.preventDefault(),this._moveTo(Math.min(e.length-1,this._active+10),e,-1);return;case"PageUp":t.preventDefault(),this._moveTo(Math.max(0,this._active-10),e,1);return}!this.hasFlag("filterable")&&t.key.length===1&&!t.metaKey&&!t.ctrlKey&&this._onTypeahead(t.key,e)}_move(t,e){if(!e.length)return;let a=this._active;for(let r=0;r<e.length;r++)if(a=(a+t+e.length)%e.length,!e[a].disabled){this._active=a,this._syncActive();return}}_moveTo(t,e,a){if(!e.length)return;let r=Math.max(0,Math.min(e.length-1,t));for(let s=0;s<e.length;s++){if(!e[r].disabled){this._active=r,this._syncActive();return}r=(r+a+e.length)%e.length}}_onTypeahead(t,e){const a=Date.now();this._typeahead=a-this._typeaheadAt>800?t:this._typeahead+t,this._typeaheadAt=a;const r=this._typeahead.toLowerCase(),s=e.findIndex(n=>!n.disabled&&n.label.toLowerCase().startsWith(r));s>=0&&(this._active=s,this._syncActive())}_onFilterInput(t){this._filterText=t,this._active=0,this._repaintRows(),clearTimeout(this._filterTimer),this._filterTimer=setTimeout(()=>this.emit("filter",{text:t}),200)}_commit(t,{keepOpen:e=this.multiple}={}){if(t==null)return;let a;if(this.multiple){const r=this._selected(),s=r.includes(t)?r.filter(n=>n!==t):[...r,t];this._setSelected(s),a=s}else a=t,this._setSelected([t]);this._internals?.setFormValue(this._formValue()),this.emit("input",{value:a}),this.emit("change",{value:a}),e?this._openPanel():this._close()}_formValue(){const t=this._selected();if(!this.multiple)return t[0]??"";const e=new FormData,a=this.attr("name");return a&&t.forEach(r=>e.append(a,r)),e}get value(){const t=this._selected();return this.multiple?t:t[0]??""}set value(t){if(Array.isArray(t)){this._setSelected(t.map(String));return}const e=String(t??"");this._setSelected(e?[e]:[])}get options(){return this._allOptions()}set options(t){this.setAttribute("options",JSON.stringify(t??[]))}}function T(i,t){const e=i[t];return e&&!e.disabled?e:null}function ht(i){return typeof i=="string"?{value:i,label:i,disabled:!1}:{value:String(i.value??i.id??""),label:String(i.label??i.value??i.id??""),disabled:!!i.disabled}}d("ga-select",pt);class gt extends l{static observed=["value","month","locale","first-day","min","max","disabled"];static styles=`
    :host {
      display: inline-block;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      padding: var(--ga-space-3, 12px);
    }
    .head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--ga-space-2, 8px);
      margin-bottom: var(--ga-space-2, 8px);
    }
    .title {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
    }
    .nav {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 28px;
      height: 28px;
      color: var(--ga-muted, #878787);
      background: transparent;
      border: 1px solid transparent;
      border-radius: var(--ga-radius-sm, 4px);
      cursor: pointer;
    }
    .nav:hover { color: var(--ga-fg, #ededed); background: var(--ga-bg-elev-hover, #232323); }
    .nav:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    .nav svg { width: 14px; height: 14px; }

    table { border-collapse: collapse; }
    th {
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 500;
      color: var(--ga-muted, #878787);
      padding: 4px 0;
      width: 34px;
    }
    td { padding: 1px; }
    .day {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: transparent;
      border: 1px solid transparent;
      border-radius: var(--ga-radius-sm, 4px);
      cursor: pointer;
    }
    .day:hover:not([aria-disabled="true"]) { background: var(--ga-bg-elev-hover, #232323); }
    .day.outside { color: var(--ga-dim, #454545); }
    .day.today { border-color: var(--ga-border-strong, #2a2a2a); font-weight: 600; }
    .day[aria-selected="true"] {
      background: var(--ga-fg, #ededed);
      color: var(--ga-bg, #000);
      border-color: var(--ga-fg, #ededed);
      font-weight: 600;
    }
    /* aria-disabled rather than the disabled attribute: a disabled grid cell
       must stay focusable, or arrow-key navigation dead-ends on it (WAI-ARIA
       grid pattern). Selection is guarded in _select instead. */
    .day[aria-disabled="true"] { opacity: 0.3; cursor: not-allowed; }
    .day:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    :host([disabled]) { opacity: 0.5; pointer-events: none; }
  `;constructor(){super(),this._focusDate="",this._wantFocus=!1}get _locale(){return this.attr("locale")||void 0}get _firstDay(){const t=Number(this.attr("first-day","1"));return Number.isInteger(t)&&t>=0&&t<=6?t:1}get _month(){const t=this.attr("month");if(/^\d{4}-\d{2}$/.test(t))return t;const e=this.attr("value");return b(e)?e.slice(0,7):z().slice(0,7)}_isDisabled(t){const e=this.attr("min"),a=this.attr("max");return!!(b(e)&&t<e||b(a)&&t>a)}template(){const t=this._month,[e,a]=t.split("-").map(Number),r=this.attr("value"),s=z(),n=new Intl.DateTimeFormat(this._locale,{month:"long",year:"numeric",timeZone:"UTC"}).format(w(`${t}-01`)),p=vt(this._locale,this._firstDay),h=w(`${t}-01`),v=(h.getUTCDay()-this._firstDay+7)%7,m=C(h,-v);let y="";for(let f=0;f<6;f++){let c="";for(let g=0;g<7;g++){const x=C(m,f*7+g),u=_(x),M=x.getUTCMonth()+1!==a||x.getUTCFullYear()!==e,j=this._isDisabled(u),O=u===r,A=["day"];M&&A.push("outside"),u===s&&A.push("today"),c+=`<td role="gridcell">
          <button class="${A.join(" ")}" part="day" type="button"
            data-iso="${u}"
            tabindex="${u===this._tabDate()?"0":"-1"}"
            aria-selected="${O}"
            ${j?'aria-disabled="true"':""}
            aria-label="${o(ut(u,this._locale))}">${x.getUTCDate()}</button>
        </td>`}y+=`<tr role="row">${c}</tr>`}return`
      <div class="head">
        <button class="nav" part="prev" type="button" data-step="-1" aria-label="Previous month">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M10 3L5 8l5 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <div class="title" part="title" aria-live="polite">${o(n)}</div>
        <button class="nav" part="next" type="button" data-step="1" aria-label="Next month">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M6 3l5 5-5 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
      </div>
      <table role="grid" aria-label="${o(n)}">
        <thead><tr role="row">
          ${p.map(f=>`<th role="columnheader" abbr="${o(f.long)}" scope="col">${o(f.short)}</th>`).join("")}
        </tr></thead>
        <tbody>${y}</tbody>
      </table>
    `}_tabDate(){const t=this._month,e=[this._focusDate,this.attr("value"),z()];for(const r of e)if(b(r)&&r.slice(0,7)===t&&!this._isDisabled(r))return r;const a=new Date(Date.UTC(Number(t.slice(0,4)),Number(t.slice(5,7)),0)).getUTCDate();for(let r=1;r<=a;r++){const s=`${t}-${String(r).padStart(2,"0")}`;if(!this._isDisabled(s))return s}return`${t}-01`}render(){super.render(),this.shadowRoot.querySelectorAll(".nav").forEach(t=>{t.addEventListener("click",()=>this._shiftMonth(Number(t.dataset.step)))}),this.shadowRoot.querySelectorAll(".day").forEach(t=>{t.addEventListener("click",()=>this._select(t.dataset.iso)),t.addEventListener("keydown",e=>this._onKey(e))}),this._wantFocus&&(this._wantFocus=!1,this.shadowRoot.querySelector(`.day[data-iso="${this._tabDate()}"]`)?.focus())}_shiftMonth(t){const[e,a]=this._month.split("-").map(Number),r=new Date(Date.UTC(e,a-1+t,1));this.setAttribute("month",_(r).slice(0,7))}_onKey(t){const e={ArrowLeft:-1,ArrowRight:1,ArrowUp:-7,ArrowDown:7},a=t.currentTarget.dataset.iso;if(e[t.key]!==void 0){t.preventDefault(),this._focusTo(_(C(w(a),e[t.key])));return}if(t.key==="Home"||t.key==="End"){t.preventDefault();const r=w(a),s=(r.getUTCDay()-this._firstDay+7)%7;this._focusTo(_(C(r,t.key==="Home"?-s:6-s)));return}if(t.key==="PageUp"||t.key==="PageDown"){t.preventDefault();const[r,s,n]=a.split("-").map(Number),p=t.key==="PageUp"?-1:1,h=new Date(Date.UTC(r,s+p,0)).getUTCDate();this._focusTo(_(new Date(Date.UTC(r,s-1+p,Math.min(n,h)))))}}_focusTo(t){this._focusDate=t,this._wantFocus=!0,t.slice(0,7)!==this._month?this.setAttribute("month",t.slice(0,7)):this.render()}_select(t){!t||this._isDisabled(t)||this.hasFlag("disabled")||(this._focusDate=t,this.setAttribute("value",t),this.emit("change",{value:t}))}get value(){return this.attr("value")}set value(t){this.setAttribute("value",t??"")}}function b(i){return typeof i=="string"&&/^\d{4}-\d{2}-\d{2}$/.test(i)}function w(i){const[t,e,a]=i.split("-").map(Number);return new Date(Date.UTC(t,(e||1)-1,a||1))}function _(i){return i.toISOString().slice(0,10)}function C(i,t){return new Date(i.getTime()+t*864e5)}function z(){const i=new Date;return _(new Date(Date.UTC(i.getFullYear(),i.getMonth(),i.getDate())))}function ut(i,t){return new Intl.DateTimeFormat(t,{dateStyle:"long",timeZone:"UTC"}).format(w(i))}function vt(i,t){const e=new Intl.DateTimeFormat(i,{weekday:"short",timeZone:"UTC"}),a=new Intl.DateTimeFormat(i,{weekday:"long",timeZone:"UTC"}),r=Date.UTC(2024,0,7);return Array.from({length:7},(s,n)=>{const p=new Date(r+(n+t)%7*864e5);return{short:e.format(p),long:a.format(p)}})}d("ga-calendar",gt);class ft extends l{static formAssociated=!0;static observed=["value","label","placeholder","hint","error","name","locale","min","max","first-day","disabled","required"];static styles=`
    :host { display: block; position: relative; }
    .field { display: flex; flex-direction: column; gap: 6px; }
    label { font-size: var(--ga-fs-sm, 14px); font-weight: 500; color: var(--ga-fg, #ededed); }
    .req { color: var(--ga-red, #ff6568); margin-left: 2px; }

    .control {
      display: flex;
      align-items: center;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border-strong, #2a2a2a);
      border-radius: var(--ga-radius, 6px);
      transition: border-color var(--ga-transition, 0.18s ease),
        box-shadow var(--ga-transition, 0.18s ease);
    }
    .control:hover { border-color: var(--ga-muted, #878787); }
    .control:focus-within {
      border-color: var(--ga-accent, #54a2ff);
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--ga-accent, #54a2ff) 25%, transparent);
    }
    :host([error]) .control, .control.invalid { border-color: var(--ga-red, #ff6568); }
    :host([disabled]) .control { opacity: 0.5; }

    input {
      flex: 1;
      min-width: 0;
      font-family: inherit;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-fg, #ededed);
      background: transparent;
      border: 0;
      padding: 10px 12px;
    }
    input:focus { outline: none; }
    input::placeholder { color: var(--ga-dim, #454545); }

    .open {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 34px;
      align-self: stretch;
      color: var(--ga-muted, #878787);
      background: transparent;
      border: 0;
      border-left: 1px solid var(--ga-border, #1f1f1f);
      border-radius: 0 var(--ga-radius, 6px) var(--ga-radius, 6px) 0;
      cursor: pointer;
    }
    .open:hover { color: var(--ga-fg, #ededed); }
    .open:focus-visible { outline: none; box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff); }
    .open svg { width: 15px; height: 15px; }

    .panel {
      z-index: var(--ga-z-overlay, 900);
      box-sizing: border-box;
      padding: 0;
      border: 0;
      background: transparent;
    }
    .panel[hidden] { display: none; }
    .panel::backdrop { background: transparent; }

    .hint { font-size: var(--ga-fs-xs, 12px); color: var(--ga-muted, #878787); }
    .error { font-size: var(--ga-fs-xs, 12px); color: var(--ga-red, #ff6568); }
  `;constructor(){super(),this._internals=this.attachInternals?.(),this._open=!1,this._invalid=!1,this._popup=null}get _locale(){return this.attr("locale")||void 0}template(){const t=this.attr("label"),e=this.attr("error"),a=this.attr("hint"),r=this.hasFlag("required")?'<span class="req">*</span>':"",s=this.attr("value"),n=this.attr("placeholder")||bt(this._locale);return`
      <div class="field">
        ${t?`<label part="label" id="lbl">${o(t)}${r}</label>`:""}
        <div class="control" part="control">
          <input part="input" type="text" inputmode="numeric" autocomplete="off"
            value="${o(s)}"
            placeholder="${o(n)}"
            ${t?'aria-labelledby="lbl"':""}
            aria-invalid="${e?"true":"false"}"
            ${this.hasFlag("disabled")?"disabled":""}
            ${this.hasFlag("required")?"required":""} />
          <button class="open" part="open" type="button"
            aria-label="Choose date" aria-haspopup="dialog"
            aria-expanded="${this._open}"
            ${this.hasFlag("disabled")?"disabled":""}>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
              <rect x="2" y="3" width="12" height="11" rx="2"/>
              <path d="M2 6.5h12M5.5 1.5v3M10.5 1.5v3" stroke-linecap="round"/>
            </svg>
          </button>
        </div>
        <div class="panel" part="panel" role="dialog" aria-label="Choose date" ${this._open?"":"hidden"}>
          <ga-calendar
            ${b(s)?`value="${o(s)}"`:""}
            ${this.attr("min")?`min="${o(this.attr("min"))}"`:""}
            ${this.attr("max")?`max="${o(this.attr("max"))}"`:""}
            ${this.attr("locale")?`locale="${o(this.attr("locale"))}"`:""}
            ${this.attr("first-day")?`first-day="${o(this.attr("first-day"))}"`:""}
          ></ga-calendar>
        </div>
        ${e?`<span class="error" part="error">${o(e)}</span>`:a?`<span class="hint" part="hint">${o(a)}</span>`:""}
      </div>
    `}render(){super.render(),this._internals?.setFormValue(this.attr("value"));const t=this.$("input"),e=this.$(".open"),a=this.$(".panel"),r=this.$("ga-calendar");!t||!e||!a||(this._popup?.destroy(),this._popup=S(e,a,{onDismiss:()=>{this._open=!1,this._syncOpen(),e.focus()}}),this._open&&this._popup.show(),e.addEventListener("click",()=>this._toggle()),t.addEventListener("input",()=>{const s=L(t.value.trim(),this._locale),n=s&&!this._outOfRange(s)?s:"";this.emit("input",{value:n,text:t.value})}),t.addEventListener("change",()=>this._commitTyped(t.value)),t.addEventListener("keydown",s=>{s.key==="Enter"&&(s.preventDefault(),this._commitTyped(t.value)),s.key==="ArrowDown"&&s.altKey&&(s.preventDefault(),this._openPanel())}),this._setInvalid(this._invalid),r?.addEventListener("change",s=>{s.stopPropagation(),this._commit(s.detail.value),this._close()}))}disconnectedCallback(){this._popup?.destroy()}_toggle(){this.hasFlag("disabled")||(this._open?this._close():this._openPanel())}_openPanel(){if(this._open||this.hasFlag("disabled"))return;this._open=!0,this._syncOpen(),this._popup?.show();const t=this.$("ga-calendar"),e=b(this.attr("value"))?this.attr("value"):z();t?.shadowRoot?.querySelector(`.day[data-iso="${e}"]`)?.focus()}_close({focusButton:t=!0}={}){this._open&&(this._open=!1,this._popup?.close(),this._syncOpen(),t&&this.$(".open")?.focus())}_syncOpen(){this.$(".open")?.setAttribute("aria-expanded",String(this._open));const t=this.$(".panel");t&&(t.hidden=!this._open)}_commitTyped(t){const e=String(t??"").trim();if(!e){this._setInvalid(!1),this._commit("");return}const a=L(e,this._locale);if(!a||this._outOfRange(a)){this._setInvalid(!0);return}this._setInvalid(!1),this._commit(a)}_outOfRange(t){const e=this.attr("min"),a=this.attr("max");return b(e)&&t<e||b(a)&&t>a}_setInvalid(t){this._invalid=t;const e=this.$("input");this.$(".control")?.classList.toggle("invalid",t),e?.setAttribute("aria-invalid",String(t||!!this.attr("error")));const a=this.hasFlag("required")&&!this.attr("value");t?this._internals?.setValidity?.({badInput:!0},"Enter a valid date.",e??void 0):a?this._internals?.setValidity?.({valueMissing:!0},"Choose a date.",e??void 0):this._internals?.setValidity?.({},"")}_commit(t){this.setAttribute("value",t),this._internals?.setFormValue(t),this.emit("input",{value:t}),this.emit("change",{value:t})}get value(){return this.attr("value")}set value(t){this.setAttribute("value",t??"")}}function L(i,t){const e=i.match(/^(\d{4})-(\d{1,2})-(\d{1,2})$/);if(e)return E(+e[1],+e[2],+e[3]);const a=i.split(/[^\d]+/).filter(Boolean).map(Number);if(a.length!==3||a.some(Number.isNaN))return null;const r=F(t),s={};r.forEach((v,m)=>{s[v]=a[m]});let{year:n,month:p,day:h}=s;return n==null||p==null||h==null?null:(n<100&&(n+=n<50?2e3:1900),E(n,p,h))}function E(i,t,e){if(t<1||t>12||e<1||e>31)return null;const a=new Date(Date.UTC(i,t-1,e));return a.getUTCMonth()!==t-1||a.getUTCDate()!==e?null:a.toISOString().slice(0,10)}function F(i){try{const e=new Intl.DateTimeFormat(i,{year:"numeric",month:"2-digit",day:"2-digit",timeZone:"UTC"}).formatToParts(w("2026-03-14")).filter(a=>["year","month","day"].includes(a.type)).map(a=>a.type);if(e.length===3)return e}catch{}return["year","month","day"]}function bt(i){const t={year:"YYYY",month:"MM",day:"DD"},e=F(i),a=e[0]==="year"?"-":"/";return e.map(r=>t[r]).join(a)}d("ga-date-input",ft);class mt extends l{static observed=["title","legend","height","empty-text","loading","empty"];static styles=`
    :host { display: block; }
    .frame {
      display: flex;
      flex-direction: column;
      gap: var(--ga-space-3, 12px);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1f1f1f);
      border-radius: var(--ga-radius-lg, 8px);
      padding: var(--ga-space-4, 16px);
    }
    /* The caption and the legend are siblings of the plot (the caption has to
       be a direct child of <figure>), so the frame lays out the header row. */
    .frame > .title { order: -2; }
    .frame > .legend { order: -1; }
    .title {
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
    }
    .legend {
      display: flex;
      flex-wrap: wrap;
      gap: var(--ga-space-3, 12px);
      margin: 0;
      padding: 0;
      list-style: none;
    }
    .legend li {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-size: var(--ga-fs-xs, 12px);
      color: var(--ga-chart-label, #878787);
    }
    .swatch {
      width: 10px;
      height: 10px;
      border-radius: 2px;
      background: var(--swatch);
      flex: none;
    }
    .plot {
      position: relative;
      min-height: var(--plot-height, 180px);
    }
    .plot ::slotted(svg),
    .plot ::slotted(canvas) { display: block; width: 100%; height: auto; }
    .state {
      position: absolute;
      inset: 0;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: var(--ga-space-2, 8px);
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-muted, #878787);
      background: var(--ga-bg-elev, #1a1a1a);
    }
    .spinner {
      width: 16px;
      height: 16px;
      border: 2px solid var(--ga-border-strong, #2a2a2a);
      border-top-color: var(--ga-accent, #54a2ff);
      border-radius: 50%;
      animation: spin 0.7s linear infinite;
    }
    @keyframes spin { to { transform: rotate(360deg); } }
    .footer {
      font-size: var(--ga-fs-xs, 12px);
      color: var(--ga-muted, #878787);
    }
  `;_legend(){let t;try{t=JSON.parse(this.attr("legend","[]"))}catch{return[]}return Array.isArray(t)?t.filter(e=>e!=null).map(e=>typeof e=="object"?{label:String(e.label??""),color:e.color?String(e.color):""}:{label:String(e),color:""}):[]}template(){const t=this.attr("title"),e=this._legend(),a=this.hasFlag("loading"),r=this.hasFlag("empty"),s=this.attr("height","180px"),n=e.map((h,v)=>{const m=h.color||`var(--ga-chart-${v%8+1})`;return`<li><span class="swatch" style="--swatch:${o(m)}"></span>${o(h.label??"")}</li>`}).join("");let p="";return a?p=`<div class="state" part="state" role="status">
        <span class="spinner" aria-hidden="true"></span> Loading…
      </div>`:r&&(p=`<div class="state" part="state" role="status">${o(this.attr("empty-text","No data"))}</div>`),`
      <figure class="frame" part="frame" style="--plot-height:${o(s)}">
        
        ${t?`<figcaption class="title" part="title">${o(t)}</figcaption>`:""}
        ${n?`<ul class="legend" part="legend">${n}</ul>`:""}
        <div class="plot" part="plot" aria-busy="${a}">
          <slot></slot>
          ${p}
        </div>
        <div class="footer" part="footer"><slot name="footer"></slot></div>
      </figure>
    `}}d("ga-chart-frame",mt);class xt extends l{static observed=["role","state","author","time"];static styles=`
    :host { display: block; }
    .row { display: flex; flex-direction: column; gap: 4px; max-width: 100%; }
    :host([role="user"]) .row { align-items: flex-end; }

    .meta {
      display: flex;
      align-items: baseline;
      gap: var(--ga-space-2, 8px);
      font-size: var(--ga-fs-xs, 12px);
      color: var(--ga-muted, #878787);
      padding: 0 2px;
    }
    .bubble {
      max-width: min(52ch, 100%);
      font-size: var(--ga-fs-sm, 14px);
      line-height: 1.55;
      color: var(--ga-fg, #ededed);
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1f1f1f);
      border-radius: var(--ga-radius-lg, 8px);
      padding: 10px 13px;
      overflow-wrap: anywhere;
    }
    :host([role="user"]) .bubble {
      background: var(--ga-fg, #ededed);
      border-color: var(--ga-fg, #ededed);
      color: var(--ga-bg, #000);
    }
    :host([role="system"]) .row { align-items: center; }
    :host([role="system"]) .bubble {
      background: transparent;
      border: 0;
      color: var(--ga-muted, #878787);
      font-size: var(--ga-fs-xs, 12px);
      text-align: center;
      padding: 4px 0;
    }
    :host([state="error"]) .bubble {
      border-color: var(--ga-red, #ff6568);
      color: var(--ga-red, #ff6568);
    }
    :host([state="pending"]) .bubble { opacity: 0.6; }

    .dots { display: inline-flex; gap: 3px; vertical-align: middle; }
    .dots i {
      width: 4px; height: 4px; border-radius: 50%;
      background: currentColor;
      animation: blink 1.2s infinite ease-in-out;
    }
    .dots i:nth-child(2) { animation-delay: 0.15s; }
    .dots i:nth-child(3) { animation-delay: 0.3s; }
    @keyframes blink { 0%, 60%, 100% { opacity: 0.25; } 30% { opacity: 1; } }

    /* Visible to assistive technology, not on screen. */
    .sr {
      position: absolute;
      width: 1px;
      height: 1px;
      margin: -1px;
      padding: 0;
      overflow: hidden;
      clip: rect(0 0 0 0);
      clip-path: inset(50%);
      white-space: nowrap;
      border: 0;
    }
    .caret {
      display: inline-block;
      width: 2px;
      height: 1em;
      background: currentColor;
      vertical-align: text-bottom;
      margin-left: 1px;
      animation: blink 1s step-end infinite;
    }
  `;template(){const t=this.attr("role","assistant"),e=this.attr("state","sent"),a=this.attr("author"),r=this.attr("time"),s=yt(t,e,a),n=e==="pending"?'<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>':`<slot></slot>${e==="streaming"?'<span class="caret" aria-hidden="true"></span>':""}`,p=e==="streaming"||e==="pending"?'aria-live="polite"':"",h=t==="system"?'role="note"':e==="error"?'role="alert"':"";return`
      <div class="row" part="row">
        ${a||r?`<div class="meta" part="meta">
              ${a?`<span>${o(a)}</span>`:""}
              ${r?`<time>${o(r)}</time>`:""}
            </div>`:""}
        <div class="bubble" part="bubble" ${p} ${h}
          ${s?'aria-describedby="status"':""}>${n}${s?`<span id="status" class="sr">${o(s)}</span>`:""}</div>
      </div>
    `}}function yt(i,t,e){const a=e||{user:"You",assistant:"Assistant",system:"System"}[i]||i;return t==="pending"?`${a} is replying`:t==="error"?`${a}, failed to send`:""}d("ga-chat-message",xt);class _t extends l{static observed=["empty-text","height"];static styles=`
    :host { display: block; }
    .shell {
      display: flex;
      flex-direction: column;
      min-height: 0;
      background: var(--ga-bg-elev, #1a1a1a);
      border: 1px solid var(--ga-border, #1f1f1f);
      border-radius: var(--ga-radius-lg, 8px);
      overflow: hidden;
    }
    .header {
      flex: none;
      font-size: var(--ga-fs-sm, 14px);
      font-weight: 600;
      color: var(--ga-fg, #ededed);
      border-bottom: 1px solid var(--ga-border, #1f1f1f);
      padding: var(--ga-space-3, 12px) var(--ga-space-4, 16px);
    }
    .header.empty, .footer.empty { display: none; }

    .area { position: relative; }
    .log {
      height: var(--chat-height, 360px);
      overflow-y: auto;
      overscroll-behavior: contain;
      display: flex;
      flex-direction: column;
      gap: var(--ga-space-3, 12px);
      padding: var(--ga-space-4, 16px);
      scroll-behavior: smooth;
    }
    @media (prefers-reduced-motion: reduce) { .log { scroll-behavior: auto; } }

    .placeholder {
      margin: auto;
      font-size: var(--ga-fs-sm, 14px);
      color: var(--ga-muted, #878787);
      text-align: center;
    }
    .placeholder[hidden] { display: none; }

    .jump {
      position: absolute;
      left: 50%;
      bottom: var(--ga-space-3, 12px);
      transform: translateX(-50%);
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-family: inherit;
      font-size: var(--ga-fs-xs, 12px);
      font-weight: 500;
      color: var(--ga-bg, #000);
      background: var(--ga-fg, #ededed);
      border: 0;
      border-radius: var(--ga-radius-full, 9999px);
      padding: 7px 13px;
      cursor: pointer;
      box-shadow: var(--ga-shadow, 0 8px 24px rgba(0, 0, 0, 0.4));
    }
    .jump[hidden] { display: none; }
    .jump:focus-visible {
      outline: none;
      box-shadow: var(--ga-ring, 0 0 0 2px #000, 0 0 0 4px #54a2ff);
    }
    .jump svg { width: 12px; height: 12px; }

    .footer {
      flex: none;
      border-top: 1px solid var(--ga-border, #1f1f1f);
      padding: var(--ga-space-3, 12px) var(--ga-space-4, 16px);
    }
  `;constructor(){super(),this._following=!0,this._observer=null}template(){return`
      <div class="shell" part="shell">
        <div class="header empty" part="header"><slot name="header"></slot></div>
        <div class="area">
          <div class="log" part="log" role="log" aria-live="polite" aria-relevant="additions"
            style="--chat-height:${o(this.attr("height","360px"))}" tabindex="0">
            <div class="placeholder" part="empty" hidden>${o(this.attr("empty-text","No messages yet."))}</div>
            <slot></slot>
          </div>
          <button class="jump" part="jump" type="button" hidden>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
              <path d="M8 3v10M3.5 8.5L8 13l4.5-4.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            Newer messages
          </button>
        </div>
        <div class="footer empty" part="footer"><slot name="footer"></slot></div>
      </div>
    `}render(){super.render();const t=this.$(".log"),e=this.$(".jump");!t||!e||(t.addEventListener("scroll",()=>this._onScroll()),e.addEventListener("click",()=>this.scrollToLatest()),this.shadowRoot.querySelectorAll("slot").forEach(a=>{a.addEventListener("slotchange",()=>this._onContentChanged())}),this._observer?.disconnect(),this._observer=new MutationObserver(()=>this._onContentChanged()),this._observer.observe(this,{childList:!0,subtree:!0,characterData:!0,attributes:!0}),this._onContentChanged(),requestAnimationFrame(()=>this._scrollToLatest({smooth:!1})))}disconnectedCallback(){this._observer?.disconnect()}_messageCount(){return[...this.children].filter(t=>t.slot!=="header"&&t.slot!=="footer").length}_onContentChanged(){const t=this.$(".placeholder");t&&(t.hidden=this._messageCount()>0);for(const e of["header","footer"]){const a=this.shadowRoot.querySelector(`slot[name="${e}"]`),r=this.$(`.${e}`);a&&r&&r.classList.toggle("empty",a.assignedNodes().length===0)}this._following&&this._scrollToLatest({smooth:!1}),this._syncJump()}_onScroll(){const t=this.$(".log");t&&(this._following=t.scrollHeight-t.scrollTop-t.clientHeight<24,this._syncJump())}_scrollToLatest({smooth:t=!0}={}){const e=this.$(".log");if(!e)return;if(t){e.scrollTop=e.scrollHeight;return}const a=e.style.scrollBehavior;e.style.scrollBehavior="auto",e.scrollTop=e.scrollHeight,e.style.scrollBehavior=a}_syncJump(){const t=this.$(".jump");t&&(t.hidden=this._following||this._messageCount()===0)}scrollToLatest(){this._following=!0,this._scrollToLatest(),this._syncJump()}}d("ga-chat",_t);
