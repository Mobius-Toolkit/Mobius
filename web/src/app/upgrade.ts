import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "@/ui/badge";
import { renderButton } from "@/ui/button";
import { defineDialogMount, renderDialog } from "@/ui/dialog";
import { Message } from "./message";
import type { Model, UpgradeState } from "./model";

const dialogMount = defineDialogMount(Message.CompletedFocusSheet);

function drainText(waiting: number): string {
  if (waiting === 0) {
    return "Upgrade is ready to restart";
  }
  return `Upgrade waits for ${waiting} ${waiting === 1 ? "agent or upgrade" : "agents or upgrades"}`;
}

function countdownText(milliseconds: number): string {
  const total = Math.max(0, Math.ceil(milliseconds / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  if (minutes === 0) {
    return `${seconds} s`;
  }
  return seconds === 0 ? `${minutes} min` : `${minutes} min ${seconds} s`;
}

function stepText(upgrade: UpgradeState): string {
  const waiting = Option.match(upgrade.drain, {
    onNone: () => 0,
    onSome: (drain) => drain.waiting,
  });
  switch (upgrade.step) {
    case "release":
      return "Upgrade reads the newest release";
    case "download":
      return "Upgrade downloads the new release";
    case "retry":
      return `Try ${upgrade.tries} failed. Upgrade tries again in ${countdownText(Option.getOrElse(upgrade.nextTry, () => upgrade.now) - upgrade.now)}`;
    case "drain":
      return drainText(waiting);
    case "restart":
      return "Upgrade restarts Mobius";
    case "idle":
      return Option.exists(upgrade.drain, (drain) => drain.on) ? drainText(waiting) : "";
  }
}

const cancellable = new Set<UpgradeState["step"]>(["release", "download", "retry", "drain"]);

function drainHeld(upgrade: UpgradeState): boolean {
  return upgrade.step === "idle" && Option.exists(upgrade.drain, (drain) => drain.on);
}

export function renderUpgradeControls(h: HtmlBuilder<Message>, model: Model): Html {
  const items: Html[] = [];
  const upgrade = model.upgrade;

  if (upgrade.step !== "idle" || drainHeld(upgrade)) {
    items.push(
      h.p(
        [h.Class("flex items-center gap-2 px-2 text-sm text-muted-foreground")],
        [h.span([h.Class("size-2 shrink-0 rounded-full bg-amber-500")], []), stepText(upgrade)],
      ),
    );
  }

  if (cancellable.has(upgrade.step) || drainHeld(upgrade)) {
    items.push(
      renderButton(h, {
        variant: "secondary",
        pending: upgrade.cancelling,
        label: "Cancel upgrade",
        onClick: Message.CancelUpgradeRequested(),
      }),
    );
  } else if (upgrade.step === "idle" && upgrade.version) {
    items.push(
      renderButton(h, {
        variant: "secondary",
        pending: upgrade.upgrading,
        label: ["Upgrade ", h.span([h.Class("text-muted-foreground")], [upgrade.version])],
        onClick: Message.ShowUpgradeChanges(),
      }),
    );
  }

  if (upgrade.failure) {
    items.push(
      renderBadge(
        h,
        "destructive",
        upgrade.failure,
        "h-auto w-full whitespace-normal wrap-anywhere",
      ),
    );
  }

  if (model.newBuild) {
    items.push(
      renderButton(h, {
        variant: "secondary",
        label: "New version",
        onClick: Message.NavigateRequested({ path: window.location.pathname }),
      }),
    );
  }

  return h.div([h.Class("contents")], items);
}

function renderReleaseChanges(h: HtmlBuilder<Message>, model: Model): Html {
  const upgrade = model.upgrade;
  if (upgrade.changesError) {
    return renderBadge(h, "destructive", upgrade.changesError);
  }
  if (Option.isNone(upgrade.changes)) {
    return h.p([h.Class("text-muted-foreground")], ["Mobius reads the changes."]);
  }
  return h.ul(
    [h.Class("grid list-disc gap-1 pl-5 wrap-anywhere")],
    upgrade.changes.value.map((title, index) => h.li([h.Key(String(index))], [title])),
  );
}

export function renderUpgradeDialog(h: HtmlBuilder<Message>, model: Model): Html {
  const upgrade = model.upgrade;
  if (!upgrade.changesShown || !upgrade.version) {
    return h.empty;
  }

  return renderDialog(h, {
    mount: h.OnMount(dialogMount()),
    titleId: "upgrade-dialog-title",
    title: `Upgrade to ${upgrade.version}`,
    description: "Mobius waits until no agent runs, and then starts the new release.",
    close: Message.HideUpgradeChanges(),
    layout: "center",
    body: h.div([h.Class("min-h-0 overflow-y-auto")], [renderReleaseChanges(h, model)]),
    footer: h.div(
      [
        h.DataAttribute("slot", "dialog-footer"),
        h.Class(
          "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        ),
      ],
      [
        renderButton(h, {
          pending: upgrade.upgrading,
          label: "Upgrade",
          onClick: Message.StartUpgradeRequested(),
        }),
      ],
    ),
    className: "flex max-h-[calc(100svh-2rem)] flex-col sm:max-w-lg",
  });
}
