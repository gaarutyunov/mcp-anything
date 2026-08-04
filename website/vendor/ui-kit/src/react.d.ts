/* =========================================================================
   `@gaarutyunov/ui-kit/react` — opt-in React JSX types.

   Reference this entry (once, anywhere in your app) to teach React's JSX about
   every `ga-*` element and its documented attributes:

     import "@gaarutyunov/ui-kit/react";
     // or, without emitting a runtime import:
     /// <reference types="@gaarutyunov/ui-kit/react" />

   It augments `React.JSX.IntrinsicElements`, so `<ga-card>`, `<ga-button>` …
   type-check with their attributes. This file is intentionally SEPARATE from
   the main entry so vanilla / Vue / Svelte / Solid users are unaffected.

   Boolean attributes accept `"" | boolean` (write `<ga-button loading>` or
   `loading="">`). Every element also accepts the standard React HTML props
   (className, style, ref, key, on* handlers, children).
   ========================================================================= */

// NOTE: this MUST be a value namespace import, not `import type`. A type-only
// import is elided, which detaches the `declare module "react"` augmentation
// below so it never applies when this entry is imported. Keeping a real import
// binding is what lets `import "@gaarutyunov/ui-kit/react"` register the JSX
// types in a consumer's project.
import * as React from "react";

type GaAttrs = React.DetailedHTMLProps<React.HTMLAttributes<HTMLElement>, HTMLElement>;
/** HTML boolean attribute: present (`""`/`true`) or absent (`false`). */
type Bool = "" | boolean;
/** Attributes that are numeric but serialise to strings in HTML. */
type Numish = string | number;

interface GaButtonAttrs extends GaAttrs {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md" | "lg";
  href?: string;
  download?: string | Bool;
  target?: string;
  rel?: string;
  type?: "button" | "submit" | "reset";
  name?: string;
  disabled?: Bool;
  loading?: Bool;
  block?: Bool;
}

interface GaRadioGroupAttrs extends GaAttrs {
  /** JSON: `{ id, label, href? }[]`. */
  items?: string;
  /** Selected item id (reflected). */
  value?: string;
}

interface GaCodeAttrs extends GaAttrs {
  /** Leading glyph, e.g. `"$"`. */
  prompt?: string;
  /** Render as an external link (↗) instead of a copy button. */
  href?: string;
  target?: string;
  rel?: string;
}

interface GaBreadcrumbsAttrs extends GaAttrs {
  /** JSON: `{ label, href? }[]`; the last item is the current page. */
  items?: string;
}

interface GaTableAttrs extends GaAttrs {
  /** JSON: `{ label, align?, width?, mono? }[]`. */
  columns?: string;
}

interface GaBadgeAttrs extends GaAttrs {
  color?: "default" | "blue" | "green" | "amber" | "purple" | "red";
  solid?: Bool;
  size?: "md" | "sm";
}

interface GaCardAttrs extends GaAttrs {
  interactive?: Bool;
  href?: string;
  padding?: "none" | "sm" | "md" | "lg";
}

interface GaAvatarAttrs extends GaAttrs {
  src?: string;
  name?: string;
  size?: "sm" | "md" | "lg";
  square?: Bool;
}

interface GaInputAttrs extends GaAttrs {
  label?: string;
  placeholder?: string;
  type?: string;
  value?: string;
  name?: string;
  hint?: string;
  error?: string;
  required?: Bool;
  disabled?: Bool;
}

interface GaSwitchAttrs extends GaAttrs {
  checked?: Bool;
  disabled?: Bool;
  label?: string;
}

interface GaSpinnerAttrs extends GaAttrs {
  size?: "sm" | "md" | "lg";
  color?: "" | "green" | "amber" | "purple" | "red" | "fg";
}

interface GaAlertAttrs extends GaAttrs {
  tone?: "info" | "success" | "warning" | "danger" | "neutral";
  title?: string;
  dismissible?: Bool;
}

interface GaTabsAttrs extends GaAttrs {
  /** JSON: `{ id, label }[]`. */
  tabs?: string;
  /** Active tab id (reflected). */
  active?: string;
}

interface GaNoteAttrs extends GaAttrs {
  tone?: "info" | "success" | "warning" | "error" | "neutral";
  title?: string;
}

interface GaSliderAttrs extends GaAttrs {
  min?: Numish;
  max?: Numish;
  step?: Numish;
  value?: Numish;
  label?: string;
  disabled?: Bool;
}

interface GaFileDropAttrs extends GaAttrs {
  accept?: string;
  multiple?: Bool;
  label?: string;
}

interface GaFabAttrs extends GaAttrs {
  color?: "" | "green" | "amber" | "purple" | "red";
  position?: "bottom-right" | "bottom-left" | "static";
  label?: string;
}

interface GaPanelAttrs extends GaAttrs {
  open?: Bool;
  side?: "right" | "left";
  title?: string;
}

interface GaHeaderAttrs extends GaAttrs {
  brand?: string;
  href?: string;
  static?: Bool;
}

interface GaBottomNavAttrs extends GaAttrs {
  /** JSON: `{ id, label, icon }[]`. */
  items?: string;
  active?: string;
  static?: Bool;
}

interface GaBottomSheetAttrs extends GaAttrs {
  open?: Bool;
  snap?: "peek" | "half" | "full";
}

interface GaKbdAttrs extends GaAttrs {}

interface GaIconAttrs extends GaAttrs {
  name?: string;
  size?: Numish;
}

declare module "react" {
  namespace JSX {
    interface IntrinsicElements {
      "ga-button": GaButtonAttrs;
      "ga-radio-group": GaRadioGroupAttrs;
      "ga-code": GaCodeAttrs;
      "ga-breadcrumbs": GaBreadcrumbsAttrs;
      "ga-table": GaTableAttrs;
      "ga-badge": GaBadgeAttrs;
      "ga-card": GaCardAttrs;
      "ga-avatar": GaAvatarAttrs;
      "ga-input": GaInputAttrs;
      "ga-switch": GaSwitchAttrs;
      "ga-spinner": GaSpinnerAttrs;
      "ga-alert": GaAlertAttrs;
      "ga-kbd": GaKbdAttrs;
      "ga-tabs": GaTabsAttrs;
      "ga-note": GaNoteAttrs;
      "ga-slider": GaSliderAttrs;
      "ga-file-drop": GaFileDropAttrs;
      "ga-fab": GaFabAttrs;
      "ga-panel": GaPanelAttrs;
      "ga-header": GaHeaderAttrs;
      "ga-bottom-nav": GaBottomNavAttrs;
      "ga-bottom-sheet": GaBottomSheetAttrs;
      "ga-icon": GaIconAttrs;
      "ga-select": GaSelectAttrs;
      "ga-calendar": GaCalendarAttrs;
      "ga-date-input": GaDateInputAttrs;
      "ga-chart-frame": GaChartFrameAttrs;
      "ga-chat-message": GaChatMessageAttrs;
      "ga-chat": GaChatAttrs;
    }
  }
}

export {};
