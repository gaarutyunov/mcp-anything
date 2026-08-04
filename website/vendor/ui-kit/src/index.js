/* =========================================================================
   GA UI Kit — universal Web Components.

   Importing this module registers every custom element (`ga-*`) as a side
   effect. Works in any framework or none:

     import "@gaarutyunov/ui-kit";              // register all components
     import "@gaarutyunov/ui-kit/tokens.css";   // optional global theme

   Or cherry-pick a single component:

     import "@gaarutyunov/ui-kit/components/button/button.js";
   ========================================================================= */

export { GaElement, define, esc } from "./core/base-element.js";

import "./components/button/button.js";
import "./components/radio-group/radio-group.js";
import "./components/code/code.js";
import "./components/breadcrumbs/breadcrumbs.js";
import "./components/table/table.js";
import "./components/badge/badge.js";
import "./components/card/card.js";
import "./components/avatar/avatar.js";
import "./components/input/input.js";
import "./components/switch/switch.js";
import "./components/spinner/spinner.js";
import "./components/alert/alert.js";
import "./components/kbd/kbd.js";
import "./components/tabs/tabs.js";
import "./components/note/note.js";
import "./components/file-drop/file-drop.js";
import "./components/fab/fab.js";
import "./components/panel/panel.js";
import "./components/slider/slider.js";
import "./components/header/header.js";
import "./components/bottom-sheet/bottom-sheet.js";
import "./components/bottom-nav/bottom-nav.js";
import "./components/icon/icon.js";
import "./components/select/select.js";
import "./components/calendar/calendar.js";
import "./components/date-input/date-input.js";
import "./components/chart-frame/chart-frame.js";
import "./components/chat-message/chat-message.js";
import "./components/chat/chat.js";

/* index.d.ts declares a named export per component, so these have to exist at
   runtime too — otherwise `import { GaButton } from "@gaarutyunov/ui-kit"`
   type-checks and then resolves to undefined. The imports above still do the
   registering; these only re-export the classes. */
export { GaButton } from "./components/button/button.js";
export { GaRadioGroup } from "./components/radio-group/radio-group.js";
export { GaCode } from "./components/code/code.js";
export { GaBreadcrumbs } from "./components/breadcrumbs/breadcrumbs.js";
export { GaTable } from "./components/table/table.js";
export { GaBadge } from "./components/badge/badge.js";
export { GaCard } from "./components/card/card.js";
export { GaAvatar } from "./components/avatar/avatar.js";
export { GaInput } from "./components/input/input.js";
export { GaSwitch } from "./components/switch/switch.js";
export { GaSpinner } from "./components/spinner/spinner.js";
export { GaAlert } from "./components/alert/alert.js";
export { GaKbd } from "./components/kbd/kbd.js";
export { GaTabs } from "./components/tabs/tabs.js";
export { GaNote } from "./components/note/note.js";
export { GaFileDrop } from "./components/file-drop/file-drop.js";
export { GaFab } from "./components/fab/fab.js";
export { GaPanel } from "./components/panel/panel.js";
export { GaSlider } from "./components/slider/slider.js";
export { GaHeader } from "./components/header/header.js";
export { GaBottomSheet } from "./components/bottom-sheet/bottom-sheet.js";
export { GaBottomNav } from "./components/bottom-nav/bottom-nav.js";
export { GaIcon } from "./components/icon/icon.js";
export { GaSelect } from "./components/select/select.js";
export { GaCalendar } from "./components/calendar/calendar.js";
export { GaDateInput } from "./components/date-input/date-input.js";
export { GaChartFrame } from "./components/chart-frame/chart-frame.js";
export { GaChatMessage } from "./components/chat-message/chat-message.js";
export { GaChat } from "./components/chat/chat.js";
