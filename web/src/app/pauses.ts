import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { pausedText } from "@/lib/state";
import { renderButton } from "@/ui/button";
import { chevronDownIcon, chevronRightIcon } from "@/ui/icons";
import { renderErrorBadge } from "@/ui/page";
import { Message } from "./message";
import type { Model, Pause } from "./model";

export type PauseUser = Pause["users"][number];

const billingUrl = "https://claude.ai/settings/billing";

const isGlobal = (user: PauseUser) =>
  user.kind === "role" || (user.kind === "session" && user.workstream === 0);

const inWorkstream = (user: PauseUser, repository: string, number: number) =>
  user.workstreamRepository === repository && user.workstream === number;

const ownerOf = (repository: string) => repository.split("/")[0] ?? "";

const userOrganization = (user: PauseUser) =>
  ownerOf(user.workstream !== 0 ? user.workstreamRepository : user.repository);

export function usersOfOrganization(
  users: readonly PauseUser[],
  organization: string,
  workstream?: { repository: string; number: number },
): PauseUser[] {
  if (workstream) {
    const inChat = users.filter(
      (user) => isGlobal(user) || inWorkstream(user, workstream.repository, workstream.number),
    );
    if (inChat.length > 0) {
      return inChat;
    }
  }
  return users.filter((user) => isGlobal(user) || userOrganization(user) === organization);
}

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

function renderUser(h: HtmlBuilder<Message>, user: PauseUser): Html {
  const path = userPath(user);
  return h.li(
    [h.Class("wrap-anywhere")],
    [
      path === undefined
        ? userText(user)
        : h.a([h.Href(path), h.Class("underline")], [userText(user)]),
    ],
  );
}

// A pause with a global user shows on each page. Each other pause shows on each page of the organization of its users.
export function renderPauses(h: HtmlBuilder<Message>, model: Model): Html[] {
  const workstream =
    model.route._tag === "WorkstreamChat"
      ? {
          repository: `${model.route.owner}/${model.route.name}`,
          number: model.route.number,
        }
      : undefined;

  return model.pauses.flatMap((pause) => {
    const isShown = pause.users.some(
      (user) =>
        isGlobal(user) ||
        (model.organization !== "" && userOrganization(user) === model.organization),
    );
    if (!isShown) {
      return [];
    }
    const users = usersOfOrganization(pause.users, model.organization, workstream);
    const isBusy = Option.isSome(model.pauseBusy) && model.pauseBusy.value === pause.harness;
    const error = Option.getOrElse(model.pauseError, () => "");
    const isExpanded = model.pausesExpanded.includes(pause.harness);

    return [
      h.keyed("div")(
        `${pause.harness} ${pause.pausedUntil}`,
        [h.Class("grid min-w-0 gap-2"), h.DataAttribute("pause", pause.harness)],
        [
          h.div(
            [
              h.Class(
                "grid min-w-0 gap-1 rounded-lg border border-amber-500/40 bg-amber-100 px-3 py-2 text-xs text-amber-900 md:text-sm dark:bg-amber-950 dark:text-amber-200",
              ),
            ],
            [
              h.div(
                [h.Class("flex min-w-0 items-center justify-between gap-2")],
                [
                  h.button(
                    [
                      h.Type("button"),
                      h.AriaExpanded(isExpanded),
                      h.Class("min-w-0 flex-1 truncate text-left"),
                      h.OnClick(Message.ToggledPauseUsers({ harness: pause.harness })),
                    ],
                    [
                      (isExpanded ? chevronDownIcon : chevronRightIcon)(
                        h,
                        "mr-1 inline size-3.5 align-[-0.125em]",
                      ),
                      `${pause.harness} ${pausedText(pause.pausedUntil)}`,
                    ],
                  ),
                  renderButton(h, {
                    label: "Resume now",
                    size: "xs",
                    disabled: isBusy,
                    pending: isBusy,
                    onClick: Message.ResumePauseRequested({ harness: pause.harness }),
                  }),
                ],
              ),
              pause.reason === "subscription"
                ? h.div(
                    [h.Class("wrap-anywhere pl-4.5")],
                    [
                      "Claude Code cannot use the Claude subscription. Pay or renew at ",
                      h.a(
                        [
                          h.Href(billingUrl),
                          h.Attribute("target", "_blank"),
                          h.Attribute("rel", "noopener noreferrer"),
                          h.Class("underline"),
                        ],
                        [billingUrl],
                      ),
                      ".",
                    ],
                  )
                : h.empty,
              isExpanded
                ? h.div(
                    [h.Class("flex min-w-0 flex-wrap items-baseline gap-x-2 pl-4.5")],
                    [
                      h.span([], ["Used by:"]),
                      h.ul(
                        [h.Class("flex min-w-0 flex-wrap gap-x-3")],
                        users.map((user) => renderUser(h, user)),
                      ),
                    ],
                  )
                : h.empty,
            ],
          ),
          error ? renderErrorBadge(h, error) : h.empty,
        ],
      ),
    ];
  });
}
