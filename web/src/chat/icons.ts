import type { Html, HtmlBuilder } from "foldkit/html";
import type { Message } from "./message";

type H = HtmlBuilder<Message>;
type Shape = readonly [
  "path" | "circle" | "line" | "rect" | "polyline" | "polygon" | "ellipse",
  Readonly<Record<string, string>>,
];

// The data of each icon comes from the icon set of the package lucide-react.
function icon(
  h: H,
  shapes: readonly Shape[],
  className: string,
  extra: ReadonlyArray<ReturnType<H["Attribute"]>> = [],
): Html {
  return h.svg(
    [
      ...extra,
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    shapes.map(([tag, attributes]) =>
      h[tag](
        Object.entries(attributes).map(([name, value]) => h.Attribute(name, value)),
        [],
      ),
    ),
  );
}

const ban: readonly Shape[] = [
  ["circle", { cx: "12", cy: "12", r: "10" }],
  ["path", { d: "M4.929 4.929 19.07 19.071" }],
];

const circleX: readonly Shape[] = [
  ["circle", { cx: "12", cy: "12", r: "10" }],
  ["path", { d: "m15 9-6 6" }],
  ["path", { d: "m9 9 6 6" }],
];

const checkCheck: readonly Shape[] = [
  ["path", { d: "M18 6 7 17l-5-5" }],
  ["path", { d: "m22 10-7.5 7.5L13 16" }],
];

const triangleAlert: readonly Shape[] = [
  ["path", { d: "m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" }],
  ["path", { d: "M12 9v4" }],
  ["path", { d: "M12 17h.01" }],
];

const check: readonly Shape[] = [["path", { d: "M20 6 9 17l-5-5" }]];

const circleAlert: readonly Shape[] = [
  ["circle", { cx: "12", cy: "12", r: "10" }],
  ["line", { x1: "12", x2: "12", y1: "8", y2: "12" }],
  ["line", { x1: "12", x2: "12.01", y1: "16", y2: "16" }],
];

const clock: readonly Shape[] = [
  ["circle", { cx: "12", cy: "12", r: "10" }],
  ["path", { d: "M12 6v6l4 2" }],
];

const chevronRight: readonly Shape[] = [["path", { d: "m9 18 6-6-6-6" }]];

const trash2: readonly Shape[] = [
  ["path", { d: "M10 11v6" }],
  ["path", { d: "M14 11v6" }],
  ["path", { d: "M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" }],
  ["path", { d: "M3 6h18" }],
  ["path", { d: "M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" }],
];

const x: readonly Shape[] = [
  ["path", { d: "M18 6 6 18" }],
  ["path", { d: "m6 6 12 12" }],
];

const arrowUp: readonly Shape[] = [
  ["path", { d: "m5 12 7-7 7 7" }],
  ["path", { d: "M12 19V5" }],
];

const square: readonly Shape[] = [["rect", { width: "18", height: "18", x: "3", y: "3", rx: "2" }]];

const mic: readonly Shape[] = [
  ["path", { d: "M12 19v3" }],
  ["path", { d: "M19 10v2a7 7 0 0 1-14 0v-2" }],
  ["rect", { x: "9", y: "2", width: "6", height: "13", rx: "3" }],
];

const paperclip: readonly Shape[] = [
  [
    "path",
    {
      d: "m16 6-8.414 8.586a2 2 0 0 0 2.829 2.829l8.414-8.586a4 4 0 1 0-5.657-5.657l-8.379 8.551a6 6 0 1 0 8.485 8.485l8.379-8.551",
    },
  ],
];

const spinner: readonly Shape[] = [["path", { d: "M21 12a9 9 0 1 1-6.219-8.56" }]];

export function banIcon(h: H, className = "size-3.5"): Html {
  return icon(h, ban, className);
}

export function circleXIcon(h: H, className = "size-3.5"): Html {
  return icon(h, circleX, className);
}

export function checkCheckIcon(h: H, className = "size-3.5"): Html {
  return icon(h, checkCheck, className);
}

export function triangleAlertIcon(h: H, className = "size-3.5"): Html {
  return icon(h, triangleAlert, className);
}

export function checkIcon(h: H, className = "size-3.5"): Html {
  return icon(h, check, className);
}

export function circleAlertIcon(h: H, className = "size-3.5"): Html {
  return icon(h, circleAlert, className);
}

export function clockIcon(h: H, className = "size-3.5"): Html {
  return icon(h, clock, className);
}

export function chevronRightIcon(h: H, className = "size-4"): Html {
  return icon(h, chevronRight, className);
}

export function trash2Icon(h: H, className = "size-4"): Html {
  return icon(h, trash2, className);
}

export function xIcon(h: H, className = "size-4"): Html {
  return icon(h, x, className);
}

export function arrowUpIcon(h: H, className = "size-4"): Html {
  return icon(h, arrowUp, className);
}

export function squareIcon(h: H, className = "size-4"): Html {
  return icon(h, square, className);
}

export function micIcon(h: H, className = "size-4"): Html {
  return icon(h, mic, className);
}

export function paperclipIcon(h: H, className = "size-4"): Html {
  return icon(h, paperclip, className);
}

export function spinnerIcon(h: H, className = "size-4 animate-spin"): Html {
  return icon(h, spinner, className, [
    h.Attribute("data-slot", "spinner"),
    h.Attribute("aria-hidden", "true"),
  ]);
}
