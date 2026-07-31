/* =========================================================================
   Ambient DOM augmentation.

   Maps every `ga-*` tag to its element class in `HTMLElementTagNameMap`, so
   `document.querySelector("ga-card")` is typed as `GaCard`, `createElement`
   returns the right class, etc. This applies to *every* consumer (vanilla,
   Vue, Svelte, Solid) — it does NOT touch React's JSX (see `./react.d.ts` for
   the opt-in React entry).

   Pulled in automatically by the package's main entry (`./index.d.ts`).
   ========================================================================= */

import type { GaButton } from "./components/button/button.js";
import type { GaRadioGroup } from "./components/radio-group/radio-group.js";
import type { GaCode } from "./components/code/code.js";
import type { GaBreadcrumbs } from "./components/breadcrumbs/breadcrumbs.js";
import type { GaTable } from "./components/table/table.js";
import type { GaBadge } from "./components/badge/badge.js";
import type { GaCard } from "./components/card/card.js";
import type { GaAvatar } from "./components/avatar/avatar.js";
import type { GaInput } from "./components/input/input.js";
import type { GaSwitch } from "./components/switch/switch.js";
import type { GaSpinner } from "./components/spinner/spinner.js";
import type { GaAlert } from "./components/alert/alert.js";
import type { GaKbd } from "./components/kbd/kbd.js";
import type { GaTabs } from "./components/tabs/tabs.js";
import type { GaNote } from "./components/note/note.js";
import type { GaFileDrop } from "./components/file-drop/file-drop.js";
import type { GaFab } from "./components/fab/fab.js";
import type { GaPanel } from "./components/panel/panel.js";
import type { GaSlider } from "./components/slider/slider.js";
import type { GaHeader } from "./components/header/header.js";
import type { GaBottomSheet } from "./components/bottom-sheet/bottom-sheet.js";
import type { GaBottomNav } from "./components/bottom-nav/bottom-nav.js";
import type { GaIcon } from "./components/icon/icon.js";
import type { GaSelect } from "./components/select/select.js";
import type { GaCalendar } from "./components/calendar/calendar.js";
import type { GaDateInput } from "./components/date-input/date-input.js";
import type { GaChartFrame } from "./components/chart-frame/chart-frame.js";
import type { GaChatMessage } from "./components/chat-message/chat-message.js";
import type { GaChat } from "./components/chat/chat.js";

declare global {
  interface HTMLElementTagNameMap {
    "ga-button": GaButton;
    "ga-radio-group": GaRadioGroup;
    "ga-code": GaCode;
    "ga-breadcrumbs": GaBreadcrumbs;
    "ga-table": GaTable;
    "ga-badge": GaBadge;
    "ga-card": GaCard;
    "ga-avatar": GaAvatar;
    "ga-input": GaInput;
    "ga-switch": GaSwitch;
    "ga-spinner": GaSpinner;
    "ga-alert": GaAlert;
    "ga-kbd": GaKbd;
    "ga-tabs": GaTabs;
    "ga-note": GaNote;
    "ga-file-drop": GaFileDrop;
    "ga-fab": GaFab;
    "ga-panel": GaPanel;
    "ga-slider": GaSlider;
    "ga-header": GaHeader;
    "ga-bottom-sheet": GaBottomSheet;
    "ga-bottom-nav": GaBottomNav;
    "ga-icon": GaIcon;
    "ga-select": GaSelect;
    "ga-calendar": GaCalendar;
    "ga-date-input": GaDateInput;
    "ga-chart-frame": GaChartFrame;
    "ga-chat-message": GaChatMessage;
    "ga-chat": GaChat;
  }
}

export {};
