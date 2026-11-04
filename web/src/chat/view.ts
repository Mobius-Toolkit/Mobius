import * as Markdown from "@foldkit/markdown";
import { Effect, Queue, Stream } from "effect";
import { Mount } from "foldkit";
import { parseMarkdown } from "@foldkit/markdown/vite";
import type { Html, HtmlBuilder } from "foldkit/html";
import {
  InboxItemKind,
  type ActionArgument,
  type ActionLabel,
  type ChatMessage,
  type InboxItem,
  type PendingAction,
  type Question as QuestionType,
  type Workstream,
} from "@/api/api.gen";
import { actionState, roleName } from "@/lib/actions";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffLines } from "@/lib/diff";
import type { DraftInboxItem } from "@/lib/draft";
import { getGetChatImageUrl } from "@/api/api.gen";
import { clock, clockSeconds, dayClock, dayLabel, localTimes } from "@/lib/time";
import {
  banIcon,
  checkCheckIcon,
  checkIcon,
  chevronRightIcon,
  circleAlertIcon,
  circleXIcon,
  clockIcon,
  spinnerIcon,
  trash2Icon,
  triangleAlertIcon,
  xIcon,
} from "./icons";
import { fileUrl, renderComposer, renderPanelComposer } from "./composer";
import { ChatList } from "./scroll";
import { badgeClass, buttonClass, cn } from "./ui";
import { Message } from "./message";
import { countsAsUnread, type Failure, type Model, type Waiting } from "./model";

type H = HtmlBuilder<Message>;

// remark-directive is always on in @foldkit/markdown, and it throws on text such as "at 09:30". The second try escapes
// each colon between two word characters.
function parseChatMarkdown(text: string) {
  try {
    return parseMarkdown(text);
  } catch {
    return parseMarkdown(text.replace(/(?<=\w):(?=\w)/g, "\\:"));
  }
}

function renderMarkdown(h: H, text: string): Html {
  if (!text) return h.empty;
  try {
    const doc = parseChatMarkdown(localTimes(text));
    return h.div(
      [
        h.Class(
          "grid grid-cols-[minmax(0,1fr)] gap-2 break-words [&>div]:grid [&>div]:grid-cols-[minmax(0,1fr)] [&>div]:gap-2 [&_blockquote]:border-l-2 [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-[0.9em] [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:text-base [&_h2]:font-semibold [&_h3]:font-semibold [&_hr]:border-border [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:whitespace-pre-line [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:px-2 [&_th]:py-1 [&_th]:font-semibold [&_ul]:list-disc [&_ul]:pl-5",
        ),
      ],
      [
        Markdown.view(doc, {
          views: {
            ...Markdown.defaultViews,
            Strikethrough: (_strikethrough, content) =>
              h.del([h.Class("bg-red-500/40 line-through")], content),
            Text: ({ value }) => {
              if (!value.includes("==")) {
                return value;
              }
              const parts = value.split("==");
              return h.span(
                [],
                parts.map((part, index) =>
                  index % 2 === 1 ? h.ins([h.Class("bg-green-500/40 no-underline")], [part]) : part,
                ),
              );
            },
            Link: (linkNode, content) =>
              h.a(
                [
                  h.Attribute("href", linkNode.url),
                  h.Attribute("target", "_blank"),
                  h.Attribute("rel", "noopener noreferrer"),
                  h.Class("break-words text-primary underline underline-offset-2"),
                ],
                content,
              ),
            Image: (imgNode) =>
              h.a(
                [
                  h.Attribute("href", imgNode.url),
                  h.Attribute("target", "_blank"),
                  h.Attribute("rel", "noopener noreferrer"),
                  h.Class("break-words text-primary underline underline-offset-2"),
                ],
                [imgNode.alt || imgNode.url],
              ),
          },
        }),
      ],
    );
  } catch {
    return h.div([h.Class("whitespace-pre-line break-words")], [localTimes(text)]);
  }
}

function renderOwnerStatus(h: H, message: ChatMessage): Html {
  if (message.stoppedAt) {
    const label = `Stopped, the agent did not get it, at ${clock(message.stoppedAt)}`;
    return h.span([h.Role("img"), h.AriaLabel(label), h.Attribute("title", label)], [banIcon(h)]);
  }
  if (message.cancelledAt) {
    const label = `Cancelled, sent again below, at ${clock(message.cancelledAt)}`;
    return h.span(
      [h.Role("img"), h.AriaLabel(label), h.Attribute("title", label)],
      [circleXIcon(h)],
    );
  }
  if (message.deliveredAt) {
    const label = `Delivered to the agent at ${clock(message.deliveredAt)}`;
    return h.span(
      [h.Role("img"), h.AriaLabel(label), h.Attribute("title", label)],
      [checkCheckIcon(h)],
    );
  }
  if (message.failedAt) {
    const label = `Not delivered, the agent failed, at ${clock(message.failedAt)}`;
    return h.span(
      [h.Role("img"), h.AriaLabel(label), h.Attribute("title", label), h.Class("text-destructive")],
      [triangleAlertIcon(h)],
    );
  }
  return h.span(
    [
      h.Role("img"),
      h.AriaLabel("Stored, waits for the agent"),
      h.Attribute("title", "Stored, waits for the agent"),
    ],
    [checkIcon(h)],
  );
}

function renderDaySeparator(h: H, label: string): Html {
  return h.div(
    [
      h.Role("separator"),
      h.AriaLabel(label),
      h.Class("flex items-center gap-3 text-xs text-muted-foreground"),
    ],
    [
      h.span([h.Class("h-px grow bg-border")]),
      h.span([], [label]),
      h.span([h.Class("h-px grow bg-border")]),
    ],
  );
}

function renderInboxItemCard(
  h: H,
  item: { reference?: string; title: string; reason?: string; link?: string },
): Html {
  const lines = [
    item.reference
      ? h.span([h.Class("truncate text-xs text-muted-foreground")], [item.reference])
      : h.empty,
    h.span([h.Class("truncate font-medium")], [item.title]),
    h.span([h.Class("truncate text-xs text-muted-foreground")], [item.reason ?? ""]),
  ];
  const cardClass = "grid min-w-0 gap-0.5 rounded-lg border bg-card px-2 py-1.5 text-sm";
  if (!item.link) {
    return h.div([h.Class(cardClass)], lines);
  }
  return h.a(
    [
      h.Attribute("href", item.link),
      h.Attribute("target", "_blank"),
      h.Attribute("rel", "noreferrer"),
      h.Class(`${cardClass} hover:bg-accent`),
    ],
    lines,
  );
}

function renderActionSender(h: H, action: PendingAction, onNavigate = false): Html {
  const [owner, name] = action.sender ? action.sender.repository.split("/") : ["", ""];
  const senderLink = action.sender ? `/workstreams/${owner}/${name}/${action.sender.number}` : "";
  const senderText = action.sender
    ? `#${action.sender.number}${action.sender.title ? ` ${action.sender.title}` : ""}`
    : "";

  return h.span(
    [h.Class("text-muted-foreground wrap-anywhere")],
    [
      roleName[action.role] || action.role,
      action.sender
        ? h.span(
            [],
            [
              " of ",
              h.a(
                [
                  h.Attribute("href", senderLink),
                  h.Class("text-primary underline-offset-4 hover:underline"),
                  ...(onNavigate ? [h.OnClick(Message.ClosePanel())] : []),
                ],
                [senderText],
              ),
            ],
          )
        : h.empty,
      " · ",
      clock(action.time),
    ],
  );
}

function renderActionCard(
  h: H,
  message: ChatMessage,
  action: PendingAction,
  pendingItem: InboxItem | undefined,
): Html {
  const pending = action.state === "pending";
  const failed = pending && action.error !== "";
  const outcome = failed ? action.error : action.state === "done" ? action.result : "";

  return h.div(
    [
      h.DataAttribute("message", String(message.id)),
      h.DataAttribute("action", String(action.id)),
      h.Class(
        "grid max-w-full grid-cols-[minmax(0,1fr)] gap-1 justify-self-stretch rounded-xl border bg-card px-3 py-2",
      ),
    ],
    [
      h.div([h.Class("text-xs")], [renderActionSender(h, action)]),
      h.p([h.Class("line-clamp-2 wrap-anywhere")], [message.text]),
      h.div(
        [h.Class("flex items-center gap-2")],
        [
          h.div(
            [h.Class("flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-1")],
            [
              h.span(
                [
                  h.DataAttribute("slot", "badge"),
                  h.Class(badgeClass(failed ? "destructive" : "secondary")),
                ],
                [actionState(action)],
              ),
              outcome
                ? h.span(
                    [h.Class("line-clamp-2 min-w-0 text-sm wrap-anywhere whitespace-pre-line")],
                    [outcome],
                  )
                : h.empty,
            ],
          ),
          pending
            ? pendingItem
              ? h.button(
                  [
                    h.Type("button"),
                    h.Class(buttonClass({ size: "sm" })),
                    h.OnClick(Message.OpenAnswerPanel({ item: pendingItem })),
                  ],
                  ["Answer"],
                )
              : h.empty
            : h.button(
                [
                  h.Type("button"),
                  h.Class(buttonClass({ variant: "ghost", size: "sm" })),
                  h.OnClick(
                    Message.OpenDetailsPanel({
                      item: {
                        id: 0,
                        action,
                        title: message.text,
                        questions: [],
                        time: action.time,
                        kind: InboxItemKind.approval,
                        answer: "",
                        dismissedAt: null,
                        issue: 0,
                        link: "",
                        mergeRefusal: "",
                        merging: false,
                        organization: "",
                        pullRequest: 0,
                        pullRequestUrl: "",
                        reason: "",
                        reportUrl: "",
                        repository: "",
                        text: "",
                        todo: null,
                        withdrawn: false,
                        workstream: 0,
                        workstreamRepository: "",
                      },
                    }),
                  ),
                ],
                ["Details"],
              ),
        ],
      ),
    ],
  );
}

function questionState(q: InboxItem) {
  if (!q.dismissedAt) return "Waits for your answer";
  if (q.answer) return "Answered";
  return q.withdrawn ? "Withdrawn" : "No answer";
}

function renderQuestionCard(
  h: H,
  message: ChatMessage,
  question: InboxItem,
  pendingItem: InboxItem | undefined,
): Html {
  const answer = question.dismissedAt ? question.answer : "";
  return h.div(
    [
      h.DataAttribute("message", String(message.id)),
      h.DataAttribute("question", String(question.id)),
      h.Class(
        "grid max-w-full grid-cols-[minmax(0,1fr)] gap-1 justify-self-stretch rounded-xl border bg-card px-3 py-2",
      ),
    ],
    [
      h.div(
        [h.Class("text-xs text-muted-foreground")],
        [`${question.repository === "" ? "Coordinator" : "Lead"} · ${clock(message.time)}`],
      ),
      h.p([h.Class("truncate")], [message.text]),
      h.div(
        [h.Class("flex items-center gap-2")],
        [
          h.div(
            [h.Class("flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-1")],
            [
              h.span(
                [h.DataAttribute("slot", "badge"), h.Class(badgeClass("secondary"))],
                [questionState(question)],
              ),
              answer
                ? h.span(
                    [h.Class("line-clamp-2 min-w-0 text-sm wrap-anywhere whitespace-pre-line")],
                    [answer],
                  )
                : h.empty,
            ],
          ),
          !question.dismissedAt && pendingItem
            ? h.button(
                [
                  h.Type("button"),
                  h.Class(buttonClass({ size: "sm" })),
                  h.OnClick(Message.OpenAnswerPanel({ item: pendingItem })),
                ],
                ["Answer"],
              )
            : h.empty,
          answer
            ? h.button(
                [
                  h.Type("button"),
                  h.Class(buttonClass({ variant: "ghost", size: "sm" })),
                  h.OnClick(Message.OpenDetailsPanel({ item: question })),
                ],
                ["Details"],
              )
            : h.empty,
        ],
      ),
    ],
  );
}

function renderEventBody(h: H, id: number, summary: string, body: string, open: boolean): Html {
  const state = open ? "open" : "closed";
  return h.div(
    [h.DataAttribute("slot", "collapsible"), h.DataAttribute("state", state)],
    [
      h.button(
        [
          h.Type("button"),
          h.AriaExpanded(open),
          h.DataAttribute("slot", "collapsible-trigger"),
          h.DataAttribute("state", state),
          h.Class("group flex w-full min-w-0 items-start gap-1 text-left break-words"),
          h.OnClick(Message.EventToggled({ id })),
        ],
        [
          chevronRightIcon(
            h,
            cn("mt-0.5 size-4 shrink-0 transition-transform", open && "rotate-90"),
          ),
          h.span([h.Class("min-w-0")], [localTimes(summary)]),
        ],
      ),
      open
        ? h.div(
            [
              h.DataAttribute("slot", "collapsible-content"),
              h.DataAttribute("state", state),
              h.Class("pt-2"),
            ],
            [renderMarkdown(h, body)],
          )
        : h.empty,
    ],
  );
}

function renderMessage(h: H, message: ChatMessage, open: boolean, rows: InboxItem[]): Html {
  if (message.question) {
    return renderQuestionCard(
      h,
      message,
      message.question as InboxItem,
      rows.find((row) => row.id === (message.question as InboxItem).id),
    );
  }
  if (message.action) {
    return renderActionCard(
      h,
      message,
      message.action as PendingAction,
      rows.find((row) => row.action?.id === (message.action as PendingAction).id),
    );
  }
  if (message.author === "Record") {
    return h.div(
      [
        h.DataAttribute("message", String(message.id)),
        h.Class(
          "max-w-[85%] justify-self-end rounded-xl border border-transparent bg-secondary px-2 py-1 text-xs text-muted-foreground wrap-anywhere",
        ),
      ],
      [`${message.text} · ${clock(message.time)}`],
    );
  }

  const isOwner = message.author === "Owner";
  const isEvent = message.author === "Event";
  const [summary, ...rest] = message.text.split("\n");
  const body = rest.join("\n").trim();
  const cardClass = cn(
    "grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 rounded-xl border px-3 py-2",
    isOwner ? "justify-self-end border-transparent bg-secondary" : "justify-self-start bg-card",
    isEvent && "border-dashed bg-transparent text-muted-foreground",
  );

  return h.div(
    [h.DataAttribute("message", String(message.id)), h.Class(cardClass)],
    [
      h.div(
        [h.Class("flex items-center gap-2 text-xs text-muted-foreground")],
        [
          h.span([], [message.author === "tell_owner" ? "Lead" : message.author]),
          h.span([], [clock(message.time)]),
          isOwner ? renderOwnerStatus(h, message) : h.empty,
        ],
      ),
      ...(message.inboxItems || []).map((item) => renderInboxItemCard(h, item)),
      isEvent && body
        ? renderEventBody(h, message.id, summary, body, open)
        : message.text
          ? renderMarkdown(h, message.text)
          : h.empty,
      message.images > 0
        ? h.div(
            [h.Class("flex flex-wrap gap-2")],
            Array.from({ length: message.images }, (_, position) =>
              h.img([
                h.Attribute("src", getGetChatImageUrl(message.id, position)),
                h.Attribute("alt", `Picture ${position + 1} of message ${message.id}`),
                h.Class("max-h-48 max-w-full rounded-lg border object-contain"),
              ]),
            ),
          )
        : h.empty,
    ],
  );
}

function renderQueuedMessage(
  h: H,
  item: Waiting,
  failure: Failure | null,
  deletable: boolean,
): Html {
  return h.div(
    [
      h.DataAttribute("queued", item.id),
      h.Class(
        "grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 justify-self-end rounded-xl border border-transparent bg-secondary px-3 py-2",
      ),
    ],
    [
      h.div(
        [h.Class("flex items-center gap-2 text-xs text-muted-foreground")],
        [
          h.span([], ["Owner"]),
          h.span([], [clock(item.time)]),
          failure
            ? h.span(
                [
                  h.Role("img"),
                  h.AriaLabel("Send failed"),
                  h.Attribute("title", "Send failed"),
                  h.Class("text-destructive"),
                ],
                [circleAlertIcon(h)],
              )
            : h.span(
                [h.Role("img"), h.AriaLabel("Not sent yet"), h.Attribute("title", "Not sent yet")],
                [clockIcon(h)],
              ),
        ],
      ),
      ...(item.inboxItems || []).map((inboxItem) => renderInboxItemCard(h, inboxItem)),
      item.text ? renderMarkdown(h, item.text) : h.empty,
      item.images.length > 0
        ? h.div(
            [h.Class("flex flex-wrap gap-2")],
            item.images.map((file, position) =>
              h.img([
                h.Key(`queued-image-${position}`),
                h.Src(fileUrl(file)),
                h.Alt(`Picture ${position + 1} of an unsent message`),
                h.Class("max-h-48 max-w-full rounded-lg border object-contain"),
              ]),
            ),
          )
        : h.empty,
      failure
        ? h.p(
            [h.Role("alert"), h.Class("grid gap-0.5 text-xs text-destructive")],
            [
              h.span([], [failure.error]),
              h.span([], [`Next try at ${clockSeconds(failure.retryAt)}`]),
            ],
          )
        : h.empty,
      failure || deletable
        ? h.div(
            [h.Class("flex gap-2")],
            [
              failure
                ? h.button(
                    [
                      h.Type("button"),
                      h.Class(buttonClass({ variant: "outline", size: "sm" })),
                      h.OnClick(Message.RetryNowClicked()),
                    ],
                    ["Retry now"],
                  )
                : h.empty,
              deletable
                ? h.button(
                    [
                      h.Type("button"),
                      h.Class(buttonClass({ variant: "ghost", size: "icon-sm" })),
                      h.AriaLabel("Delete the message"),
                      h.Attribute("title", "Delete the message"),
                      h.OnClick(Message.DiscardQueuedClicked({ id: item.id })),
                    ],
                    [trash2Icon(h)],
                  )
                : h.empty,
            ],
          )
        : h.empty,
    ],
  );
}

const briefCutHeight = 144;

// The button of the Brief bubble shows only when the text is taller than the cut height.
const BriefLength = Mount.defineStream("BriefLength", {
  messages: [Message.BriefMeasured],
  execute: ({ element }) =>
    Stream.callback<ReturnType<typeof Message.BriefMeasured>>((queue) =>
      Effect.gen(function* () {
        const content = element.firstElementChild;
        const measure = () =>
          Queue.offerUnsafe(
            queue,
            Message.BriefMeasured({ long: (content?.clientHeight ?? 0) > briefCutHeight }),
          );
        measure();
        const observer = new ResizeObserver(measure);
        if (content) {
          observer.observe(content);
        }
        yield* Effect.addFinalizer(() => Effect.sync(() => observer.disconnect()));
      }),
    ),
});

function renderBriefBubble(h: H, model: Model): Html {
  const brief = model.brief as Workstream | null;
  if (!brief || !model.loaded || model.older) return h.empty;

  return h.section(
    [
      h.AriaLabel("Brief"),
      h.Class(
        "grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 justify-self-start rounded-xl border bg-card px-3 py-2",
      ),
    ],
    [
      h.span([h.Class("text-xs text-muted-foreground")], ["Brief"]),
      h.span([h.Class("font-medium wrap-anywhere")], [brief.title]),
      h.div(
        [
          h.Class(cn("min-w-0", !model.briefOpen && "max-h-36 overflow-hidden")),
          h.OnMount(BriefLength()),
        ],
        [renderMarkdown(h, brief.brief)],
      ),
      model.briefLong
        ? h.button(
            [
              h.Type("button"),
              h.DataAttribute("slot", "button"),
              h.Class(
                buttonClass({ variant: "ghost", size: "sm", className: "justify-self-start" }),
              ),
              h.OnClick(Message.BriefToggled()),
            ],
            [model.briefOpen ? "Show less" : "Show all"],
          )
        : h.empty,
    ],
  );
}

function renderTrayButtons(h: H, item: InboxItem, model: Model): Html[] {
  const buttons: Html[] = [];
  if (item.kind === InboxItemKind.question || item.action) {
    buttons.push(
      h.button(
        [
          h.Type("button"),
          h.Class(buttonClass({ size: "sm" })),
          h.OnClick(Message.OpenAnswerPanel({ item })),
        ],
        ["Answer"],
      ),
    );
  }
  if (item.kind === InboxItemKind.stopped || item.kind === InboxItemKind.lost) {
    const isResuming = model.resumingId === item.id;
    buttons.push(
      h.button(
        [
          h.Type("button"),
          h.Disabled(model.resumingId !== null),
          h.Class(buttonClass({ size: "sm" })),
          h.OnClick(Message.ResumeClicked({ item })),
        ],
        [
          isResuming ? spinnerIcon(h) : h.empty,
          item.kind === InboxItemKind.stopped ? "Resume" : "Start again",
        ],
      ),
    );
  }
  if (item.kind === InboxItemKind.stopped) {
    buttons.push(
      h.button(
        [
          h.Type("button"),
          h.Class(buttonClass({ variant: "outline", size: "sm" })),
          h.OnClick(Message.OpenAnswerPanel({ item })),
        ],
        ["Answer"],
      ),
    );
  }
  if ((item.kind === InboxItemKind.question || item.kind === InboxItemKind.stopped) && item.link) {
    buttons.push(
      h.a(
        [
          h.Attribute("href", item.link),
          h.Attribute("target", "_blank"),
          h.Attribute("rel", "noreferrer"),
          h.Class(buttonClass({ variant: "outline", size: "sm" })),
        ],
        ["Open GitHub"],
      ),
    );
  }
  if (item.kind === InboxItemKind.Lead_failed) {
    buttons.push(
      h.button([h.Type("button"), h.Class(buttonClass({ size: "sm" }))], ["Send again"]),
    );
  }
  const isPullRequest = item.kind === InboxItemKind.ready_for_review;
  const isTodo = item.kind === InboxItemKind.todo;
  if (isTodo) {
    buttons.push(
      h.button(
        [
          h.Type("button"),
          h.Class(buttonClass({ size: "sm" })),
          h.OnClick(Message.OpenAnswerPanel({ item })),
        ],
        ["Complete"],
      ),
    );
  }
  if (isPullRequest) {
    buttons.push(h.button([h.Type("button"), h.Class(buttonClass({ size: "sm" }))], ["Merge"]));
  }
  if ((isPullRequest || isTodo) && item.pullRequestUrl) {
    buttons.push(
      h.a(
        [
          h.Attribute("href", item.pullRequestUrl),
          h.Attribute("target", "_blank"),
          h.Attribute("rel", "noreferrer"),
          h.Class(buttonClass({ variant: "outline", size: "sm" })),
        ],
        ["Open PR"],
      ),
    );
  }
  const reference = !item.issue
    ? ""
    : isPullRequest || isTodo
      ? `PR #${item.pullRequest}`
      : `#${item.issue}`;
  const referenceLink = isPullRequest || isTodo ? item.pullRequestUrl : item.link;
  const draftItem: DraftInboxItem = {
    id: item.id,
    reference,
    title: item.title,
    reason: item.reason,
    link: referenceLink,
  };
  buttons.push(
    h.button(
      [
        h.Type("button"),
        h.Class(buttonClass({ variant: "outline", size: "sm" })),
        h.OnClick(Message.DraftInboxItemAdded({ item: draftItem })),
      ],
      ["Chat"],
    ),
  );
  return buttons;
}

function renderTraySideButtons(h: H, item: InboxItem): Html[] {
  const buttons: Html[] = [];
  if (item.kind !== InboxItemKind.question && item.kind !== InboxItemKind.stopped && !item.action) {
    buttons.push(
      h.button(
        [
          h.Type("button"),
          h.Class(buttonClass({ variant: "ghost", size: "sm" })),
          h.OnClick(Message.OpenDetailsPanel({ item })),
        ],
        ["Details"],
      ),
    );
  }
  if (item.kind === InboxItemKind.Lead_failed || item.kind === InboxItemKind.tool_upgrade) {
    buttons.push(
      h.button(
        [h.Type("button"), h.Class(buttonClass({ variant: "outline", size: "sm" }))],
        ["Dismiss"],
      ),
    );
  }
  return buttons;
}

function trayRows(model: Model) {
  return (model.trayItems as InboxItem[])
    .filter((item) => !item.action || item.action.state === "pending")
    .toSorted((a, b) => a.id - b.id);
}

function renderActionTray(h: H, model: Model): Html {
  const rows = trayRows(model);
  if (rows.length === 0) return h.empty;

  return h.div(
    [
      h.Class(
        "grid grid-cols-1 gap-1 border-t py-2 group-has-[textarea:focus]/chat:pointer-coarse:hidden md:px-4",
      ),
    ],
    [
      h.h3(
        [h.Class("text-xs font-semibold tracking-wide text-muted-foreground uppercase")],
        [
          h.button(
            [
              h.Type("button"),
              h.AriaExpanded(!model.trayCollapsed),
              h.Class("group flex w-full items-center gap-1 px-4 text-left uppercase md:px-0"),
              h.OnClick(Message.TrayToggled()),
            ],
            [
              `Waits for you (${rows.length})`,
              chevronRightIcon(
                h,
                `size-4 shrink-0 transition-transform ${!model.trayCollapsed ? "rotate-90" : ""}`,
              ),
            ],
          ),
        ],
      ),
      h.div(
        [
          h.DataAttribute("slot", "collapsible-content"),
          h.DataAttribute("state", model.trayCollapsed ? "closed" : "open"),
          h.Class(cn("grid gap-1", model.trayCollapsed && "hidden")),
        ],
        [
          model.trayError
            ? h.div(
                [h.Class("px-4 md:px-0")],
                [
                  h.span(
                    [
                      h.DataAttribute("slot", "badge"),
                      h.Class(badgeClass("destructive", "h-auto whitespace-normal")),
                    ],
                    [model.trayError],
                  ),
                ],
              )
            : h.empty,
          h.ul(
            [
              h.AriaLabel("Pending actions"),
              h.Class("max-h-[35svh] divide-y overflow-y-auto md:-mx-2"),
            ],
            rows.map((item) => {
              const reference = !item.issue
                ? ""
                : item.kind === InboxItemKind.ready_for_review || item.kind === InboxItemKind.todo
                  ? `PR #${item.pullRequest}`
                  : `#${item.issue}`;
              const referenceLink =
                item.kind === InboxItemKind.ready_for_review || item.kind === InboxItemKind.todo
                  ? item.pullRequestUrl
                  : item.link;
              const refNode = reference
                ? referenceLink
                  ? h.a(
                      [
                        h.Attribute("href", referenceLink),
                        h.Attribute("target", "_blank"),
                        h.Attribute("rel", "noreferrer"),
                        h.Class(
                          "shrink-0 font-medium text-foreground underline-offset-4 hover:underline",
                        ),
                      ],
                      [reference],
                    )
                  : h.span([h.Class("shrink-0 font-medium")], [reference])
                : h.empty;
              const todoBadge = item.todo
                ? h.span(
                    [
                      h.Attribute("data-slot", "badge"),
                      h.Attribute("data-variant", item.todo.mandatory ? "default" : "outline"),
                      h.Class(
                        `group/badge inline-flex h-5 w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-4xl border px-2 py-0.5 text-xs font-medium whitespace-nowrap ${
                          item.todo.mandatory
                            ? "border-transparent bg-primary text-primary-foreground"
                            : "border-border text-foreground"
                        }`,
                      ),
                    ],
                    [`Todo · ${item.todo.mandatory ? "mandatory" : "optional"}`],
                  )
                : h.empty;
              return h.li(
                [
                  h.Class(
                    "flex min-h-11 items-center gap-4 px-4 py-2 md:px-2 flex-wrap gap-y-2 py-2",
                  ),
                ],
                [
                  h.div(
                    [
                      h.Class(
                        "flex min-w-0 grow basis-full flex-wrap items-center gap-x-2 gap-y-2 sm:flex-nowrap",
                      ),
                    ],
                    [
                      h.span(
                        [
                          h.Class(
                            "flex min-w-0 grow basis-full flex-wrap items-baseline gap-x-2 sm:basis-0 sm:flex-nowrap",
                          ),
                        ],
                        [
                          todoBadge,
                          refNode,
                          h.span(
                            [h.Class("contents sm:flex sm:min-w-0 sm:items-baseline sm:gap-x-2")],
                            [
                              h.span(
                                [
                                  h.Class(
                                    "grow basis-0 truncate sm:max-w-full sm:grow-0 sm:basis-auto",
                                  ),
                                ],
                                [item.title],
                              ),
                              item.reason
                                ? h.span(
                                    [
                                      h.Class(
                                        "basis-full truncate text-sm text-muted-foreground sm:basis-auto",
                                      ),
                                    ],
                                    [item.reason],
                                  )
                                : h.empty,
                            ],
                          ),
                        ],
                      ),
                      h.div(
                        [
                          h.Class(
                            "flex basis-full items-center gap-2 sm:basis-auto sm:shrink-0 sm:gap-4",
                          ),
                        ],
                        [
                          h.div(
                            [h.Class("flex flex-wrap items-center gap-2 empty:hidden")],
                            renderTrayButtons(h, item, model),
                          ),
                          h.div(
                            [h.Class("ml-auto flex items-center gap-2")],
                            renderTraySideButtons(h, item),
                          ),
                        ],
                      ),
                    ],
                  ),
                  (item.merging ? "" : item.mergeRefusal) || item.action?.error
                    ? h.p(
                        [
                          h.Role("alert"),
                          h.Class("basis-full text-sm text-destructive wrap-anywhere"),
                        ],
                        [(item.merging ? "" : item.mergeRefusal) || (item.action?.error ?? "")],
                      )
                    : h.empty,
                ],
              );
            }),
          ),
        ],
      ),
    ],
  );
}

function textColor(color: string): string {
  const [red, green, blue] = [0, 2, 4].map((start) => parseInt(color.slice(start, start + 2), 16));
  return (0.2126 * red + 0.7152 * green + 0.0722 * blue) / 255 > 0.453 ? "#000000" : "#ffffff";
}

function renderLabelChips(h: H, labels: ActionLabel[]): Html {
  return h.div(
    [h.Class("flex flex-wrap gap-1")],
    labels.map((label) =>
      label.color
        ? h.span(
            [
              h.DataAttribute("slot", "badge"),
              h.Class(badgeClass("default", "wrap-anywhere")),
              h.Style({ background: `#${label.color}`, color: textColor(label.color) }),
            ],
            [label.name],
          )
        : h.span(
            [h.DataAttribute("slot", "badge"), h.Class(badgeClass("outline", "wrap-anywhere"))],
            [label.name],
          ),
    ),
  );
}

function renderRemovedSwitch(h: H, checked: boolean): Html {
  return h.div(
    [h.Class("flex items-center gap-2")],
    [
      h.button(
        [
          h.Type("button"),
          h.Role("switch"),
          h.AriaChecked(checked),
          h.AriaLabel("Show removed text"),
          h.Attribute("data-slot", "switch"),
          h.Attribute("data-state", checked ? "checked" : "unchecked"),
          h.Class(
            `peer group/switch relative inline-flex shrink-0 items-center rounded-full border border-transparent transition-all outline-none h-[18.4px] w-[32px] ${
              checked ? "bg-primary" : "bg-input"
            }`,
          ),
          h.OnClick(Message.ToggleRemovedText()),
        ],
        [
          h.span([
            h.Attribute("data-slot", "switch-thumb"),
            h.Class(
              `pointer-events-none block rounded-full bg-background ring-0 transition-transform size-4 ${
                checked ? "translate-x-[calc(100%-2px)]" : "translate-x-0"
              }`,
            ),
          ]),
        ],
      ),
      h.label(
        [
          h.Class("text-sm font-medium leading-none cursor-pointer"),
          h.OnClick(Message.ToggleRemovedText()),
        ],
        ["Show removed text"],
      ),
    ],
  );
}

function renderFullArgument(h: H, arg: ActionArgument, model: Model, action?: PendingAction): Html {
  if (arg.name === "labels") {
    return (arg.labels || []).length > 0 ? renderLabelChips(h, arg.labels) : h.empty;
  }
  const before =
    action?.tool === "edit_brief" && arg.name === "brief"
      ? action.state === "pending"
        ? model.briefActionBrief || undefined
        : action.oldBrief || undefined
      : undefined;
  if (before !== undefined) {
    const marked = briefMarkdown(diffLines(before, arg.value), !model.showRemovedText);
    return h.div(
      [h.Class("grid min-w-0 gap-0.5")],
      [
        h.span([h.Class("text-xs text-muted-foreground")], [arg.name]),
        renderMarkdown(h, marked),
        renderRemovedSwitch(h, model.showRemovedText),
      ],
    );
  }
  const isShort = !arg.value.includes("\n") && arg.value.length <= 80;
  return h.div(
    [h.Class("grid min-w-0 gap-0.5")],
    [
      h.span([h.Class("text-xs text-muted-foreground")], [arg.name]),
      isShort ? h.span([h.Class("wrap-anywhere")], [arg.value]) : renderMarkdown(h, arg.value),
    ],
  );
}

const PanelOpened = Mount.define("PanelOpened", {
  messages: [Message.CompletedFocusPanel],
  execute: ({ element }) =>
    Effect.sync(() => {
      (element as HTMLElement).focus();
      return Message.CompletedFocusPanel();
    }),
});

const link = "text-primary underline-offset-4 hover:underline";

function renderOutcome(h: H, label: string, text: string): Html {
  return h.div(
    [h.Class("grid min-w-0 gap-0.5")],
    [
      h.span([h.Class("text-xs text-muted-foreground")], [label]),
      h.span([h.Class("wrap-anywhere whitespace-pre-line")], [text]),
    ],
  );
}

// The options of a question as buttons. A click selects or clears an option. In a single choice, a click on an option
// clears the other option.
function renderOptions(h: H, model: Model, question: QuestionType, index: number): Html {
  const selected = (model.selectedOptions as Record<number, number[]>)[index] ?? [];
  return h.div(
    [h.Role("group"), h.AriaLabel(question.text), h.Class("grid gap-2")],
    [
      h.p(
        [h.Class("wrap-anywhere")],
        [
          h.span([h.Class("font-medium")], [question.text]),
          " ",
          h.span(
            [h.Class("text-sm text-muted-foreground")],
            [question.multiple ? "Select one or more." : "Select one."],
          ),
        ],
      ),
      ...question.options.map((option, position) => {
        const on = selected.includes(position);
        return h.button(
          [
            h.Key(`option-${position}`),
            h.Type("button"),
            h.Attribute("aria-pressed", String(on)),
            h.Class(
              cn(
                "flex w-full items-start gap-3 rounded-lg border p-3 text-left",
                on && "border-primary",
              ),
            ),
            h.OnClick(
              Message.OptionToggled({
                questionIndex: index,
                optionIndex: position,
                multiple: question.multiple,
              }),
            ),
          ],
          [
            h.span([
              h.AriaHidden(true),
              h.Class(
                cn(
                  "mt-0.5 size-4 shrink-0 border",
                  question.multiple ? "rounded-sm" : "rounded-full",
                  on ? "border-primary bg-primary" : "border-input",
                ),
              ),
            ]),
            h.span(
              [h.Class("grid min-w-0 gap-0.5")],
              [
                h.span([h.Class("wrap-anywhere")], [option.title]),
                option.details
                  ? h.span(
                      [h.Class("text-sm text-muted-foreground wrap-anywhere")],
                      [option.details],
                    )
                  : h.empty,
              ],
            ),
          ],
        );
      }),
    ],
  );
}

function workstreamName(model: Model, item: InboxItem) {
  if (item.workstream === 0) {
    return "Coordinator";
  }
  return (
    (model.workstreams as Workstream[] | null)?.find(
      (workstream) =>
        workstream.repository === item.workstreamRepository &&
        workstream.number === item.workstream,
    )?.title ?? `#${item.workstream}`
  );
}

function renderAnswerDialog(h: H, model: Model): Html {
  const panel = model.activePanel;
  if (!panel) return h.empty;

  const item = panel.item;
  const input = panel.type === "answer";
  const action = item.action;
  const todo = item.todo;
  if (input && action && action.state !== "pending") {
    return h.empty;
  }
  const title = item.title || item.reason;

  const body = action
    ? h.div(
        [h.Class("grid min-w-0 content-start gap-2 overflow-y-auto px-4 pb-4")],
        [
          renderActionSender(h, action, true),
          ...(action.arguments || [])
            .filter((arg) => !(action.tool === "message_lead" && arg.name === "workstream"))
            .map((arg) => renderFullArgument(h, arg, model, action)),
          ...(action.state !== "pending"
            ? [
                h.span(
                  [
                    h.DataAttribute("slot", "badge"),
                    h.Class(badgeClass("secondary", "justify-self-start")),
                  ],
                  [actionState(action)],
                ),
                action.state === "done" ? renderOutcome(h, "Result", action.result) : h.empty,
                action.message ? renderOutcome(h, "Your message", action.message) : h.empty,
              ]
            : []),
        ],
      )
    : todo
      ? h.div(
          [h.Class("grid min-w-0 content-start gap-2 overflow-y-auto px-4 pb-4 wrap-anywhere")],
          [
            h.span(
              [h.Class("text-muted-foreground")],
              [`${todo.mandatory ? "Mandatory" : "Optional"} ${todo.kind.replace("_", " ")}`],
            ),
            h.p([], [`Reason: ${todo.reason}`]),
            h.p([], [`File: ${todo.path}:${todo.line}`]),
            h.p(
              [],
              [
                "Pull request: ",
                h.a(
                  [
                    h.Attribute("href", item.pullRequestUrl),
                    h.Attribute("target", "_blank"),
                    h.Attribute("rel", "noreferrer"),
                    h.Class(link),
                  ],
                  [`PR #${item.pullRequest} ↗`],
                ),
              ],
            ),
            todo.mandatory ? h.p([], ["Mobius merges after you complete this todo."]) : h.empty,
          ],
        )
      : h.div(
          [h.Class("grid content-start gap-2 overflow-y-auto px-4 pb-4")],
          [
            h.span(
              [h.Class("text-muted-foreground")],
              [`${workstreamName(model, item)} · ${dayClock(item.time)}`],
            ),
            renderMarkdown(h, item.text),
            ...item.questions.map((question, index) => renderOptions(h, model, question, index)),
            !input && item.answer ? renderOutcome(h, "Your answer", item.answer) : h.empty,
            item.reportUrl
              ? h.a(
                  [
                    h.Attribute("href", item.reportUrl),
                    h.Attribute("target", "_blank"),
                    h.Attribute("rel", "noreferrer"),
                    h.Class(link),
                  ],
                  ["Approval report"],
                )
              : h.empty,
          ],
        );

  return h.div(
    [h.DataAttribute("slot", "dialog-portal")],
    [
      h.div([
        h.DataAttribute("slot", "dialog-overlay"),
        h.DataAttribute("state", "open"),
        h.Class(
          "fixed inset-0 isolate z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs",
        ),
        h.OnClick(Message.ClosePanel()),
      ]),
      h.div(
        [
          h.Role("dialog"),
          h.Tabindex(-1),
          h.AriaLabelledBy("answer-title"),
          h.DataAttribute("slot", "dialog-content"),
          h.DataAttribute("state", "open"),
          h.OnMount(PanelOpened()),
          h.Class(
            cn(
              "fixed top-1/2 left-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl bg-popover p-4 text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm",
              "grid max-h-[calc(100dvh-var(--keyboard,0px)-2rem)] grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 p-0 max-sm:top-auto max-sm:bottom-(--keyboard,0px) max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-b-none sm:max-h-[85dvh] sm:max-w-lg",
            ),
          ),
        ],
        [
          h.div(
            [h.DataAttribute("slot", "dialog-header"), h.Class("flex flex-col gap-1 p-4 pr-10")],
            [
              h.h2(
                [
                  h.Id("answer-title"),
                  h.DataAttribute("slot", "dialog-title"),
                  h.Class("font-heading text-base leading-none font-medium wrap-anywhere"),
                ],
                [title],
              ),
              item.title && !action && !todo
                ? h.p([h.Class("text-sm text-muted-foreground")], [item.reason])
                : h.empty,
            ],
          ),
          body,
          input ? renderPanelComposer(h, model, item) : h.empty,
          h.button(
            [
              h.Type("button"),
              h.DataAttribute("slot", "dialog-close"),
              h.Class(
                buttonClass({
                  variant: "ghost",
                  size: "icon-sm",
                  className: "absolute top-2 right-2",
                }),
              ),
              h.OnClick(Message.ClosePanel()),
            ],
            [xIcon(h), h.span([h.Class("sr-only")], ["Close"])],
          ),
        ],
      ),
    ],
  );
}

export function view(model: Model, h: H): Html {
  const messages = model.messages as ChatMessage[];
  const queue = model.queue as Waiting[];
  const storedIds = new Set(messages.map((m) => m.browserId));
  const pending = queue.filter((item) => !storedIds.has(item.id));
  const count = messages.length + pending.length;
  const next = pending.find((item) => !item.sent);
  const rows = trayRows(model);
  const unreadMessages = messages.filter(countsAsUnread);
  const waiting =
    model.older && model.openUnread !== null && unreadMessages.length < model.openUnread;
  const showBrief = Boolean(model.brief) && model.loaded && !model.older;
  const listReady =
    model.loaded &&
    model.queueLoaded &&
    model.openUnread !== null &&
    !waiting &&
    !(showBrief && !model.briefMeasured);
  const firstUnread =
    model.openUnread !== null && model.openUnread > 0
      ? (unreadMessages[Math.max(0, unreadMessages.length - model.openUnread)]?.id ?? "")
      : "";

  return h.div(
    [h.Class("flex min-h-0 min-w-0 grow flex-col")],
    [
      // Main scrollable message list
      h.div(
        [
          h.OnMount(ChatList()),
          h.DataAttribute("ready", String(listReady)),
          h.DataAttribute("first-unread", String(firstUnread)),
          h.Class(
            "grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-3 overflow-y-auto overscroll-contain bg-muted/40 p-4",
          ),
        ],
        [
          model.error
            ? h.span(
                [h.DataAttribute("slot", "badge"), h.Class(badgeClass("destructive"))],
                [model.error],
              )
            : h.empty,
          renderBriefBubble(h, model),
          model.loaded && model.queueLoaded && count === 0
            ? h.p(
                [h.Class("text-center text-sm text-muted-foreground")],
                ["No messages. Write to start a chat session."],
              )
            : h.empty,
          // Render messages
          ...messages.flatMap((msg, idx) => {
            const label = dayLabel(msg.time);
            const prevLabel = idx > 0 ? dayLabel(messages[idx - 1].time) : null;
            const items = [];
            if (idx === 0 || label !== prevLabel) {
              items.push(renderDaySeparator(h, label));
            }
            items.push(
              renderMessage(h, msg, (model.openEvents as number[]).includes(msg.id), rows),
            );
            return items;
          }),
          // Render queued messages
          ...pending.flatMap((queued, idx) => {
            const label = dayLabel(queued.time);
            const prev = idx === 0 ? messages.at(-1) : pending[idx - 1];
            const prevLabel = prev ? dayLabel(prev.time) : null;
            const items = [];
            if (!prevLabel || label !== prevLabel) {
              items.push(renderDaySeparator(h, label));
            }
            items.push(
              renderQueuedMessage(
                h,
                queued,
                model.attempt?.id === queued.id ? (model.attempt as Failure) : null,
                !queued.sent && (queued !== next || model.attempt?.id === queued.id),
              ),
            );
            return items;
          }),
          // Writing indicator
          model.writing
            ? h.p(
                [h.Class("flex items-center gap-2 text-sm text-muted-foreground")],
                [
                  h.span([h.Class("size-2 animate-pulse rounded-full bg-green-600")], []),
                  model.pausedUntil
                    ? `The ${model.agent} waits for the usage limit until ${dayClock(model.pausedUntil)}.`
                    : messages.some(
                          (m) =>
                            m.author === "Owner" &&
                            !m.deliveredAt &&
                            !m.stoppedAt &&
                            !m.cancelledAt,
                        )
                      ? `The message waits for the ${model.agent}.`
                      : `The ${model.agent} writes a reply.`,
                ],
              )
            : h.empty,
          // Chat session failure
          model.failure
            ? h.span(
                [
                  h.DataAttribute("slot", "badge"),
                  h.Class(
                    badgeClass("destructive", "h-auto w-full justify-start whitespace-normal"),
                  ),
                ],
                [`The chat session failed: ${model.failure}`],
              )
            : h.empty,
        ],
      ),
      // Action Tray
      renderActionTray(h, model),
      // Composer
      renderComposer(h, model),
      // Modal dialogs
      renderAnswerDialog(h, model),
    ],
  );
}
