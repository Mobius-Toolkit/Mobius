import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import {
  AddServer,
  ChangeServer,
  DeleteServer,
  DisconnectOAuth,
  GoBack,
  Navigate,
  Redirect,
  RequestLogin,
  ShowNotice,
  StartOAuth,
} from "./commands";
import { Message } from "./message";
import {
  argsOf,
  newSheet,
  pairsOf,
  serverNamed,
  sheetOf,
  toPairs,
  withPairs,
  type Model,
  type Sheet,
} from "./model";
import type { Body, Callback, McpServer } from "./schemas";

const path = "/settings/mcp-servers";

const withSheet = (model: Model, change: (sheet: Sheet) => Sheet): Model => ({
  ...model,
  sheet: Option.map(model.sheet, change),
});

const withServers = (
  model: Model,
  change: (servers: readonly McpServer[]) => McpServer[],
): Model => ({
  ...model,
  servers: Option.map(model.servers, change),
});

const byName = (a: McpServer, b: McpServer) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);

const noticeOf = (model: Model, callback: Callback) => {
  const name = serverNamed(model, callback.server)?.name ?? `#${callback.server}`;
  return callback.oauth === "connected"
    ? `Connected ${name}`
    : `The connect of ${name} failed: ${callback.reason}`;
};

const bodyOf = (sheet: Sheet): Body =>
  sheet.kind === "http"
    ? {
        name: sheet.name,
        kind: "http",
        url: sheet.url,
        command: "",
        args: [],
        headers: toPairs(sheet.headers),
        env: [],
      }
    : {
        name: sheet.name,
        kind: "stdio",
        url: "",
        command: sheet.command,
        args: argsOf(sheet.args),
        headers: [],
        env: toPairs(sheet.env),
      };

const pending = (sheet: Sheet, kind: Sheet["pending"]): Sheet => ({
  ...sheet,
  pending: kind,
  error: "",
});

const failed = (sheet: Sheet, error: string): Sheet => ({ ...sheet, pending: "none", error });

const withPair = (
  model: Model,
  index: number,
  change: (pair: Sheet["headers"][number]) => Sheet["headers"][number],
) =>
  withSheet(model, (sheet) =>
    withPairs(
      sheet,
      pairsOf(sheet).map((pair, other) => (other === index ? change(pair) : pair)),
    ),
  );

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ServersLoaded: ({ servers }) => {
      const loaded = { ...model, servers: Option.some(servers) };
      return Option.match(model.callback, {
        onNone: () => ({ model: loaded }),
        onSome: (callback) => ({
          model: { ...loaded, callback: Option.none() },
          commands: [ShowNotice({ text: noticeOf(loaded, callback) }), Navigate({ path })],
        }),
      });
    },
    ServersFailed: ({ error }) => ({ model: { ...model, error } }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    ClickedAdd: () => ({ model: { ...model, sheet: Option.some(newSheet()) } }),
    ClickedEdit: ({ id }) => {
      const server = serverNamed(model, id);
      return server === undefined
        ? { model }
        : { model: { ...model, sheet: Option.some(sheetOf(server)) } };
    },
    ClosedSheet: () => ({ model: { ...model, sheet: Option.none() } }),
    PressedEscape: () =>
      Option.exists(model.sheet, (sheet) => sheet.confirmingDelete)
        ? { model: withSheet(model, (sheet) => ({ ...sheet, confirmingDelete: false })) }
        : { model: { ...model, sheet: Option.none() } },
    ChangedName: ({ text }) => ({
      model: withSheet(model, (sheet) => ({ ...sheet, name: text })),
    }),
    PickedKind: ({ kind }) => ({ model: withSheet(model, (sheet) => ({ ...sheet, kind })) }),
    ChangedUrl: ({ text }) => ({ model: withSheet(model, (sheet) => ({ ...sheet, url: text })) }),
    ChangedCommand: ({ text }) => ({
      model: withSheet(model, (sheet) => ({ ...sheet, command: text })),
    }),
    ChangedArgs: ({ text }) => ({ model: withSheet(model, (sheet) => ({ ...sheet, args: text })) }),
    ChangedPairName: ({ index, text }) => ({
      model: withPair(model, index, (pair) => ({ ...pair, name: text })),
    }),
    ChangedPairValue: ({ index, text }) => ({
      model: withPair(model, index, (pair) => ({ ...pair, value: text })),
    }),
    ClickedAddPair: () => ({
      model: withSheet(model, (sheet) =>
        withPairs(sheet, [...pairsOf(sheet), { name: "", value: "", stored: false }]),
      ),
    }),
    ClickedRemovePair: ({ index }) => ({
      model: withSheet(model, (sheet) =>
        withPairs(
          sheet,
          pairsOf(sheet).filter((_, other) => other !== index),
        ),
      ),
    }),
    ChangedClientId: ({ text }) => ({
      model: withSheet(model, (sheet) => ({ ...sheet, clientId: text })),
    }),
    ChangedClientSecret: ({ text }) => ({
      model: withSheet(model, (sheet) => ({ ...sheet, clientSecret: text })),
    }),
    ClickedSave: () =>
      Option.match(model.sheet, {
        onNone: () => ({ model }),
        onSome: (sheet) => ({
          model: withSheet(model, (current) => pending(current, "save")),
          commands: [
            Option.match(sheet.id, {
              onNone: () => AddServer({ organization: model.organization, body: bodyOf(sheet) }),
              onSome: (id) =>
                ChangeServer({ organization: model.organization, id, body: bodyOf(sheet) }),
            }),
          ],
        }),
      }),
    ServerSaved: ({ server }) => ({
      model: {
        ...withServers(model, (servers) =>
          [...servers.filter((other) => other.id !== server.id), server].toSorted(byName),
        ),
        sheet: Option.none(),
      },
    }),
    SaveFailed: ({ error }) => ({ model: withSheet(model, (sheet) => failed(sheet, error)) }),
    SheetLoginRequired: () => ({
      model: withSheet(model, (sheet) => ({ ...sheet, pending: "none" })),
      commands: [RequestLogin()],
    }),
    ClickedDelete: () => ({
      model: withSheet(model, (sheet) => ({ ...sheet, confirmingDelete: true })),
    }),
    DismissedDelete: () => ({
      model: withSheet(model, (sheet) => ({ ...sheet, confirmingDelete: false })),
    }),
    ClickedConfirmDelete: () =>
      Option.match(model.sheet, {
        onNone: () => ({ model }),
        onSome: (sheet) =>
          Option.match(sheet.id, {
            onNone: () => ({ model }),
            onSome: (id) => ({
              model: withSheet(model, (current) => pending(current, "delete")),
              commands: [DeleteServer({ organization: model.organization, id, name: sheet.name })],
            }),
          }),
      }),
    ServerDeleted: ({ id }) => ({
      model: {
        ...withServers(model, (servers) => servers.filter((other) => other.id !== id)),
        sheet: Option.none(),
      },
    }),
    DeleteFailed: ({ error }) => ({
      model: withSheet(model, (sheet) => ({ ...failed(sheet, error), confirmingDelete: false })),
    }),
    ClickedConnect: () =>
      Option.match(model.sheet, {
        onNone: () => ({ model }),
        onSome: (sheet) =>
          Option.match(sheet.id, {
            onNone: () => ({ model }),
            onSome: (id) => ({
              model: withSheet(model, (current) => pending(current, "connect")),
              commands: [
                StartOAuth({
                  organization: model.organization,
                  id,
                  clientId: sheet.clientId,
                  clientSecret: sheet.clientSecret,
                }),
              ],
            }),
          }),
      }),
    OAuthStarted: ({ url }) => ({ model, commands: [Redirect({ url })] }),
    OAuthFailed: ({ error }) => ({ model: withSheet(model, (sheet) => failed(sheet, error)) }),
    ClickedDisconnect: () =>
      Option.match(model.sheet, {
        onNone: () => ({ model }),
        onSome: (sheet) =>
          Option.match(sheet.id, {
            onNone: () => ({ model }),
            onSome: (id) => ({
              model: withSheet(model, (current) => pending(current, "disconnect")),
              commands: [
                DisconnectOAuth({ organization: model.organization, id, name: sheet.name }),
              ],
            }),
          }),
      }),
    OAuthDisconnected: ({ id }) => ({
      model: withSheet(
        withServers(model, (servers) =>
          servers.map((server) =>
            server.id === id ? { ...server, oauth: "none", oauthReason: "" } : server,
          ),
        ),
        (sheet) => ({ ...sheet, pending: "none" }),
      ),
    }),
    ClickedBack: () => ({ model, commands: [GoBack()] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedGoBack: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedShowNotice: () => ({ model }),
    CompletedRedirect: () => ({ model }),
    CompletedFocusSheet: () => ({ model }),
    CompletedFocusDialog: () => ({ model }),
  });
}
