import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "@/ui/badge";
import { renderButton } from "@/ui/button";
import { renderErrorBadge, renderList, renderPageHeader, renderRow } from "@/ui/page";
import type { Devices } from "../schemas";
import { Message } from "./message";
import type { Model } from "./model";

type H = HtmlBuilder<Message>;

function renderLogin(h: H, model: Model, devices: Devices, login: Devices["logins"][number]): Html {
  return renderRow(h, String(login.id), "justify-between", [
    h.span(
      [h.Class("grid gap-1")],
      [
        h.span([], [login.userAgent]),
        h.span(
          [h.Class("text-muted-foreground")],
          [`Logged in ${new Date(login.createdAt).toLocaleString()}`],
        ),
      ],
    ),
    h.span(
      [h.Class("flex items-center gap-2")],
      [
        login.id === devices.thisDevice ? renderBadge(h, "secondary", "This device") : h.empty,
        renderButton(h, {
          label: "Log out",
          variant: "outline",
          pending: model.loggingOut.includes(login.id),
          onClick: Message.ClickedLogOut({ id: login.id }),
        }),
      ],
    ),
  ]);
}

export function view(model: Model, h: H): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Devices"),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      Option.match(model.devices, {
        onNone: () => renderList(h, []),
        onSome: (devices) =>
          renderList(
            h,
            devices.logins.map((login) => renderLogin(h, model, devices, login)),
          ),
      }),
    ],
  );
}
