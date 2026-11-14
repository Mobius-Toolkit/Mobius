import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { dayClock } from "@/lib/time";
import { renderButton } from "@/ui/button";
import { renderErrorBadge } from "@/ui/page";
import { Message } from "./message";
import type { Model, Pause } from "./model";

type PauseUser = Pause["users"][number];

const phoneUserCount = 2;

const isGlobal = (user: PauseUser) =>
  user.kind === "role" || (user.kind === "session" && user.workstream === 0);

const inWorkstream = (user: PauseUser, repository: string, number: number) =>
  user.workstreamRepository === repository && user.workstream === number;

const roleTitle = (role: string) => {
  const words = role.replaceAll("_", " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
};

const issue = (user: PauseUser) => `${user.repository}#${user.number} ${user.title}`;

function userText(user: PauseUser): string {
  const role = roleTitle(user.role);
  switch (user.kind) {
    case "role":
      return user.binding ? `${role} binding ${user.binding}` : `${role} role`;
    case "workstream":
    case "task":
      return `${role} setting of ${issue(user)}`;
    case "session":
      return user.repository ? `${role} session of ${issue(user)}` : `${role} session`;
  }
}

function userPath(user: PauseUser): string | undefined {
  switch (user.kind) {
    case "workstream":
      return `/workstreams/${user.repository}/${user.number}?tab=details&setting=${user.role}`;
    case "task":
      return `/workstreams/${user.workstreamRepository}/${user.workstream}?tab=tasks&setting=${user.role}&task=${user.repository}/${user.number}`;
    case "role":
    case "session":
      return undefined;
  }
}

function renderUser(h: HtmlBuilder<Message>, user: PauseUser, hiddenOnPhone: boolean): Html {
  const path = userPath(user);
  return h.li(
    [h.Class(cn("wrap-anywhere", hiddenOnPhone && "hidden md:block"))],
    [
      path === undefined
        ? userText(user)
        : h.a([h.Href(path), h.Class("underline")], [userText(user)]),
    ],
  );
}

// A pause that no role binding and no session without a Workstream uses shows only in the Workstreams of its users.
export function renderPauses(h: HtmlBuilder<Message>, model: Model): Html[] {
  const workstream =
    model.route._tag === "WorkstreamChat"
      ? {
          repository: `${model.route.owner}/${model.route.name}`,
          number: model.route.number,
        }
      : undefined;

  return model.pauses.flatMap((pause) => {
    const users = workstream
      ? pause.users.filter(
          (user) => isGlobal(user) || inWorkstream(user, workstream.repository, workstream.number),
        )
      : pause.users;
    const isShown = pause.users.some(isGlobal) || (workstream !== undefined && users.length > 0);
    if (!isShown) {
      return [];
    }
    const isBusy = Option.isSome(model.pauseBusy) && model.pauseBusy.value === pause.harness;
    const error = Option.getOrElse(model.pauseError, () => "");
    const isExpanded = model.pausesExpanded.includes(pause.harness);
    const hiddenCount = isExpanded ? 0 : Math.max(users.length - phoneUserCount, 0);

    return [
      h.keyed("div")(
        `${pause.harness} ${pause.pausedUntil}`,
        [h.Class("grid gap-2"), h.DataAttribute("pause", pause.harness)],
        [
          h.div(
            [
              h.Class(
                "grid gap-1 rounded-lg border border-amber-500/40 bg-amber-100 px-3 py-2 text-xs text-amber-900 md:text-sm dark:bg-amber-950 dark:text-amber-200",
              ),
            ],
            [
              h.div(
                [h.Class("flex flex-wrap items-center justify-between gap-2")],
                [
                  h.span([], [pause.harness, " paused until ", dayClock(pause.pausedUntil)]),
                  renderButton(h, {
                    label: "Resume now",
                    size: "xs",
                    disabled: isBusy,
                    pending: isBusy,
                    onClick: Message.ResumePauseRequested({ harness: pause.harness }),
                  }),
                ],
              ),
              h.div(
                [h.Class("flex flex-wrap items-baseline gap-x-2")],
                [
                  h.span([], ["Used by:"]),
                  h.ul(
                    [h.Class("flex min-w-0 flex-wrap gap-x-3")],
                    users.map((user, index) =>
                      renderUser(h, user, !isExpanded && index >= phoneUserCount),
                    ),
                  ),
                  hiddenCount > 0
                    ? renderButton(h, {
                        label: `Show ${hiddenCount} more`,
                        variant: "link",
                        size: "xs",
                        className: "md:hidden",
                        onClick: Message.ExpandedPauseUsers({ harness: pause.harness }),
                      })
                    : h.empty,
                ],
              ),
            ],
          ),
          error ? renderErrorBadge(h, error) : h.empty,
        ],
      ),
    ];
  });
}
