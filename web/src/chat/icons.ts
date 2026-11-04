import type { Html, HtmlBuilder } from "foldkit/html";
import type { Message } from "./message";

type H = HtmlBuilder<Message>;

export function banIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.circle([h.Attribute("cx", "12"), h.Attribute("cy", "12"), h.Attribute("r", "10")]),
      h.path([h.Attribute("d", "m4.9 4.9 14.2 14.2")]),
    ],
  );
}

export function circleXIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.circle([h.Attribute("cx", "12"), h.Attribute("cy", "12"), h.Attribute("r", "10")]),
      h.path([h.Attribute("d", "m15 9-6 6")]),
      h.path([h.Attribute("d", "m9 9 6 6")]),
    ],
  );
}

export function checkCheckIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.path([h.Attribute("d", "M18 6 7 17l-5-5")]),
      h.path([h.Attribute("d", "m22 10-7.5 7.5L13 16")]),
    ],
  );
}

export function triangleAlertIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.path([
        h.Attribute(
          "d",
          "m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3",
        ),
      ]),
      h.line([
        h.Attribute("x1", "12"),
        h.Attribute("x2", "12"),
        h.Attribute("y1", "9"),
        h.Attribute("y2", "13"),
      ]),
      h.line([
        h.Attribute("x1", "12"),
        h.Attribute("x2", "12.01"),
        h.Attribute("y1", "17"),
        h.Attribute("y2", "17"),
      ]),
    ],
  );
}

export function checkIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [h.path([h.Attribute("d", "M20 6 9 17l-5-5")])],
  );
}

export function circleAlertIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.circle([h.Attribute("cx", "12"), h.Attribute("cy", "12"), h.Attribute("r", "10")]),
      h.line([
        h.Attribute("x1", "12"),
        h.Attribute("x2", "12"),
        h.Attribute("y1", "8"),
        h.Attribute("y2", "12"),
      ]),
      h.line([
        h.Attribute("x1", "12"),
        h.Attribute("x2", "12.01"),
        h.Attribute("y1", "16"),
        h.Attribute("y2", "16"),
      ]),
    ],
  );
}

export function clockIcon(h: H, className = "size-3.5"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.circle([h.Attribute("cx", "12"), h.Attribute("cy", "12"), h.Attribute("r", "10")]),
      h.polyline([h.Attribute("points", "12 6 12 12 16 14")]),
    ],
  );
}

export function chevronRightIcon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [h.path([h.Attribute("d", "m9 18 6-6-6-6")])],
  );
}

export function trash2Icon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.path([h.Attribute("d", "M3 6h18")]),
      h.path([h.Attribute("d", "M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6")]),
      h.path([h.Attribute("d", "M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2")]),
      h.line([
        h.Attribute("x1", "10"),
        h.Attribute("x2", "10"),
        h.Attribute("y1", "11"),
        h.Attribute("y2", "17"),
      ]),
      h.line([
        h.Attribute("x1", "14"),
        h.Attribute("x2", "14"),
        h.Attribute("y1", "11"),
        h.Attribute("y2", "17"),
      ]),
    ],
  );
}

export function xIcon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [h.path([h.Attribute("d", "M18 6 6 18")]), h.path([h.Attribute("d", "m6 6 12 12")])],
  );
}

export function arrowUpIcon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [h.path([h.Attribute("d", "m5 12 7-7 7 7")]), h.path([h.Attribute("d", "M12 19V5")])],
  );
}

export function squareIcon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.rect([
        h.Attribute("width", "18"),
        h.Attribute("height", "18"),
        h.Attribute("x", "3"),
        h.Attribute("y", "3"),
        h.Attribute("rx", "2"),
      ]),
    ],
  );
}

export function micIcon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.path([h.Attribute("d", "M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z")]),
      h.path([h.Attribute("d", "M19 10v2a7 7 0 0 1-14 0v-2")]),
      h.line([
        h.Attribute("x1", "12"),
        h.Attribute("x2", "12"),
        h.Attribute("y1", "19"),
        h.Attribute("y2", "22"),
      ]),
    ],
  );
}

export function spinnerIcon(h: H, className = "size-4 animate-spin"): Html {
  return h.svg(
    [
      h.Attribute("data-slot", "spinner"),
      h.AriaHidden(true),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
      h.Class(className),
    ],
    [h.path([h.Attribute("d", "M21 12a9 9 0 1 1-6.219-8.56")])],
  );
}

export function paperclipIcon(h: H, className = "size-4"): Html {
  return h.svg(
    [
      h.Class(className),
      h.Attribute("viewBox", "0 0 24 24"),
      h.Attribute("fill", "none"),
      h.Attribute("stroke", "currentColor"),
      h.Attribute("stroke-width", "2"),
      h.Attribute("stroke-linecap", "round"),
      h.Attribute("stroke-linejoin", "round"),
    ],
    [
      h.path([
        h.Attribute(
          "d",
          "m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48",
        ),
      ]),
    ],
  );
}
