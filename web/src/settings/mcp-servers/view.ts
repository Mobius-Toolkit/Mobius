import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "@/ui/badge";
import { buttonClass, renderButton } from "@/ui/button";
import { defineDialogMount, renderDialog } from "@/ui/dialog";
import { spinnerIcon, xIcon } from "@/ui/icons";
import { inputClass } from "@/ui/input";
import { renderErrorBadge, renderList, renderPageHeader, renderRow } from "@/ui/page";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { textareaClass } from "@/ui/textarea";
import { Message } from "./message";
import {
  canConnect,
  kindText,
  pairsOf,
  serverNamed,
  type Model,
  type Sheet,
  type SheetPair,
} from "./model";
import type { McpServer } from "./schemas";

type H = HtmlBuilder<Message>;

const SheetOpened = defineInPlaceMount(Message.CompletedFocusSheet);
const DialogOpened = defineDialogMount(Message.CompletedFocusDialog);

const mask = "••••••";

const muted = "text-sm text-muted-foreground";

function renderServerRow(h: H, server: McpServer): Html {
  const secrets = server.kind === "http" ? server.headers : server.env;
  const target = server.kind === "http" ? server.url : [server.command, ...server.args].join(" ");
  return renderRow(h, String(server.id), "items-start", [
    h.span(
      [h.Class("grid min-w-0 grow gap-0.5")],
      [
        h.span(
          [h.Class("flex flex-wrap items-center gap-2")],
          [
            h.span([h.Class("wrap-anywhere")], [server.name]),
            renderBadge(h, "secondary", kindText(server.kind)),
            server.oauth === "connected" ? renderBadge(h, "secondary", "OAuth") : h.empty,
            server.oauth === "reconnect" ? renderBadge(h, "destructive", "Reconnect") : h.empty,
          ],
        ),
        h.span([h.Class(`${muted} break-all`)], [target]),
        secrets.length > 0
          ? h.span(
              [h.Class(`flex flex-wrap gap-x-3 ${muted}`)],
              secrets.map((secret) =>
                h.keyed("span")(secret.name, [h.Class("break-all")], [`${secret.name} ${mask}`]),
              ),
            )
          : h.empty,
      ],
    ),
    renderButton(h, {
      label: "Edit",
      variant: "outline",
      size: "sm",
      onClick: Message.ClickedEdit({ id: server.id }),
    }),
  ]);
}

function renderField(h: H, label: string, id: string, children: ReadonlyArray<Html>): Html {
  return h.div(
    [h.Class("grid grid-cols-[6.5rem_1fr] items-center gap-2")],
    [
      h.label([h.For(id), h.Class("text-sm leading-none font-medium select-none")], [label]),
      h.div([h.Class("flex min-w-0 items-center gap-2")], children),
    ],
  );
}

function renderTextField(
  h: H,
  options: {
    label: string;
    id: string;
    value: string;
    placeholder?: string;
    secret?: boolean;
    onInput: (text: string) => Message;
  },
): Html {
  return renderField(h, options.label, options.id, [
    h.input([
      h.Id(options.id),
      h.Type(options.secret ? "password" : "text"),
      h.Autocomplete(options.secret ? "new-password" : "off"),
      h.Spellcheck(false),
      h.Placeholder(options.placeholder ?? ""),
      h.Value(options.value),
      h.OnInput(options.onInput),
      h.Class(inputClass()),
    ]),
  ]);
}

function renderKind(h: H, sheet: Sheet): Html {
  return renderField(h, "Kind", "mcp-kind-http", [
    h.div(
      [h.Role("radiogroup"), h.AriaLabel("Kind"), h.Class("flex items-center gap-4")],
      (["http", "stdio"] as const).map((kind) =>
        h.keyed("label")(
          kind,
          [h.Class("flex items-center gap-1.5")],
          [
            h.input([
              h.Id(`mcp-kind-${kind}`),
              h.Type("radio"),
              h.Name("mcp-kind"),
              h.Checked(sheet.kind === kind),
              h.OnClick(Message.PickedKind({ kind })),
              h.Class("size-4 accent-primary"),
            ]),
            kindText(kind),
          ],
        ),
      ),
    ),
  ]);
}

function renderPair(h: H, sheet: Sheet, pair: SheetPair, index: number): Html {
  const noun = sheet.kind === "http" ? "header" : "variable";
  return h.keyed("div")(
    String(index),
    [h.Class("grid grid-cols-[1fr_1fr_auto] items-center gap-2")],
    [
      h.input([
        h.Type("text"),
        h.AriaLabel(`Name of ${noun} ${index + 1}`),
        h.Autocomplete("off"),
        h.Spellcheck(false),
        h.Placeholder("Name"),
        h.Value(pair.name),
        h.OnInput((text) => Message.ChangedPairName({ index, text })),
        h.Class(inputClass()),
      ]),
      h.input([
        h.Type("password"),
        h.AriaLabel(`Value of ${noun} ${index + 1}`),
        h.Autocomplete("new-password"),
        h.Placeholder(pair.stored ? mask : "Value"),
        h.Value(pair.value),
        h.OnInput((text) => Message.ChangedPairValue({ index, text })),
        h.Class(inputClass()),
      ]),
      h.button(
        [
          h.Type("button"),
          h.AriaLabel(`Remove ${noun} ${index + 1}`),
          h.Class(buttonClass({ variant: "ghost", size: "icon-sm" })),
          h.OnClick(Message.ClickedRemovePair({ index })),
        ],
        [xIcon(h)],
      ),
    ],
  );
}

function renderPairs(h: H, sheet: Sheet): Html {
  const plural = sheet.kind === "http" ? "Headers" : "Environment";
  return h.div(
    [h.Class("grid gap-2")],
    [
      h.h3([h.Class("text-sm font-medium")], [plural]),
      ...pairsOf(sheet).map((pair, index) => renderPair(h, sheet, pair, index)),
      h.div(
        [],
        [
          renderButton(h, {
            label: sheet.kind === "http" ? "Add a header" : "Add a variable",
            variant: "outline",
            size: "sm",
            onClick: Message.ClickedAddPair(),
          }),
        ],
      ),
    ],
  );
}

function renderTarget(h: H, sheet: Sheet): Html[] {
  return sheet.kind === "http"
    ? [
        renderTextField(h, {
          label: "URL",
          id: "mcp-url",
          value: sheet.url,
          placeholder: "https://",
          onInput: (text) => Message.ChangedUrl({ text }),
        }),
      ]
    : [
        renderTextField(h, {
          label: "Command",
          id: "mcp-command",
          value: sheet.command,
          onInput: (text) => Message.ChangedCommand({ text }),
        }),
        renderField(h, "Arguments", "mcp-args", [
          h.textarea([
            h.Id("mcp-args"),
            h.Rows(2),
            h.Spellcheck(false),
            h.Placeholder("One argument on each line"),
            h.Value(sheet.args),
            h.OnInput((text) => Message.ChangedArgs({ text })),
            h.Class(textareaClass("min-h-16")),
          ]),
        ]),
      ];
}

function oauthState(server: McpServer | undefined) {
  if (server?.oauth === "connected") {
    return "Connected";
  }
  if (server?.oauth === "reconnect") {
    return `Needs a new Connect: ${server.oauthReason}`;
  }
  return "Not connected";
}

function renderOAuth(h: H, sheet: Sheet, server: McpServer | undefined): Html {
  const busy = sheet.pending !== "none";
  return h.div(
    [h.Class("grid gap-2")],
    [
      h.h3([h.Class("text-sm font-medium")], ["OAuth"]),
      h.p([h.Class(`${muted} wrap-anywhere`)], [oauthState(server)]),
      renderTextField(h, {
        label: "Client ID",
        id: "mcp-client-id",
        value: sheet.clientId,
        onInput: (text) => Message.ChangedClientId({ text }),
      }),
      renderTextField(h, {
        label: "Client secret",
        id: "mcp-client-secret",
        value: sheet.clientSecret,
        secret: true,
        onInput: (text) => Message.ChangedClientSecret({ text }),
      }),
      h.div(
        [h.Class("flex items-center gap-2")],
        [
          renderButton(h, {
            label: "Connect",
            variant: "outline",
            size: "sm",
            disabled: busy,
            pending: sheet.pending === "connect",
            onClick: Message.ClickedConnect(),
          }),
          server !== undefined && server.oauth !== "none"
            ? renderButton(h, {
                label: "Disconnect",
                variant: "outline",
                size: "sm",
                disabled: busy,
                pending: sheet.pending === "disconnect",
                onClick: Message.ClickedDisconnect(),
              })
            : h.empty,
        ],
      ),
    ],
  );
}

function renderSheetOf(h: H, model: Model, sheet: Sheet): Html {
  const saved = Option.flatMap(sheet.id, (id) => Option.fromNullishOr(serverNamed(model, id)));
  const busy = sheet.pending !== "none";
  return renderSheet(h, {
    mount: h.OnMount(SheetOpened()),
    titleId: "mcp-title",
    descriptionId: "mcp-description",
    title: Option.match(saved, {
      onNone: () => "Add a server",
      onSome: (server) => `Edit ${server.name}`,
    }),
    description: `An MCP server of ${model.organization}`,
    close: Message.ClosedSheet(),
    className:
      "max-h-[90dvh] gap-3 overflow-y-auto p-4 pt-3 md:mx-auto md:max-w-md md:rounded-t-xl",
    children: [
      renderTextField(h, {
        label: "Name",
        id: "mcp-name",
        value: sheet.name,
        onInput: (text) => Message.ChangedName({ text }),
      }),
      renderKind(h, sheet),
      ...renderTarget(h, sheet),
      renderPairs(h, sheet),
      canConnect(model, sheet) ? renderOAuth(h, sheet, Option.getOrUndefined(saved)) : h.empty,
      sheet.error
        ? renderBadge(h, "destructive", sheet.error, "h-auto whitespace-normal")
        : h.empty,
      h.div(
        [h.Class("flex items-center justify-between gap-2")],
        [
          Option.isSome(sheet.id)
            ? renderButton(h, {
                label: "Delete",
                variant: "outline",
                disabled: busy,
                onClick: Message.ClickedDelete(),
              })
            : h.span([]),
          h.div(
            [h.Class("flex items-center gap-2")],
            [
              renderButton(h, {
                label: "Cancel",
                variant: "outline",
                onClick: Message.ClosedSheet(),
              }),
              renderButton(h, {
                label: "Save",
                disabled: busy,
                pending: sheet.pending === "save",
                onClick: Message.ClickedSave(),
              }),
            ],
          ),
        ],
      ),
    ],
  });
}

function renderConfirmation(h: H, sheet: Sheet): Html {
  return renderDialog(h, {
    mount: h.OnMount(DialogOpened()),
    titleId: "mcp-delete-title",
    title: `Delete ${sheet.name}`,
    description: "Each Workstream that selects the server stops using it.",
    close: Message.DismissedDelete(),
    layout: "center",
    footer: h.div(
      [h.DataAttribute("slot", "dialog-footer"), h.Class("flex justify-end gap-2")],
      [
        renderButton(h, {
          label: "Cancel",
          variant: "outline",
          onClick: Message.DismissedDelete(),
        }),
        h.button(
          [
            h.Type("button"),
            h.Disabled(sheet.pending === "delete"),
            h.Class(buttonClass({ variant: "destructive" })),
            h.OnClick(Message.ClickedConfirmDelete()),
          ],
          [sheet.pending === "delete" ? spinnerIcon(h) : h.empty, "Delete"],
        ),
      ],
    ),
  });
}

export function view(model: Model, h: H): Html {
  const servers = Option.getOrUndefined(model.servers);
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "MCP servers", Message.ClickedBack()),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      servers && servers.length === 0
        ? h.p([h.Class("px-4 text-muted-foreground md:px-0")], ["No MCP servers."])
        : h.empty,
      servers
        ? renderList(
            h,
            servers.map((server) => renderServerRow(h, server)),
          )
        : h.empty,
      servers
        ? h.div(
            [h.Class("flex justify-center px-4 md:justify-start md:px-0")],
            [renderButton(h, { label: "Add a server", onClick: Message.ClickedAdd() })],
          )
        : h.empty,
      Option.match(model.sheet, {
        onNone: () => h.empty,
        onSome: (sheet) =>
          h.div(
            [h.Class("contents")],
            [
              renderSheetOf(h, model, sheet),
              sheet.confirmingDelete ? renderConfirmation(h, sheet) : h.empty,
            ],
          ),
      }),
    ],
  );
}
