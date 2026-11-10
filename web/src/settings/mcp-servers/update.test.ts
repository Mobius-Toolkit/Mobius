import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import {
  ChangeServer,
  DisconnectOAuth,
  Navigate,
  Redirect,
  ShowNotice,
  StartOAuth,
} from "./commands";
import { Message } from "./message";
import { initialModel, type Model, type Sheet } from "./model";
import type { Callback, McpServer } from "./schemas";
import { update } from "./update";

const none: Callback = { oauth: "", server: 0, reason: "" };

const http: McpServer = {
  id: 3,
  name: "makeadir",
  kind: "http",
  url: "https://mcp.example.com/mcp",
  headers: [{ name: "Authorization" }],
  command: "",
  args: [],
  env: [],
  oauth: "none",
  oauthReason: "",
};

const stdio: McpServer = {
  id: 4,
  name: "search",
  kind: "stdio",
  url: "",
  headers: [],
  command: "npx",
  args: ["-y", "@acme/search-mcp"],
  env: [{ name: "API_KEY" }],
  oauth: "none",
  oauthReason: "",
};

const loaded = (callback: Callback = none): Model => ({
  ...initialModel({ organization: "plants", callback }),
  servers: Option.some([http, stdio]),
});

const sheet = (m: Model): Sheet => Option.getOrThrow(m.sheet);

describe("the sheet of a server", () => {
  it("opens with the stored secrets empty, and sends an empty value for a field that the Owner leaves empty", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 3 })),
      model((m) => {
        expect(sheet(m).headers).toEqual([{ name: "Authorization", value: "", stored: true }]);
      }),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        ChangeServer({
          organization: "plants",
          id: 3,
          body: {
            name: "makeadir",
            kind: "http",
            url: "https://mcp.example.com/mcp",
            command: "",
            args: [],
            headers: [{ name: "Authorization", value: "" }],
            env: [],
          },
        }),
      ),
      StoryCommand.resolve(ChangeServer, Message.SaveFailed({ error: "nope" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ pending: "none", error: "nope" });
      }),
    );
  });

  it("splits the arguments of a stdio server at the lines", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 4 })),
      message(Message.ChangedArgs({ text: "-y\n\n@acme/other-mcp\n" })),
      message(Message.ClickedSave()),
      StoryCommand.expectExact(
        ChangeServer({
          organization: "plants",
          id: 4,
          body: {
            name: "search",
            kind: "stdio",
            url: "",
            command: "npx",
            args: ["-y", "@acme/other-mcp"],
            headers: [],
            env: [{ name: "API_KEY", value: "" }],
          },
        }),
      ),
      StoryCommand.resolve(ChangeServer, Message.SaveFailed({ error: "nope" })),
    );
  });

  it("keeps the rows of the other kind when the Owner switches the kind", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 3 })),
      message(Message.PickedKind({ kind: "stdio" })),
      message(Message.ClickedAddPair()),
      message(Message.PickedKind({ kind: "http" })),
      model((m) => {
        expect(sheet(m).headers).toHaveLength(1);
        expect(sheet(m).env).toHaveLength(1);
      }),
    );
  });

  it("adds a saved server in name order and closes the sheet", () => {
    const added: McpServer = { ...stdio, id: 9, name: "alpha" };
    story(
      update,
      given(loaded()),
      message(Message.ClickedAdd()),
      message(Message.ServerSaved({ server: added })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.servers).map((server) => server.name)).toEqual([
          "alpha",
          "makeadir",
          "search",
        ]);
      }),
    );
  });

  it("closes the confirmation with the Escape key first, and then the sheet", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 3 })),
      message(Message.ClickedDelete()),
      message(Message.PressedEscape()),
      model((m) => {
        expect(sheet(m).confirmingDelete).toBe(false);
      }),
      message(Message.PressedEscape()),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
      }),
    );
  });

  it("removes a deleted server from the list", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 3 })),
      message(Message.ServerDeleted({ id: 3 })),
      model((m) => {
        expect(Option.isNone(m.sheet)).toBe(true);
        expect(Option.getOrThrow(m.servers).map((server) => server.id)).toEqual([4]);
      }),
    );
  });
});

describe("the OAuth of a server", () => {
  it("sends the client fields of Connect, and opens the authorization page of the answer", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 3 })),
      message(Message.ChangedClientId({ text: "client" })),
      message(Message.ChangedClientSecret({ text: "secret" })),
      message(Message.ClickedConnect()),
      StoryCommand.expectExact(
        StartOAuth({ organization: "plants", id: 3, clientId: "client", clientSecret: "secret" }),
      ),
      StoryCommand.resolve(StartOAuth, Message.OAuthStarted({ url: "https://auth.example.com/a" })),
      StoryCommand.expectExact(Redirect({ url: "https://auth.example.com/a" })),
      StoryCommand.resolve(Redirect, Message.CompletedRedirect()),
    );
  });

  it("shows the reason of a refused Connect in the sheet", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedEdit({ id: 3 })),
      message(Message.ClickedConnect()),
      StoryCommand.resolve(StartOAuth, Message.OAuthFailed({ error: "no ui_url" })),
      model((m) => {
        expect(sheet(m)).toMatchObject({ pending: "none", error: "no ui_url" });
      }),
    );
  });

  it("shows the notice of a connect after the list loads, and removes the query", () => {
    story(
      update,
      given({ ...loaded({ oauth: "connected", server: 3, reason: "" }), servers: Option.none() }),
      message(Message.ServersLoaded({ servers: [http, stdio] })),
      StoryCommand.expectExact(
        ShowNotice({ text: "Connected makeadir" }),
        Navigate({ path: "/settings/mcp-servers" }),
      ),
      StoryCommand.resolveAll(
        [ShowNotice, Message.CompletedShowNotice()],
        [Navigate, Message.CompletedNavigate()],
      ),
      model((m) => {
        expect(Option.isNone(m.callback)).toBe(true);
      }),
    );
  });

  it("shows the reason of a failed connect", () => {
    story(
      update,
      given({
        ...loaded({ oauth: "failed", server: 4, reason: "access denied" }),
        servers: Option.none(),
      }),
      message(Message.ServersLoaded({ servers: [http, stdio] })),
      StoryCommand.expectExact(
        ShowNotice({ text: "The connect of search failed: access denied" }),
        Navigate({ path: "/settings/mcp-servers" }),
      ),
      StoryCommand.resolveAll(
        [ShowNotice, Message.CompletedShowNotice()],
        [Navigate, Message.CompletedNavigate()],
      ),
    );
  });

  it("sets a disconnected server to no OAuth", () => {
    const connected: McpServer = { ...http, oauth: "connected" };
    story(
      update,
      given({ ...loaded(), servers: Option.some([connected, stdio]) }),
      message(Message.ClickedEdit({ id: 3 })),
      message(Message.ClickedDisconnect()),
      StoryCommand.resolve(DisconnectOAuth, Message.OAuthDisconnected({ id: 3 })),
      model((m) => {
        expect(Option.getOrThrow(m.servers)[0].oauth).toBe("none");
        expect(sheet(m).pending).toBe("none");
      }),
    );
  });
});
