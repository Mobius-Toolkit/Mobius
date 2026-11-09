import type { Html, HtmlBuilder } from "foldkit/html";
import type { WorkstreamAutopilot } from "@/api/api.gen";

type Shape = readonly [
  "path" | "circle" | "line" | "rect" | "polyline" | "polygon" | "ellipse",
  Readonly<Record<string, string>>,
];

// The data of each icon comes from the icon set of the package lucide-react.
function icon<M>(
  h: HtmlBuilder<M>,
  shapes: readonly Shape[],
  className: string,
  extra: ReadonlyArray<ReturnType<HtmlBuilder<M>["Attribute"]>> = [],
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

const messageSquare: readonly Shape[] = [
  [
    "path",
    {
      d: "M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z",
    },
  ],
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

const arrowLeft: readonly Shape[] = [
  ["path", { d: "m12 19-7-7 7-7" }],
  ["path", { d: "M19 12H5" }],
];

const arrowDown: readonly Shape[] = [
  ["path", { d: "M12 5v14" }],
  ["path", { d: "m19 12-7 7-7-7" }],
];

const chevronDown: readonly Shape[] = [["path", { d: "m6 9 6 6 6-6" }]];

const play: readonly Shape[] = [
  [
    "path",
    {
      d: "M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z",
    },
  ],
];

const settings: readonly Shape[] = [
  [
    "path",
    {
      d: "M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915",
    },
  ],
  ["circle", { cx: "12", cy: "12", r: "3" }],
];

export function banIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, ban, className);
}

export function circleXIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, circleX, className);
}

export function checkCheckIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, checkCheck, className);
}

export function triangleAlertIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, triangleAlert, className);
}

export function checkIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, check, className);
}

export function circleAlertIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, circleAlert, className);
}

export function clockIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, clock, className);
}

export function chevronRightIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, chevronRight, className);
}

export function trash2Icon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, trash2, className);
}

export function xIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, x, className);
}

export function arrowUpIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, arrowUp, className);
}

export function squareIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, square, className);
}

export function arrowLeftIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, arrowLeft, className);
}

export function arrowDownIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, arrowDown, className);
}

export function chevronDownIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, chevronDown, className);
}

export function playIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, play, className);
}

export function messageSquareIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, messageSquare, className);
}

export function settingsIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, settings, className);
}

export function micIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, mic, className);
}

export function paperclipIcon<M>(h: HtmlBuilder<M>, className = "size-4"): Html {
  return icon(h, paperclip, className);
}

export function spinnerIcon<M>(h: HtmlBuilder<M>, className = "size-4 animate-spin"): Html {
  return icon(h, spinner, className, [
    h.Attribute("data-slot", "spinner"),
    h.Attribute("aria-hidden", "true"),
  ]);
}

const powerOff: readonly Shape[] = [
  ["path", { d: "M18.36 6.64A9 9 0 0 1 20.77 15" }],
  ["path", { d: "M6.16 6.16a9 9 0 1 0 12.68 12.68" }],
  ["path", { d: "M12 2v4" }],
  ["path", { d: "m2 2 20 20" }],
];

const planeTakeoff: readonly Shape[] = [
  ["path", { d: "M2 22h20" }],
  [
    "path",
    {
      d: "M6.36 17.4 4 17l-2-4 1.1-.55a2 2 0 0 1 1.8 0l.17.1a2 2 0 0 0 1.8 0L8 12 5 6l.9-.45a2 2 0 0 1 2.09.2l4.02 3a2 2 0 0 0 2.1.2l4.19-2.06a2.41 2.41 0 0 1 1.73-.17L21 7a1.4 1.4 0 0 1 .87 1.99l-.38.76c-.23.46-.6.84-1.07 1.08L7.58 17.2a2 2 0 0 1-1.22.18Z",
    },
  ],
];

const planeLanding: readonly Shape[] = [
  ["path", { d: "M2 22h20" }],
  [
    "path",
    {
      d: "M3.77 10.77 2 9l2-4.5 1.1.55c.55.28.9.84.9 1.45s.35 1.17.9 1.45L8 8.5l3-6 1.05.53a2 2 0 0 1 1.09 1.52l.72 5.4a2 2 0 0 0 1.09 1.52l4.4 2.2c.42.22.78.55 1.01.96l.6 1.03c.49.88-.06 1.98-1.06 2.1l-1.18.15c-.47.06-.95-.02-1.37-.24L4.29 11.15a2 2 0 0 1-.52-.38Z",
    },
  ],
];

const plane: readonly Shape[] = [
  [
    "path",
    {
      d: "M17.8 19.2 16 11l3.5-3.5C21 6 21.5 4 21 3c-1-.5-3 0-4.5 1.5L13 8 4.8 6.2c-.5-.1-.9.1-1.1.5l-.3.5c-.2.5-.1 1 .3 1.3L9 12l-2 3H4l-1 1 3 2 2 3 1-1v-3l3-2 3.5 5.3c.3.4.8.5 1.3.3l.5-.2c.4-.3.6-.7.5-1.2z",
    },
  ],
];

const gitPullRequestArrow: readonly Shape[] = [
  ["circle", { cx: "5", cy: "6", r: "3" }],
  ["path", { d: "M5 9v12" }],
  ["circle", { cx: "19", cy: "18", r: "3" }],
  ["path", { d: "m15 9-3-3 3-3" }],
  ["path", { d: "M12 6h5a2 2 0 0 1 2 2v7" }],
];

const autopilotShapes: Record<WorkstreamAutopilot, readonly Shape[]> = {
  off: powerOff,
  start: planeTakeoff,
  merge: planeLanding,
  start_and_merge: plane,
};

export function autopilotIcon<M>(h: HtmlBuilder<M>, level: WorkstreamAutopilot): Html {
  return icon(h, autopilotShapes[level], "size-3.5 text-muted-foreground");
}

export function gitPullRequestArrowIcon<M>(h: HtmlBuilder<M>, className = "size-3.5"): Html {
  return icon(h, gitPullRequestArrow, className);
}
