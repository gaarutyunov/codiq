var e=class extends HTMLElement{static styles=``;static observed=[];static get observedAttributes(){return this.observed}static get _css(){return Object.prototype.hasOwnProperty.call(this,`_cssCache`)||(this._cssCache=`
  :host {
    box-sizing: border-box;
    font-family: var(--ga-font-sans, ui-sans-serif, system-ui, sans-serif);
  }
  :host([hidden]) { display: none !important; }
  *, *::before, *::after { box-sizing: inherit; }
  @media (prefers-reduced-motion: reduce) {
    * { transition-duration: 0.001ms !important; animation-duration: 0.001ms !important; }
  }
`+(this.styles||``)),this._cssCache}constructor(){super(),this.attachShadow({mode:`open`,delegatesFocus:!0}),this._mounted=!1}connectedCallback(){this._mounted=!0,this.render()}attributeChangedCallback(){this._mounted&&this.render()}template(){return``}render(){this.shadowRoot.innerHTML=`<style>`+this.constructor._css+`</style>`+this.template()}$(e){return this.shadowRoot.querySelector(e)}emit(e,t){this.dispatchEvent(new CustomEvent(e,{detail:t,bubbles:!0,composed:!0}))}hasFlag(e){return this.hasAttribute(e)}attr(e,t=``){return this.getAttribute(e)??t}};function t(e,t){customElements.get(e)||customElements.define(e,t)}function n(e){return String(e??``).replace(/&/g,`&amp;`).replace(/</g,`&lt;`).replace(/>/g,`&gt;`).replace(/"/g,`&quot;`)}t(`ga-button`,class extends e{static observed=[`variant`,`size`,`href`,`download`,`target`,`rel`,`type`,`name`,`aria-label`,`disabled`,`loading`,`block`];static styles=`
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
  `;connectedCallback(){super.connectedCallback(),this.addEventListener(`click`,this._guard,!0)}disconnectedCallback(){this.removeEventListener(`click`,this._guard,!0)}_guard=e=>{(this.hasFlag(`disabled`)||this.hasFlag(`loading`))&&(e.stopImmediatePropagation(),e.preventDefault())};_pass(e,t=e){return this.hasAttribute(e)?` ${t}="${n(this.getAttribute(e))}"`:``}template(){let e=this.attr(`href`),t=e?`a`:`button`,r=this._pass(`aria-label`);return`
      <${t} class="btn" part="button" ${e?`href="${n(e)}"`+this._pass(`download`)+this._pass(`target`)+this._pass(`rel`)+r:`type="${n(this.attr(`type`,`button`))}"`+this._pass(`name`)+r+(this.hasFlag(`disabled`)?` disabled`:``)}>
        <slot name="start"></slot>
        ${this.hasFlag(`loading`)?`<span class="spinner" aria-hidden="true"></span>`:``}
        <slot></slot>
        <slot name="end"></slot>
      </${t}>
    `}}),t(`ga-radio-group`,class extends e{static formAssociated=!0;static observed=[`items`,`value`];static styles=`
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
  `;constructor(){super(),this._internals=this.attachInternals?.()}_parse(){try{return JSON.parse(this.attr(`items`,`[]`))}catch{return[]}}template(){let e=this._parse(),t=this.attr(`value`)||e[0]?.id;return`<div class="group" part="group" role="radiogroup">${e.map(e=>{let r=e.id===t,i=r?`0`:`-1`;return e.href?`<a class="item" part="item" data-id="${n(e.id)}"
          href="${n(e.href)}" role="radio"
          aria-checked="${r}"
          ${r?`aria-current="page"`:``}
          tabindex="${i}">${n(e.label)}</a>`:`<button class="item" part="item" type="button" data-id="${n(e.id)}"
        role="radio" aria-checked="${r}" tabindex="${i}">${n(e.label)}</button>`}).join(``)}</div>`}render(){super.render(),this._internals?.setFormValue(this.value);let e=[...this.shadowRoot.querySelectorAll(`.item`)];e.forEach(t=>{t.tagName===`BUTTON`&&t.addEventListener(`click`,()=>this._select(t.dataset.id)),t.addEventListener(`keydown`,t=>this._onKey(t,e))})}_onKey(e,t){let n={ArrowRight:1,ArrowDown:1,ArrowLeft:-1,ArrowUp:-1}[e.key];if(!n)return;e.preventDefault();let r=t[(t.indexOf(e.currentTarget)+n+t.length)%t.length];r&&(r.focus(),r.tagName===`BUTTON`&&this._select(r.dataset.id))}_select(e){e==null||e===this.attr(`value`)||(this.setAttribute(`value`,e),this.emit(`change`,{value:e}))}get value(){return this.attr(`value`)||this._parse()[0]?.id||``}set value(e){this.setAttribute(`value`,e)}});var r=class extends e{static observed=[`prompt`,`href`,`target`,`rel`];static styles=`
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
  `;_pass(e,t=e){return this.hasAttribute(e)?` ${t}="${n(this.getAttribute(e))}"`:``}template(){let e=this.attr(`href`),t=this.attr(`prompt`),r=t?`<span class="prompt" part="prompt" aria-hidden="true">${n(t)}</span>`:``,a=`<code class="text" part="text"><slot></slot></code>`;return e?`
        <a class="block" part="block"
          href="${n(e)}"${this._pass(`target`)}${this._pass(`rel`)}>
          ${r}
          ${a}
          <span class="arrow" part="arrow" aria-hidden="true">${o}</span>
        </a>
      `:`
      <div class="block" part="block">
        ${r}
        ${a}
        <button class="action" part="copy" type="button" aria-label="Copy to clipboard">
          <span class="ico">${i}</span>
        </button>
      </div>
    `}render(){super.render();let e=this.$(`button.action`);e?.addEventListener(`click`,()=>this._copy(e))}_copy(e){let t=(this.textContent||``).trim(),n=()=>{e.classList.add(`copied`),e.querySelector(`.ico`).innerHTML=a,this.emit(`copy`,{text:t}),clearTimeout(this._t),this._t=setTimeout(()=>{e.classList.remove(`copied`);let t=e.querySelector(`.ico`);t&&(t.innerHTML=i)},1500)};navigator.clipboard?.writeText?navigator.clipboard.writeText(t).then(n).catch(()=>{}):n()}disconnectedCallback(){clearTimeout(this._t)}},i=`<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <rect x="9" y="9" width="13" height="13" rx="2"></rect>
  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>`,a=`<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
  <path d="M20 6 9 17l-5-5"></path></svg>`,o=`<svg width="14" height="14" viewBox="0 0 24 24" fill="none"
  stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
  <path d="M7 17 17 7"></path><path d="M7 7h10v10"></path></svg>`;t(`ga-code`,r),t(`ga-breadcrumbs`,class extends e{static observed=[`items`];static styles=`
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
  `;_parse(){try{return JSON.parse(this.attr(`items`,`[]`))}catch{return[]}}template(){let e=this._parse(),t=e.length-1;return`<nav aria-label="Breadcrumb" part="nav"><ol part="list">${e.map((e,r)=>{let i=r>0?`<span class="sep" part="separator" aria-hidden="true">/</span>`:``,a=r===t,o=n(e.label);return`<li>${i}${!a&&e.href?`<a part="link" href="${n(e.href)}">${o}</a>`:`<span class="current" part="current" ${a?`aria-current="page"`:``}>${o}</span>`}</li>`}).join(``)}</ol></nav>`}});var s=0;t(`ga-table`,class extends e{static observed=[`columns`];static styles=`
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
  `;connectedCallback(){this._scope=`ga-table-`+s++,this.setAttribute(`data-ga-scope`,this._scope),super.connectedCallback()}disconnectedCallback(){this._sheet?.remove(),this._sheet=null}_parse(){try{return JSON.parse(this.attr(`columns`,`[]`))}catch{return[]}}template(){let e=this._parse(),t=e.map(e=>e.width||`minmax(0, 1fr)`).join(` `)||`1fr`;this.style.setProperty(`--ga-table-cols`,t);let r=e.map(e=>`<span class="${`cell`+(e.mono?` mono`:``)}" part="head-cell" style="${e.align?`text-align:${n(e.align)}`:``}">${n(e.label??``)}</span>`).join(``);return this._applyCellRules(e),`
      <div class="table" part="table">
        <div class="head" part="header" role="row">${r}</div>
        <slot part="body"></slot>
      </div>
    `}_applyCellRules(e){let t=e.map((e,t)=>{let n=[];return e.align&&n.push(`text-align:${e.align}`),e.mono&&n.push(`font-family:var(--ga-font-mono, ui-monospace, monospace);font-variant-numeric:tabular-nums`),n.length?`ga-table[data-ga-scope="${this._scope}"] > *:not([slot]) > :nth-child(${t+1}){${n.join(`;`)}}`:``}).filter(Boolean).join(`
`);this._sheet||(this._sheet=document.createElement(`style`),document.head.appendChild(this._sheet)),this._sheet.textContent=t}}),t(`ga-badge`,class extends e{static observed=[`color`,`solid`,`size`];static styles=`
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
  `;template(){return`<span class="badge" part="badge"><slot></slot></span>`}}),t(`ga-card`,class extends e{static observed=[`interactive`,`href`,`padding`];static styles=`
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
  `;connectedCallback(){super.connectedCallback(),this._sync=()=>this._toggleSlots(),this.shadowRoot.addEventListener(`slotchange`,this._sync)}_toggleSlots(){for(let e of[`header`,`footer`]){let t=this.$(`slot[name="${e}"]`),n=this.$(`.${e}`);t&&n&&n.classList.toggle(`show`,t.assignedNodes().length>0)}}template(){let e=this.attr(`href`),t=e?`a`:`div`;return`
      <${t} class="card" part="card" ${e?`href="${e}"`:``}>
        <div class="header" part="header"><slot name="header"></slot></div>
        <div class="body" part="body"><slot></slot></div>
        <div class="footer" part="footer"><slot name="footer"></slot></div>
      </${t}>
    `}}),t(`ga-avatar`,class extends e{static observed=[`src`,`name`,`size`,`square`];static styles=`
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
  `;_initials(e){return e.trim().split(/\s+/).slice(0,2).map(e=>e[0]?.toUpperCase()??``).join(``)||`?`}template(){let e=this.attr(`src`),t=this.attr(`name`,``),r=e?`<img src="${n(e)}" alt="${n(t)}" loading="lazy" />`:`<span aria-hidden="true">${n(this._initials(t))}</span>`;return`<div class="avatar" part="avatar" role="img" aria-label="${n(t)}">${r}</div>`}}),t(`ga-input`,class extends e{static formAssociated=!0;static observed=[`label`,`placeholder`,`type`,`value`,`name`,`hint`,`error`,`disabled`,`required`];static styles=`
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
  `;constructor(){super(),this._internals=this.attachInternals?.()}template(){let e=this.attr(`label`),t=this.attr(`error`),r=this.attr(`hint`),i=this.hasFlag(`required`)?`<span class="req">*</span>`:``;return`
      <div class="field">
        ${e?`<label part="label">${n(e)}${i}</label>`:``}
        <input
          part="input"
          type="${n(this.attr(`type`,`text`))}"
          placeholder="${n(this.attr(`placeholder`))}"
          value="${n(this.attr(`value`))}"
          ${this.hasFlag(`disabled`)?`disabled`:``}
          ${this.hasFlag(`required`)?`required`:``}
          aria-invalid="${t?`true`:`false`}"
        />
        ${t?`<span class="error" part="error">${n(t)}</span>`:r?`<span class="hint" part="hint">${n(r)}</span>`:``}
      </div>
    `}render(){super.render();let e=this.$(`input`);e&&(e.addEventListener(`input`,()=>{this._value=e.value,this._internals?.setFormValue(e.value),this.emit(`input`,{value:e.value})}),e.addEventListener(`change`,()=>this.emit(`change`,{value:e.value})))}get value(){return this.$(`input`)?.value??this._value??this.attr(`value`)}set value(e){this._value=e,this.setAttribute(`value`,e)}}),t(`ga-switch`,class extends e{static formAssociated=!0;static observed=[`checked`,`disabled`,`label`];static styles=`
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
  `;template(){let e=this.hasFlag(`checked`),t=this.attr(`label`);return`
      <label class="wrap">
        <button
          part="track"
          type="button"
          role="switch"
          aria-checked="${e}"
          ${this.hasFlag(`disabled`)?`disabled`:``}
        ><span class="knob" part="knob"></span></button>
        ${t?`<span class="label">${n(t)}</span>`:``}
      </label>
    `}render(){super.render(),this.$(`button`)?.addEventListener(`click`,()=>this.toggle())}toggle(){if(this.hasFlag(`disabled`))return;let e=!this.hasFlag(`checked`);this.toggleAttribute(`checked`,e),this.emit(`change`,{checked:e})}get checked(){return this.hasFlag(`checked`)}set checked(e){this.toggleAttribute(`checked`,!!e)}}),t(`ga-spinner`,class extends e{static observed=[`size`,`color`];static styles=`
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
  `;template(){return`<div class="spinner" part="spinner" role="status" aria-label="Loading"></div>`}}),t(`ga-alert`,class extends e{static observed=[`tone`,`title`,`dismissible`];static styles=`
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
  `;template(){let e=this.attr(`title`),t=this.hasFlag(`dismissible`)?`<button class="close" part="close" aria-label="Dismiss">&times;</button>`:``;return`
      <div class="alert" part="alert" role="alert">
        <span class="dot" aria-hidden="true"></span>
        <div class="content">
          ${e?`<div class="title" part="title">${n(e)}</div>`:``}
          <slot></slot>
        </div>
        ${t}
      </div>
    `}render(){super.render(),this.$(`.close`)?.addEventListener(`click`,()=>{this.emit(`dismiss`),this.remove()})}}),t(`ga-kbd`,class extends e{static styles=`
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
  `;template(){return`<kbd part="kbd"><slot></slot></kbd>`}}),t(`ga-tabs`,class extends e{static observed=[`tabs`,`active`];static styles=`
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
  `;_parse(){try{return JSON.parse(this.attr(`tabs`,`[]`))}catch{return[]}}template(){let e=this._parse(),t=this.attr(`active`)||e[0]?.id;return`
      <div class="list" part="list" role="tablist">${e.map(e=>`
      <button class="tab" part="tab" role="tab" data-id="${n(e.id)}"
        aria-selected="${e.id===t}" tabindex="${e.id===t?`0`:`-1`}">
        ${n(e.label)}
      </button>`).join(``)}</div>
      <div class="panels" part="panels">${e.map(e=>`
      <div role="tabpanel" ${e.id===t?``:`hidden`}>
        <slot name="${n(e.id)}"></slot>
      </div>`).join(``)}</div>
    `}render(){super.render(),this.shadowRoot.querySelectorAll(`.tab`).forEach(e=>{e.addEventListener(`click`,()=>this._select(e.dataset.id))})}_select(e){e!==this.attr(`active`)&&(this.setAttribute(`active`,e),this.emit(`change`,{id:e}))}}),t(`ga-note`,class extends e{static observed=[`tone`,`title`];static styles=`
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
  `;template(){let e=this.attr(`title`);return`
      <div class="note" part="note">
        ${e?`<div class="title" part="title">${n(e)}</div>`:``}
        <slot></slot>
      </div>
    `}});var c={compass:`<circle cx="12" cy="12" r="10"/><polygon points="16.24 7.76 14.12 14.12 7.76 16.24 9.88 9.88 16.24 7.76"/>`,bookmark:`<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>`,star:`<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>`,heart:`<path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>`,plus:`<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`,minus:`<line x1="5" y1="12" x2="19" y2="12"/>`,check:`<polyline points="20 6 9 17 4 12"/>`,x:`<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>`,trash:`<polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>`,bell:`<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>`,user:`<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>`,home:`<path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/>`,search:`<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>`,settings:`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>`,menu:`<line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="18" x2="21" y2="18"/>`,"chevron-right":`<polyline points="9 18 15 12 9 6"/>`,"chevron-down":`<polyline points="6 9 12 15 18 9"/>`,"arrow-right":`<line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/>`,"external-link":`<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>`,upload:`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>`,download:`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>`,image:`<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>`,info:`<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>`,layers:`<polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/>`,sun:`<circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>`,moon:`<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>`,github:`<path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"/>`};Object.keys(c),t(`ga-icon`,class extends e{static observed=[`name`,`size`];static styles=`
    :host { display: inline-flex; line-height: 0; }
    svg {
      display: block;
      stroke: currentColor; fill: none;
      stroke-width: 2; stroke-linecap: round; stroke-linejoin: round;
    }
  `;template(){let e=c[this.attr(`name`)]||``,t=Number(this.attr(`size`))||20;return`<svg viewBox="0 0 24 24" width="${t}" height="${t}" part="svg" aria-hidden="true">${e}</svg>`}}),t(`ga-file-drop`,class extends e{static observed=[`accept`,`multiple`,`label`];static styles=`
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
  `;template(){return`
      <label class="drop" part="drop">
        <ga-icon class="icon" name="upload" size="24"></ga-icon>
        <span class="label">${n(this.attr(`label`,`Drop files here or click to browse`))}</span>
        <span class="hint"><slot></slot></span>
        <input type="file" ${this.hasFlag(`multiple`)?`multiple`:``} accept="${n(this.attr(`accept`))}" />
      </label>
    `}render(){super.render();let e=this.$(`.drop`),t=this.$(`input`);t.addEventListener(`change`,()=>this._emit(t.files)),[`dragenter`,`dragover`].forEach(t=>e.addEventListener(t,t=>{t.preventDefault(),e.classList.add(`dragging`)})),[`dragleave`,`dragend`,`drop`].forEach(t=>e.addEventListener(t,t=>{t.preventDefault(),e.classList.remove(`dragging`)})),e.addEventListener(`drop`,e=>{e.dataTransfer?.files?.length&&this._emit(e.dataTransfer.files)})}_emit(e){e&&e.length&&this.emit(`files`,{files:Array.from(e)})}}),t(`ga-fab`,class extends e{static observed=[`color`,`position`,`label`];static styles=`
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
      <button class="fab" part="fab" aria-label="${n(this.attr(`label`,`Action`))}">
        <slot>+</slot>
      </button>
    `}}),t(`ga-panel`,class extends e{static observed=[`open`,`side`,`title`];static styles=`
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
  `;template(){return`
      <div class="scrim" part="scrim"></div>
      <div class="panel" part="panel" role="dialog" aria-modal="true">
        <div class="head" part="header">
          <span class="title"><slot name="header">${n(this.attr(`title`))}</slot></span>
          <button class="close" aria-label="Close">&times;</button>
        </div>
        <div class="body" part="body"><slot></slot></div>
        <div class="foot" part="footer"><slot name="footer"></slot></div>
      </div>
    `}connectedCallback(){super.connectedCallback(),this._key=e=>{e.key===`Escape`&&this.open&&this.close()},document.addEventListener(`keydown`,this._key),this.shadowRoot.addEventListener(`slotchange`,()=>this._syncFooter())}disconnectedCallback(){this._key&&document.removeEventListener(`keydown`,this._key)}render(){super.render(),this.$(`.close`)?.addEventListener(`click`,()=>this.close()),this.$(`.scrim`)?.addEventListener(`click`,()=>this.close()),this._syncFooter()}_syncFooter(){let e=this.$(`slot[name="footer"]`),t=this.$(`.foot`);e&&t&&t.classList.toggle(`show`,e.assignedNodes().length>0)}get open(){return this.hasFlag(`open`)}set open(e){this.toggleAttribute(`open`,!!e)}show(){this.open||(this.setAttribute(`open`,``),this.emit(`open`))}close(){this.open&&(this.removeAttribute(`open`),this.emit(`close`))}toggle(){this.open?this.close():this.show()}}),t(`ga-slider`,class extends e{static formAssociated=!0;static observed=[`min`,`max`,`step`,`value`,`label`,`disabled`];static styles=`
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
  `;constructor(){super(),this._internals=this.attachInternals?.()}template(){let e=this.attr(`label`),t=this.attr(`value`,`50`);return`
      <div class="wrap">
        ${e?`<div class="top"><span class="label">${n(e)}</span><span class="val">${n(t)}</span></div>`:``}
        <input type="range"
          min="${n(this.attr(`min`,`0`))}"
          max="${n(this.attr(`max`,`100`))}"
          step="${n(this.attr(`step`,`1`))}"
          value="${n(t)}"
          ${this.hasFlag(`disabled`)?`disabled`:``} />
      </div>
    `}render(){super.render();let e=this.$(`input`),t=this.$(`.val`);e&&(this._internals?.setFormValue(e.value),e.addEventListener(`input`,()=>{this._value=e.value,t&&(t.textContent=e.value),this._internals?.setFormValue(e.value),this.emit(`input`,{value:e.value})}),e.addEventListener(`change`,()=>this.emit(`change`,{value:e.value})))}get value(){return this.$(`input`)?.value??this._value??this.attr(`value`)}set value(e){this._value=e,this.setAttribute(`value`,e)}}),t(`ga-header`,class extends e{static observed=[`brand`,`href`,`static`];static styles=`
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
  `;template(){let e=this.attr(`href`),t=e?`a`:`div`;return`
      <header class="hdr" part="header">
        <${t} class="brand" part="brand" ${e?`href="${n(e)}"`:``}><slot name="brand">${n(this.attr(`brand`))}</slot></${t}>
        <div class="spacer"></div>
        <nav class="actions" part="actions"><slot></slot></nav>
      </header>
    `}}),t(`ga-bottom-sheet`,class extends e{static observed=[`open`,`snap`];static styles=`
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
    `}connectedCallback(){super.connectedCallback(),this._onResize=()=>this._apply(),window.addEventListener(`resize`,this._onResize),this._onMove=e=>this._move(e),this._onUp=()=>this._up(),window.addEventListener(`pointermove`,this._onMove),window.addEventListener(`pointerup`,this._onUp)}disconnectedCallback(){window.removeEventListener(`resize`,this._onResize),window.removeEventListener(`pointermove`,this._onMove),window.removeEventListener(`pointerup`,this._onUp)}attributeChangedCallback(){this._mounted&&this._apply()}render(){super.render();let e=this.$(`.grip`),t=this.$(`.head`);for(let n of[e,t])n?.addEventListener(`pointerdown`,e=>this._down(e));requestAnimationFrame(()=>this._apply())}get open(){return this.hasFlag(`open`)}get snap(){return this.attr(`snap`,`half`)}show(e){e&&this.setAttribute(`snap`,e),this.setAttribute(`open`,``),this._apply(),this.emit(`open`)}close(){this.removeAttribute(`open`),this._apply(),this.emit(`close`)}snapTo(e){this.setAttribute(`snap`,e),this._apply(),this.emit(`snapchange`,{snap:e})}_snaps(){let e=this.$(`.sheet`)?.offsetHeight||window.innerHeight*.88,t=window.innerHeight;return{full:0,half:Math.max(0,e-t*.45),peek:Math.max(0,e-128),closed:e}}_currentY(){let e=/translateY\(([-0-9.]+)px\)/.exec(this.$(`.sheet`)?.style.transform||``);return e?parseFloat(e[1]):this._snaps().closed}_apply(){let e=this.$(`.sheet`);if(!e)return;let t=this._snaps(),n=this.open?t[this.snap]??t.half:t.closed;e.style.transform=`translateY(${n}px)`}_down(e){this._dragging=!0,this._startY=e.clientY,this._startTf=this._currentY(),this.$(`.sheet`)?.classList.add(`dragging`)}_move(e){if(!this._dragging)return;let t=this._snaps(),n=Math.min(t.closed,Math.max(0,this._startTf+(e.clientY-this._startY)));this.$(`.sheet`).style.transform=`translateY(${n}px)`}_up(){if(!this._dragging)return;this._dragging=!1,this.$(`.sheet`)?.classList.remove(`dragging`);let e=this._snaps(),t=this._currentY();if(t>e.peek+80){this.close();return}let n=`full`;for(let r of[`full`,`half`,`peek`])Math.abs(e[r]-t)<Math.abs(e[n]-t)&&(n=r);n!==this.snap&&(this.setAttribute(`snap`,n),this.emit(`snapchange`,{snap:n})),this._apply()}}),t(`ga-bottom-nav`,class extends e{static observed=[`items`,`active`];static styles=`
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
  `;_parse(){try{return JSON.parse(this.attr(`items`,`[]`))}catch{return[]}}template(){let e=this._parse(),t=this.attr(`active`)||e[0]?.id;return`<nav class="nav" part="nav" role="navigation">${e.map(e=>{let r=e.icon||``,i=/^[a-z][a-z0-9-]*$/.test(r)?`<ga-icon class="icon" name="${n(r)}" size="22"></ga-icon>`:`<span class="icon" aria-hidden="true">${n(r||`•`)}</span>`;return`
      <button class="item" part="item" data-id="${n(e.id)}"
        ${e.id===t?`aria-current="page"`:``}>
        ${i}
        <span class="label">${n(e.label)}</span>
      </button>`}).join(``)}</nav>`}render(){super.render(),this.shadowRoot.querySelectorAll(`.item`).forEach(e=>e.addEventListener(`click`,()=>this._select(e.dataset.id)))}_select(e){e!==this.attr(`active`)&&(this.setAttribute(`active`,e),this.emit(`change`,{id:e}))}});var l=typeof HTMLElement<`u`&&Object.prototype.hasOwnProperty.call(HTMLElement.prototype,`popover`),u=4;function d(e,t,n={}){let r=!1,i=n.onDismiss||(()=>{});l&&t.setAttribute(`popover`,`manual`);function a(){let n=e.getBoundingClientRect(),r=t.offsetHeight||0,i=window.innerHeight-n.bottom,a=i<r+u&&n.top>i;t.style.minWidth=`${n.width}px`,l?(t.style.position=`fixed`,t.style.left=`${n.left}px`,t.style.top=a?`auto`:`${n.bottom+u}px`,t.style.bottom=a?`${window.innerHeight-n.top+u}px`:`auto`,t.style.margin=`0`):(t.style.position=`absolute`,t.style.left=`0`,t.style.top=a?`auto`:`100%`,t.style.bottom=a?`100%`:`auto`,t.style.marginTop=a?`0`:`${u}px`,t.style.marginBottom=a?`${u}px`:`0`),t.dataset.placement=a?`top`:`bottom`}function o(n){let r=n.composedPath();r.includes(t)||r.includes(e)||m(`outside`)}function s(e){e.key===`Escape`&&(e.stopPropagation(),m(`escape`))}function c(){let t=e.getBoundingClientRect();if(!(t.bottom>0&&t.top<window.innerHeight&&t.right>0&&t.left<window.innerWidth)){m(`scroll`);return}a()}function d(e){let t=e?`addEventListener`:`removeEventListener`;document[t](`pointerdown`,o,!0),document[t](`keydown`,s,!0),window[t](`scroll`,c,!0),window[t](`resize`,c)}function f(){if(!r){if(r=!0,t.hidden=!1,l)try{t.showPopover()}catch{}a(),d(!0)}}function p(){if(r){if(r=!1,d(!1),l)try{t.hidePopover()}catch{}t.hidden=!0}}function m(e){p(),i(e)}return{show:f,close:p,reposition:a,get open(){return r},destroy(){r&&p()}}}var f=class extends e{static formAssociated=!0;static observed=[`options`,`value`,`multiple`,`filterable`,`placeholder`,`label`,`hint`,`error`,`name`,`disabled`,`required`];static styles=`
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
  `;constructor(){super(),this._internals=this.attachInternals?.(),this._open=!1,this._active=-1,this._filterText=``,this._typeahead=``,this._typeaheadAt=0,this._filterTimer=0,this._popup=null,this._values=null,this._reflecting=!1}attributeChangedCallback(e,t,n){e===`value`&&!this._reflecting&&(this._values=null),super.attributeChangedCallback(e,t,n)}_allOptions(){let e=this.getAttribute(`options`);if(e)try{let t=JSON.parse(e);if(Array.isArray(t))return t.map(m)}catch{}return[...this.querySelectorAll(`option`)].map(e=>({value:e.value??e.textContent.trim(),label:e.textContent.trim(),disabled:e.disabled}))}_visibleOptions(){let e=this._allOptions(),t=this._filterText.trim().toLowerCase();return t?e.filter(e=>e.label.toLowerCase().includes(t)):e}get multiple(){return this.hasFlag(`multiple`)}_selected(){if(this._values)return this._values;let e=this.attr(`value`);return e?this.multiple?e.split(`,`).filter(Boolean):[e]:[]}_setSelected(e){this._values=e,this._reflecting=!0,this.setAttribute(`value`,e.join(`,`)),this._reflecting=!1}_summary(){let e=this._selected(),t=this._allOptions();if(!e.length)return`<span class="placeholder">${n(this.attr(`placeholder`,`Select…`))}</span>`;if(this.multiple&&e.length>1)return`<span>${e.length} selected</span>`;let r=t.find(t=>t.value===e[0]);return`<span>${n(r?r.label:e[0])}</span>`}template(){let e=this.attr(`label`),t=this.attr(`error`),r=this.attr(`hint`),i=this.hasFlag(`required`)?`<span class="req">*</span>`:``,a=this.hasFlag(`disabled`);return`
      <div class="field">
        ${e?`<label part="label" id="lbl">${n(e)}${i}</label>`:``}
        <button class="trigger" part="trigger" type="button"
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded="${this._open}"
          aria-controls="listbox"
          ${e?`aria-labelledby="lbl"`:``}
          aria-invalid="${t?`true`:`false`}"
          ${a?`disabled`:``}>
          ${this._summary()}
          <svg class="caret" viewBox="0 0 16 16" aria-hidden="true" fill="none"
            stroke="currentColor" stroke-width="1.5">
            <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <div class="panel" part="panel" id="panel" ${this._open?``:`hidden`}>
          ${this.hasFlag(`filterable`)?`<input class="filter" part="filter" type="text" autocomplete="off"
                 placeholder="Filter…" aria-label="Filter options"
                 value="${n(this._filterText)}" />`:``}
          <div id="listbox" role="listbox"
            aria-multiselectable="${this.multiple}"
            ${e?`aria-labelledby="lbl"`:``}>${this._rows()}</div>
        </div>
        ${t?`<span class="error" part="error">${n(t)}</span>`:r?`<span class="hint" part="hint">${n(r)}</span>`:``}
      </div>
      <slot hidden></slot>
    `}_rows(){let e=this._visibleOptions();if(!e.length)return`<div class="empty" role="option" aria-disabled="true">No matches</div>`;let t=this._selected();return e.map((e,r)=>{let i=t.includes(e.value);return`<div class="opt${r===this._active?` active`:``}"
          part="option" role="option" id="opt-${r}" data-value="${n(e.value)}"
          aria-selected="${i}"
          ${e.disabled?`aria-disabled="true"`:``}>
          <svg class="tick" viewBox="0 0 16 16" aria-hidden="true" fill="none"
            stroke="currentColor" stroke-width="2">
            <path d="M3 8.5l3.5 3.5L13 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span>${n(e.label)}</span>
        </div>`}).join(``)}render(){super.render(),this._internals?.setFormValue(this._formValue());let e=this.$(`.trigger`),t=this.$(`.panel`);if(!e||!t)return;this._popup?.destroy(),this._popup=d(e,t,{onDismiss:()=>{this._open=!1,this._active=-1,this._syncOpenState(),e.focus()}}),this._open&&this._popup.show(),e.addEventListener(`click`,()=>this._toggle()),e.addEventListener(`keydown`,e=>this._onTriggerKey(e));let n=this.$(`.filter`);n?.addEventListener(`input`,()=>this._onFilterInput(n.value)),n?.addEventListener(`keydown`,e=>this._onTriggerKey(e)),this._bindRows(),this.$(`slot`)?.addEventListener(`slotchange`,()=>this._onSlotChange())}_onSlotChange(){let e=this.$(`.trigger`);if(e){let t=e.querySelector(`.caret`);e.innerHTML=this._summary()+(t?t.outerHTML:``)}this._repaintRows()}disconnectedCallback(){this._popup?.destroy(),clearTimeout(this._filterTimer)}_bindRows(){this.shadowRoot.querySelectorAll(`.opt`).forEach(e=>{e.addEventListener(`click`,()=>{e.getAttribute(`aria-disabled`)!==`true`&&this._commit(e.dataset.value)}),e.addEventListener(`pointerdown`,e=>e.preventDefault())})}_repaintRows(){let e=this.$(`#listbox`);e&&(e.innerHTML=this._rows(),this._bindRows(),this._syncActive(),this._popup?.reposition())}_toggle(){this.hasFlag(`disabled`)||(this._open?this._close():this._openPanel())}_openPanel(){if(this._open)return;this._open=!0;let e=this._selected(),t=this._visibleOptions();this._active=Math.max(0,t.findIndex(t=>e.includes(t.value))),this._syncOpenState(),this._popup?.show(),this._repaintRows();let n=this.$(`.filter`);n&&n.focus()}_close({focusTrigger:e=!0}={}){this._open&&(this._open=!1,this._active=-1,this._popup?.close(),this._syncOpenState(),e&&this.$(`.trigger`)?.focus())}_syncOpenState(){let e=this.$(`.trigger`),t=this.$(`.panel`);e?.setAttribute(`aria-expanded`,String(this._open)),t&&(t.hidden=!this._open),this._syncActive()}_syncActive(){let e=this.$(`.trigger`),t=[...this.shadowRoot.querySelectorAll(`.opt`)];t.forEach((e,t)=>e.classList.toggle(`active`,t===this._active));let n=t[this._active];this._open&&n?(e?.setAttribute(`aria-activedescendant`,n.id),n.scrollIntoView({block:`nearest`})):e?.removeAttribute(`aria-activedescendant`)}_onTriggerKey(e){let t=this._visibleOptions();if(!this._open){([`Enter`,` `,`ArrowDown`,`ArrowUp`].includes(e.key)||e.altKey&&e.key===`ArrowDown`)&&(e.preventDefault(),this._openPanel());return}switch(e.key){case`Escape`:e.preventDefault(),this._close();return;case`Tab`:{let e=p(t,this._active);e?this._commit(e.value,{keepOpen:!1}):this._close({focusTrigger:!1});return}case`Enter`:{e.preventDefault();let n=p(t,this._active);n&&this._commit(n.value);return}case` `:{if(this.hasFlag(`filterable`)&&e.target===this.$(`.filter`))return;e.preventDefault();let n=p(t,this._active);n&&this._commit(n.value);return}case`ArrowDown`:e.preventDefault(),this._move(1,t);return;case`ArrowUp`:e.preventDefault(),this._move(-1,t);return;case`Home`:e.preventDefault(),this._moveTo(0,t,1);return;case`End`:e.preventDefault(),this._moveTo(t.length-1,t,-1);return;case`PageDown`:e.preventDefault(),this._moveTo(Math.min(t.length-1,this._active+10),t,-1);return;case`PageUp`:e.preventDefault(),this._moveTo(Math.max(0,this._active-10),t,1);return;default:break}!this.hasFlag(`filterable`)&&e.key.length===1&&!e.metaKey&&!e.ctrlKey&&this._onTypeahead(e.key,t)}_move(e,t){if(!t.length)return;let n=this._active;for(let r=0;r<t.length;r++)if(n=(n+e+t.length)%t.length,!t[n].disabled){this._active=n,this._syncActive();return}}_moveTo(e,t,n){if(!t.length)return;let r=Math.max(0,Math.min(t.length-1,e));for(let e=0;e<t.length;e++){if(!t[r].disabled){this._active=r,this._syncActive();return}r=(r+n+t.length)%t.length}}_onTypeahead(e,t){let n=Date.now();this._typeahead=n-this._typeaheadAt>800?e:this._typeahead+e,this._typeaheadAt=n;let r=this._typeahead.toLowerCase(),i=t.findIndex(e=>!e.disabled&&e.label.toLowerCase().startsWith(r));i>=0&&(this._active=i,this._syncActive())}_onFilterInput(e){this._filterText=e,this._active=0,this._repaintRows(),clearTimeout(this._filterTimer),this._filterTimer=setTimeout(()=>this.emit(`filter`,{text:e}),200)}_commit(e,{keepOpen:t=this.multiple}={}){if(e==null)return;let n;if(this.multiple){let t=this._selected(),r=t.includes(e)?t.filter(t=>t!==e):[...t,e];this._setSelected(r),n=r}else n=e,this._setSelected([e]);this._internals?.setFormValue(this._formValue()),this.emit(`input`,{value:n}),this.emit(`change`,{value:n}),t?this._openPanel():this._close()}_formValue(){let e=this._selected();if(!this.multiple)return e[0]??``;let t=new FormData,n=this.attr(`name`);return n&&e.forEach(e=>t.append(n,e)),t}get value(){let e=this._selected();return this.multiple?e:e[0]??``}set value(e){if(Array.isArray(e)){this._setSelected(e.map(String));return}let t=String(e??``);this._setSelected(t?[t]:[])}get options(){return this._allOptions()}set options(e){this.setAttribute(`options`,JSON.stringify(e??[]))}};function p(e,t){let n=e[t];return n&&!n.disabled?n:null}function m(e){return typeof e==`string`?{value:e,label:e,disabled:!1}:{value:String(e.value??e.id??``),label:String(e.label??e.value??e.id??``),disabled:!!e.disabled}}t(`ga-select`,f);var h=class extends e{static observed=[`value`,`month`,`locale`,`first-day`,`min`,`max`,`disabled`];static styles=`
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
  `;constructor(){super(),this._focusDate=``,this._wantFocus=!1}get _locale(){return this.attr(`locale`)||void 0}get _firstDay(){let e=Number(this.attr(`first-day`,`1`));return Number.isInteger(e)&&e>=0&&e<=6?e:1}get _month(){let e=this.attr(`month`);if(/^\d{4}-\d{2}$/.test(e))return e;let t=this.attr(`value`);return g(t)?t.slice(0,7):b().slice(0,7)}_isDisabled(e){let t=this.attr(`min`),n=this.attr(`max`);return!!(g(t)&&e<t||g(n)&&e>n)}template(){let e=this._month,[t,r]=e.split(`-`).map(Number),i=this.attr(`value`),a=b(),o=new Intl.DateTimeFormat(this._locale,{month:`long`,year:`numeric`,timeZone:`UTC`}).format(_(`${e}-01`)),s=S(this._locale,this._firstDay),c=_(`${e}-01`),l=y(c,-((c.getUTCDay()-this._firstDay+7)%7)),u=``;for(let e=0;e<6;e++){let o=``;for(let s=0;s<7;s++){let c=y(l,e*7+s),u=v(c),d=c.getUTCMonth()+1!==r||c.getUTCFullYear()!==t,f=this._isDisabled(u),p=u===i,m=[`day`];d&&m.push(`outside`),u===a&&m.push(`today`),o+=`<td role="gridcell">
          <button class="${m.join(` `)}" part="day" type="button"
            data-iso="${u}"
            tabindex="${u===this._tabDate()?`0`:`-1`}"
            aria-selected="${p}"
            ${f?`aria-disabled="true"`:``}
            aria-label="${n(x(u,this._locale))}">${c.getUTCDate()}</button>
        </td>`}u+=`<tr role="row">${o}</tr>`}return`
      <div class="head">
        <button class="nav" part="prev" type="button" data-step="-1" aria-label="Previous month">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M10 3L5 8l5 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
        <div class="title" part="title" aria-live="polite">${n(o)}</div>
        <button class="nav" part="next" type="button" data-step="1" aria-label="Next month">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M6 3l5 5-5 5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </button>
      </div>
      <table role="grid" aria-label="${n(o)}">
        <thead><tr role="row">
          ${s.map(e=>`<th role="columnheader" abbr="${n(e.long)}" scope="col">${n(e.short)}</th>`).join(``)}
        </tr></thead>
        <tbody>${u}</tbody>
      </table>
    `}_tabDate(){let e=this._month,t=[this._focusDate,this.attr(`value`),b()];for(let n of t)if(g(n)&&n.slice(0,7)===e&&!this._isDisabled(n))return n;let n=new Date(Date.UTC(Number(e.slice(0,4)),Number(e.slice(5,7)),0)).getUTCDate();for(let t=1;t<=n;t++){let n=`${e}-${String(t).padStart(2,`0`)}`;if(!this._isDisabled(n))return n}return`${e}-01`}render(){super.render(),this.shadowRoot.querySelectorAll(`.nav`).forEach(e=>{e.addEventListener(`click`,()=>this._shiftMonth(Number(e.dataset.step)))}),this.shadowRoot.querySelectorAll(`.day`).forEach(e=>{e.addEventListener(`click`,()=>this._select(e.dataset.iso)),e.addEventListener(`keydown`,e=>this._onKey(e))}),this._wantFocus&&(this._wantFocus=!1,this.shadowRoot.querySelector(`.day[data-iso="${this._tabDate()}"]`)?.focus())}_shiftMonth(e){let[t,n]=this._month.split(`-`).map(Number),r=new Date(Date.UTC(t,n-1+e,1));this.setAttribute(`month`,v(r).slice(0,7))}_onKey(e){let t={ArrowLeft:-1,ArrowRight:1,ArrowUp:-7,ArrowDown:7},n=e.currentTarget.dataset.iso;if(t[e.key]!==void 0){e.preventDefault(),this._focusTo(v(y(_(n),t[e.key])));return}if(e.key===`Home`||e.key===`End`){e.preventDefault();let t=_(n),r=(t.getUTCDay()-this._firstDay+7)%7;this._focusTo(v(y(t,e.key===`Home`?-r:6-r)));return}if(e.key===`PageUp`||e.key===`PageDown`){e.preventDefault();let[t,r,i]=n.split(`-`).map(Number),a=e.key===`PageUp`?-1:1,o=new Date(Date.UTC(t,r+a,0)).getUTCDate();this._focusTo(v(new Date(Date.UTC(t,r-1+a,Math.min(i,o)))))}}_focusTo(e){this._focusDate=e,this._wantFocus=!0,e.slice(0,7)===this._month?this.render():this.setAttribute(`month`,e.slice(0,7))}_select(e){!e||this._isDisabled(e)||this.hasFlag(`disabled`)||(this._focusDate=e,this.setAttribute(`value`,e),this.emit(`change`,{value:e}))}get value(){return this.attr(`value`)}set value(e){this.setAttribute(`value`,e??``)}};function g(e){return typeof e==`string`&&/^\d{4}-\d{2}-\d{2}$/.test(e)}function _(e){let[t,n,r]=e.split(`-`).map(Number);return new Date(Date.UTC(t,(n||1)-1,r||1))}function v(e){return e.toISOString().slice(0,10)}function y(e,t){return new Date(e.getTime()+t*864e5)}function b(){let e=new Date;return v(new Date(Date.UTC(e.getFullYear(),e.getMonth(),e.getDate())))}function x(e,t){return new Intl.DateTimeFormat(t,{dateStyle:`long`,timeZone:`UTC`}).format(_(e))}function S(e,t){let n=new Intl.DateTimeFormat(e,{weekday:`short`,timeZone:`UTC`}),r=new Intl.DateTimeFormat(e,{weekday:`long`,timeZone:`UTC`}),i=Date.UTC(2024,0,7);return Array.from({length:7},(e,a)=>{let o=new Date(i+(a+t)%7*864e5);return{short:n.format(o),long:r.format(o)}})}t(`ga-calendar`,h);var C=class extends e{static formAssociated=!0;static observed=[`value`,`label`,`placeholder`,`hint`,`error`,`name`,`locale`,`min`,`max`,`first-day`,`disabled`,`required`];static styles=`
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
  `;constructor(){super(),this._internals=this.attachInternals?.(),this._open=!1,this._invalid=!1,this._popup=null}get _locale(){return this.attr(`locale`)||void 0}template(){let e=this.attr(`label`),t=this.attr(`error`),r=this.attr(`hint`),i=this.hasFlag(`required`)?`<span class="req">*</span>`:``,a=this.attr(`value`),o=this.attr(`placeholder`)||D(this._locale);return`
      <div class="field">
        ${e?`<label part="label" id="lbl">${n(e)}${i}</label>`:``}
        <div class="control" part="control">
          <input part="input" type="text" inputmode="numeric" autocomplete="off"
            value="${n(a)}"
            placeholder="${n(o)}"
            ${e?`aria-labelledby="lbl"`:``}
            aria-invalid="${t?`true`:`false`}"
            ${this.hasFlag(`disabled`)?`disabled`:``}
            ${this.hasFlag(`required`)?`required`:``} />
          <button class="open" part="open" type="button"
            aria-label="Choose date" aria-haspopup="dialog"
            aria-expanded="${this._open}"
            ${this.hasFlag(`disabled`)?`disabled`:``}>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
              <rect x="2" y="3" width="12" height="11" rx="2"/>
              <path d="M2 6.5h12M5.5 1.5v3M10.5 1.5v3" stroke-linecap="round"/>
            </svg>
          </button>
        </div>
        <div class="panel" part="panel" role="dialog" aria-label="Choose date" ${this._open?``:`hidden`}>
          <ga-calendar
            ${g(a)?`value="${n(a)}"`:``}
            ${this.attr(`min`)?`min="${n(this.attr(`min`))}"`:``}
            ${this.attr(`max`)?`max="${n(this.attr(`max`))}"`:``}
            ${this.attr(`locale`)?`locale="${n(this.attr(`locale`))}"`:``}
            ${this.attr(`first-day`)?`first-day="${n(this.attr(`first-day`))}"`:``}
          ></ga-calendar>
        </div>
        ${t?`<span class="error" part="error">${n(t)}</span>`:r?`<span class="hint" part="hint">${n(r)}</span>`:``}
      </div>
    `}render(){super.render(),this._internals?.setFormValue(this.attr(`value`));let e=this.$(`input`),t=this.$(`.open`),n=this.$(`.panel`),r=this.$(`ga-calendar`);!e||!t||!n||(this._popup?.destroy(),this._popup=d(t,n,{onDismiss:()=>{this._open=!1,this._syncOpen(),t.focus()}}),this._open&&this._popup.show(),t.addEventListener(`click`,()=>this._toggle()),e.addEventListener(`input`,()=>{let t=w(e.value.trim(),this._locale),n=t&&!this._outOfRange(t)?t:``;this.emit(`input`,{value:n,text:e.value})}),e.addEventListener(`change`,()=>this._commitTyped(e.value)),e.addEventListener(`keydown`,t=>{t.key===`Enter`&&(t.preventDefault(),this._commitTyped(e.value)),t.key===`ArrowDown`&&t.altKey&&(t.preventDefault(),this._openPanel())}),this._setInvalid(this._invalid),r?.addEventListener(`change`,e=>{e.stopPropagation(),this._commit(e.detail.value),this._close()}))}disconnectedCallback(){this._popup?.destroy()}_toggle(){this.hasFlag(`disabled`)||(this._open?this._close():this._openPanel())}_openPanel(){if(this._open||this.hasFlag(`disabled`))return;this._open=!0,this._syncOpen(),this._popup?.show();let e=this.$(`ga-calendar`),t=g(this.attr(`value`))?this.attr(`value`):b();e?.shadowRoot?.querySelector(`.day[data-iso="${t}"]`)?.focus()}_close({focusButton:e=!0}={}){this._open&&(this._open=!1,this._popup?.close(),this._syncOpen(),e&&this.$(`.open`)?.focus())}_syncOpen(){this.$(`.open`)?.setAttribute(`aria-expanded`,String(this._open));let e=this.$(`.panel`);e&&(e.hidden=!this._open)}_commitTyped(e){let t=String(e??``).trim();if(!t){this._setInvalid(!1),this._commit(``);return}let n=w(t,this._locale);if(!n||this._outOfRange(n)){this._setInvalid(!0);return}this._setInvalid(!1),this._commit(n)}_outOfRange(e){let t=this.attr(`min`),n=this.attr(`max`);return g(t)&&e<t||g(n)&&e>n}_setInvalid(e){this._invalid=e;let t=this.$(`input`);this.$(`.control`)?.classList.toggle(`invalid`,e),t?.setAttribute(`aria-invalid`,String(e||!!this.attr(`error`)));let n=this.hasFlag(`required`)&&!this.attr(`value`);e?this._internals?.setValidity?.({badInput:!0},`Enter a valid date.`,t??void 0):n?this._internals?.setValidity?.({valueMissing:!0},`Choose a date.`,t??void 0):this._internals?.setValidity?.({},``)}_commit(e){this.setAttribute(`value`,e),this._internals?.setFormValue(e),this.emit(`input`,{value:e}),this.emit(`change`,{value:e})}get value(){return this.attr(`value`)}set value(e){this.setAttribute(`value`,e??``)}};function w(e,t){let n=e.match(/^(\d{4})-(\d{1,2})-(\d{1,2})$/);if(n)return T(+n[1],+n[2],+n[3]);let r=e.split(/[^\d]+/).filter(Boolean).map(Number);if(r.length!==3||r.some(Number.isNaN))return null;let i=E(t),a={};i.forEach((e,t)=>{a[e]=r[t]});let{year:o,month:s,day:c}=a;return o==null||s==null||c==null?null:(o<100&&(o+=o<50?2e3:1900),T(o,s,c))}function T(e,t,n){if(t<1||t>12||n<1||n>31)return null;let r=new Date(Date.UTC(e,t-1,n));return r.getUTCMonth()!==t-1||r.getUTCDate()!==n?null:r.toISOString().slice(0,10)}function E(e){try{let t=new Intl.DateTimeFormat(e,{year:`numeric`,month:`2-digit`,day:`2-digit`,timeZone:`UTC`}).formatToParts(_(`2026-03-14`)).filter(e=>[`year`,`month`,`day`].includes(e.type)).map(e=>e.type);if(t.length===3)return t}catch{}return[`year`,`month`,`day`]}function D(e){let t={year:`YYYY`,month:`MM`,day:`DD`},n=E(e),r=n[0]===`year`?`-`:`/`;return n.map(e=>t[e]).join(r)}t(`ga-date-input`,C),t(`ga-chart-frame`,class extends e{static observed=[`title`,`legend`,`height`,`empty-text`,`loading`,`empty`];static styles=`
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
  `;_legend(){let e;try{e=JSON.parse(this.attr(`legend`,`[]`))}catch{return[]}return Array.isArray(e)?e.filter(e=>e!=null).map(e=>typeof e==`object`?{label:String(e.label??``),color:e.color?String(e.color):``}:{label:String(e),color:``}):[]}template(){let e=this.attr(`title`),t=this._legend(),r=this.hasFlag(`loading`),i=this.hasFlag(`empty`),a=this.attr(`height`,`180px`),o=t.map((e,t)=>`<li><span class="swatch" style="--swatch:${n(e.color||`var(--ga-chart-${t%8+1})`)}"></span>${n(e.label??``)}</li>`).join(``),s=``;return r?s=`<div class="state" part="state" role="status">
        <span class="spinner" aria-hidden="true"></span> Loading…
      </div>`:i&&(s=`<div class="state" part="state" role="status">${n(this.attr(`empty-text`,`No data`))}</div>`),`
      <figure class="frame" part="frame" style="--plot-height:${n(a)}">
        
        ${e?`<figcaption class="title" part="title">${n(e)}</figcaption>`:``}
        ${o?`<ul class="legend" part="legend">${o}</ul>`:``}
        <div class="plot" part="plot" aria-busy="${r}">
          <slot></slot>
          ${s}
        </div>
        <div class="footer" part="footer"><slot name="footer"></slot></div>
      </figure>
    `}});var O=class extends e{static observed=[`role`,`state`,`author`,`time`];static styles=`
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
  `;template(){let e=this.attr(`role`,`assistant`),t=this.attr(`state`,`sent`),r=this.attr(`author`),i=this.attr(`time`),a=k(e,t,r),o=t===`pending`?`<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>`:`<slot></slot>${t===`streaming`?`<span class="caret" aria-hidden="true"></span>`:``}`,s=t===`streaming`||t===`pending`?`aria-live="polite"`:``,c=e===`system`?`role="note"`:t===`error`?`role="alert"`:``;return`
      <div class="row" part="row">
        ${r||i?`<div class="meta" part="meta">
              ${r?`<span>${n(r)}</span>`:``}
              ${i?`<time>${n(i)}</time>`:``}
            </div>`:``}
        <div class="bubble" part="bubble" ${s} ${c}
          ${a?`aria-describedby="status"`:``}>${o}${a?`<span id="status" class="sr">${n(a)}</span>`:``}</div>
      </div>
    `}};function k(e,t,n){let r=n||{user:`You`,assistant:`Assistant`,system:`System`}[e]||e;return t===`pending`?`${r} is replying`:t===`error`?`${r}, failed to send`:``}t(`ga-chat-message`,O),t(`ga-chat`,class extends e{static observed=[`empty-text`,`height`];static styles=`
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
            style="--chat-height:${n(this.attr(`height`,`360px`))}" tabindex="0">
            <div class="placeholder" part="empty" hidden>${n(this.attr(`empty-text`,`No messages yet.`))}</div>
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
    `}render(){super.render();let e=this.$(`.log`),t=this.$(`.jump`);!e||!t||(e.addEventListener(`scroll`,()=>this._onScroll()),t.addEventListener(`click`,()=>this.scrollToLatest()),this.shadowRoot.querySelectorAll(`slot`).forEach(e=>{e.addEventListener(`slotchange`,()=>this._onContentChanged())}),this._observer?.disconnect(),this._observer=new MutationObserver(()=>this._onContentChanged()),this._observer.observe(this,{childList:!0,subtree:!0,characterData:!0,attributes:!0}),this._onContentChanged(),requestAnimationFrame(()=>this._scrollToLatest({smooth:!1})))}disconnectedCallback(){this._observer?.disconnect()}_messageCount(){return[...this.children].filter(e=>e.slot!==`header`&&e.slot!==`footer`).length}_onContentChanged(){let e=this.$(`.placeholder`);e&&(e.hidden=this._messageCount()>0);for(let e of[`header`,`footer`]){let t=this.shadowRoot.querySelector(`slot[name="${e}"]`),n=this.$(`.${e}`);t&&n&&n.classList.toggle(`empty`,t.assignedNodes().length===0)}this._following&&this._scrollToLatest({smooth:!1}),this._syncJump()}_onScroll(){let e=this.$(`.log`);e&&(this._following=e.scrollHeight-e.scrollTop-e.clientHeight<24,this._syncJump())}_scrollToLatest({smooth:e=!0}={}){let t=this.$(`.log`);if(!t)return;if(e){t.scrollTop=t.scrollHeight;return}let n=t.style.scrollBehavior;t.style.scrollBehavior=`auto`,t.scrollTop=t.scrollHeight,t.style.scrollBehavior=n}_syncJump(){let e=this.$(`.jump`);e&&(e.hidden=this._following||this._messageCount()===0)}scrollToLatest(){this._following=!0,this._scrollToLatest(),this._syncJump()}});