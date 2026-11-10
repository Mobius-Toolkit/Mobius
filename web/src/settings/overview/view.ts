import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { uiRelease } from "@/lib/build";
import { settingsPages } from "@/lib/settings";
import { renderBadge } from "@/ui/badge";
import { defineRouterLink } from "@/ui/link";
import { inset, renderLinkRow, renderList, renderPageHeader } from "@/ui/page";
import { Message } from "./message";
import { languageName, switchesFailed, switchesText, type Model } from "./model";

type H = HtmlBuilder<Message>;

const RouterLink = defineRouterLink(Message.NavigateTo);

const muted = "text-sm text-muted-foreground";

function renderPageRow(h: H, model: Model, page: (typeof settingsPages)[number]): Html {
  const state = switchesText(model);
  const children: Html[] = [];
  if (page.path === "/settings/switches") {
    children.push(
      state ? h.span([h.Class(muted)], [state]) : h.empty,
      switchesFailed(model) ? renderBadge(h, "destructive", "Error") : h.empty,
    );
  }
  if (page.path === "/settings/voice") {
    children.push(h.span([h.Class(muted)], [languageName(model)]));
  }
  return renderLinkRow(h, {
    key: page.path,
    path: page.path,
    mount: h.OnMount(RouterLink({ path: page.path })),
    title: h.span([], [page.title]),
    children,
  });
}

function renderVersion(h: H, title: string, version: string): Html {
  return version
    ? h.div([h.Class("flex gap-4")], [h.dt([h.Class("w-16")], [title]), h.dd([], [version])])
    : h.empty;
}

export function view(model: Model, h: H): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Settings"),
      renderList(
        h,
        settingsPages.map((page) => renderPageRow(h, model, page)),
      ),
      h.dl(
        [h.Class(cn("mt-4 grid gap-1 text-xs text-muted-foreground", inset))],
        [renderVersion(h, "Server", model.server), renderVersion(h, "Browser", uiRelease)],
      ),
    ],
  );
}
