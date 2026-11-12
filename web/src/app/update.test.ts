import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { fromString } from "foldkit/url";
import { FetchAgents } from "@/agents/commands";
import { Message as AgentsMessage } from "@/agents/message";
import { Message as SwitchesMessage } from "@/settings/switches/message";
import { Message as PanelMessage } from "@/panel/message";
import { Message as TasksMessage } from "@/panel/tasks/message";
import {
  CheckUpgrade,
  FetchCIStops,
  FetchPauses,
  FetchSwitches,
  GoBack,
  LoadApp,
  LoadExternal,
  Navigate,
  ReplaceUrl,
  SettleScroll,
  SubmitLogin,
} from "./commands";
import { Message } from "./message";
import { initialModel, type Model } from "./model";
import { AppRoute, parseRoute } from "./route";
import { update } from "./update";

const urlOf = (path: string) => {
  const parsed = fromString(`http://localhost:6363${path}`);
  if (Option.isNone(parsed)) {
    throw new Error(`Invalid URL: ${path}`);
  }
  return parsed.value;
};

const createModel = (path = "/chat", overrides: Partial<Model> = {}): Model => {
  const route = parseRoute(urlOf(path));
  return {
    ...initialModel(route),
    organization: "owner",
    apps: Option.some([
      {
        slug: "mobius",
        installUrl: "",
        repositories: ["owner/repo"],
      },
    ]),
    organizations: ["owner", "other-org"],
    ...overrides,
  };
};

describe("router update logic", () => {
  it("transitions to Workstreams route on UrlChanged", () => {
    story(
      update,
      given(createModel("/chat")),
      message(Message.UrlChanged({ url: urlOf("/workstreams") })),
      model((m) => {
        expect(m.route._tag).toBe("Workstreams");
        expect(Option.isSome(m.workstreamsProgram)).toBe(true);
      }),
      StoryCommand.expectHas(SettleScroll),
      StoryCommand.resolve(SettleScroll, Message.CompletedSettleScroll()),
    );
  });

  it("redirects unknown path to /chat with ReplaceUrl", () => {
    story(
      update,
      given(createModel("/chat")),
      message(Message.UrlChanged({ url: urlOf("/unknown/path") })),
      model((m) => {
        expect(m.route._tag).toBe("NotFound");
      }),
      StoryCommand.expectHas(ReplaceUrl),
      StoryCommand.resolve(ReplaceUrl, Message.CompletedReplaceUrl()),
    );
  });

  it("navigates on internal UrlRequested", () => {
    const url = urlOf("/workstreams");
    story(
      update,
      given(createModel("/chat")),
      message(
        Message.UrlRequested({
          request: { _tag: "Internal", url },
        }),
      ),
      StoryCommand.expectHas(Navigate),
      StoryCommand.resolve(Navigate, Message.CompletedNavigate()),
    );
  });

  it("loads external URL on external UrlRequested", () => {
    story(
      update,
      given(createModel("/chat")),
      message(
        Message.UrlRequested({
          request: { _tag: "External", href: "https://github.com" },
        }),
      ),
      StoryCommand.expectHas(LoadExternal),
      StoryCommand.resolve(LoadExternal, Message.CompletedLoadExternal()),
    );
  });

  it("goes back to the page before, or to the parent page when there is none", () => {
    story(
      update,
      given(createModel("/workstreams")),
      message(Message.BackRequested({ parent: "/settings" })),
      StoryCommand.expectExact(GoBack({ parent: "/settings" })),
      StoryCommand.resolve(GoBack, Message.CompletedGoBack()),
    );
  });

  it("opens login sheet on ShowLogin", () => {
    story(
      update,
      given(createModel("/chat", { loginShown: false })),
      message(Message.ShowLogin()),
      model((m) => {
        expect(m.loginShown).toBe(true);
      }),
    );
  });

  it("closes login sheet on HideLogin", () => {
    story(
      update,
      given(createModel("/chat", { loginShown: true })),
      message(Message.HideLogin()),
      model((m) => {
        expect(m.loginShown).toBe(false);
      }),
    );
  });

  it("updates login password on PasswordInputChanged", () => {
    story(
      update,
      given(createModel("/chat")),
      message(Message.PasswordInputChanged({ password: "secret123" })),
      model((m) => {
        expect(m.passwordInput).toBe("secret123");
      }),
    );
  });

  it("submits login and on success hides login, clears password, and loads app", () => {
    story(
      update,
      given(createModel("/chat", { loginShown: true, passwordInput: "secret" })),
      message(Message.SubmitLoginRequested()),
      model((m) => {
        expect(m.loginBusy).toBe(true);
      }),
      StoryCommand.expectHas(SubmitLogin),
      StoryCommand.resolve(SubmitLogin, Message.LoginSucceeded()),
      model((m) => {
        expect(m.loginShown).toBe(false);
        expect(m.loginBusy).toBe(false);
        expect(m.passwordInput).toBe("");
      }),
      StoryCommand.expectHas(LoadApp),
      StoryCommand.resolve(
        LoadApp,
        Message.AppsAndOrganizationsLoaded({ apps: [], organizations: [] }),
      ),
      StoryCommand.resolve(
        CheckUpgrade,
        Message.UpgradeInfoLoaded({ version: null, drain: null, failure: null }),
      ),
      StoryCommand.resolve(FetchPauses, Message.PausesChanged({ pauses: [] })),
      StoryCommand.resolve(FetchCIStops, Message.CIStopsChanged({ stops: [] })),
      StoryCommand.resolve(
        FetchSwitches,
        Message.SwitchesChanged({
          switches: { start: true, merge: true, repairs: true, held: [] },
        }),
      ),
    );
  });

  it("shows error on login failure", () => {
    story(
      update,
      given(createModel("/chat", { loginShown: true, passwordInput: "wrong" })),
      message(Message.SubmitLoginRequested()),
      StoryCommand.resolve(SubmitLogin, Message.LoginFailed({ error: "Invalid password" })),
      model((m) => {
        expect(m.loginBusy).toBe(false);
        expect(m.loginError).toBe("Invalid password");
      }),
    );
  });
});

describe("organization switch update logic", () => {
  it("switches current organization and opens its Coordinator chat", () => {
    const { model: next, commands } = update(
      createModel("/chat", { organization: "owner" }),
      Message.SelectOrganization({ organization: "other-org" }),
    );
    expect(next.organization).toBe("other-org");
    expect(Option.map(next.chat, (chat) => chat.organization)).toEqual(Option.some("other-org"));
    expect(commands).toHaveLength(2);
  });

  it("navigates to /workstreams when switching org away from current workstream", () => {
    const route = AppRoute.WorkstreamChat({ owner: "owner", name: "repo", number: 1 });
    story(
      update,
      given(createModel("/workstreams/owner/repo/1", { route, organization: "owner" })),
      message(Message.SelectOrganization({ organization: "other-org" })),
      model((m) => {
        expect(m.organization).toBe("other-org");
      }),
      StoryCommand.expectHas(Navigate),
      StoryCommand.resolve(Navigate, Message.CompletedNavigate()),
    );
  });

  it("opens the overview page on the Checkup route", () => {
    const { model: next } = update(
      createModel("/chat"),
      Message.UrlChanged({ url: urlOf("/settings/checkup") }),
    );
    expect(next.route._tag).toBe("Checkup");
    expect(Option.map(next.checkupProgram, (checkup) => checkup.page)).toEqual(
      Option.some("overview"),
    );
  });

  it("loads the agent list when the route changes to Agents", () => {
    story(
      update,
      given(createModel("/settings")),
      message(Message.UrlChanged({ url: urlOf("/agents") })),
      model((m) => {
        expect(m.route._tag).toBe("Agents");
        expect(Option.map(m.agentsProgram, (agents) => agents.request)).toEqual(Option.some(1));
      }),
      StoryCommand.expectHas(FetchAgents({ request: 1 })),
      StoryCommand.resolve(
        FetchAgents,
        AgentsMessage.AgentsLoaded({ request: 1, agents: { groups: [] } }),
      ),
      StoryCommand.resolve(SettleScroll, Message.CompletedSettleScroll()),
    );
  });

  it("keeps the Usage Submodel when the URL repeats its search", () => {
    const opened = update(createModel("/chat"), Message.UrlChanged({ url: urlOf("/usage") }));
    const marked: Model = {
      ...opened.model,
      usageProgram: Option.map(opened.model.usageProgram, (usage) => ({
        ...usage,
        groupSheetOpen: true,
      })),
    };
    const changed = update(marked, Message.UrlChanged({ url: urlOf("/usage") }));
    expect(changed.commands).toHaveLength(1);
    expect(Option.map(changed.model.usageProgram, (usage) => usage.groupSheetOpen)).toEqual(
      Option.some(true),
    );
  });

  it("closes the Tasks sheet after a task reaches the draft", () => {
    story(
      update,
      given(createModel("/workstreams/owner/repo/1", { mobileTasksOpen: true })),
      message(
        Message.GotPanelMessage({
          message: PanelMessage.GotTasksMessage({ message: TasksMessage.AttachedTask() }),
        }),
      ),
      model((m) => {
        expect(m.mobileTasksOpen).toBe(false);
      }),
    );
  });

  it("gives the switch errors to the Settings overview", () => {
    const { model: next } = update(
      createModel("/settings/switches", { switchErrors: { start: "The switch did not change." } }),
      Message.UrlChanged({ url: urlOf("/settings") }),
    );
    expect(Option.map(next.settingsProgram, (settings) => settings.errors.start)).toEqual(
      Option.some("The switch did not change."),
    );
  });

  it("keeps a switch error until the server gives another value", () => {
    const switches = { start: true, merge: true, repairs: true, held: [] };
    const failed = update(
      createModel("/settings/switches", { switches: Option.some(switches) }),
      Message.ToggleSwitchFailed({ kind: "start", on: false, error: "The switch did not change." }),
    ).model;
    expect(failed.switchErrors).toEqual({ start: "The switch did not change." });

    const same = update(failed, Message.SwitchesChanged({ switches })).model;
    expect(same.switchErrors).toEqual({ start: "The switch did not change." });

    const other = update(
      failed,
      Message.SwitchesChanged({ switches: { ...switches, start: false } }),
    ).model;
    expect(other.switchErrors).toEqual({});
  });

  it("clears the error of a switch when the Owner changes it again", () => {
    const switches = { start: true, merge: true, repairs: true, held: [] };
    const next = update(
      createModel("/settings/switches", {
        switches: Option.some(switches),
        switchErrors: { start: "The switch did not change." },
        switchFailures: { start: false },
      }),
      Message.ToggleSwitchRequested({ kind: "start", on: false }),
    ).model;
    expect(next.switchErrors).toEqual({});
  });

  it("opens the MCP servers of the organization that the page loads after it starts", () => {
    const opened = update(
      createModel("/chat", { organization: "", organizations: [], apps: Option.none() }),
      Message.UrlChanged({ url: urlOf("/settings/mcp-servers") }),
    ).model;
    expect(Option.map(opened.mcpServersProgram, (servers) => servers.organization)).toEqual(
      Option.some(""),
    );

    const loaded = update(
      opened,
      Message.AppsAndOrganizationsLoaded({ apps: [], organizations: ["owner"] }),
    );
    expect(Option.map(loaded.model.mcpServersProgram, (servers) => servers.organization)).toEqual(
      Option.some("owner"),
    );
  });

  it("hides the mark of a Workstream whose issue the Owner answered", () => {
    const row = {
      number: 45,
      pullRequest: null,
      pullRequestUrl: null,
      question: true,
      reason: "",
      repository: "owner/shop",
      stopped: false,
      title: "",
      url: "",
      workstream: 12,
    };
    const next = update(
      createModel("/workstreams", { needsHuman: [row] }),
      Message.UrlChanged({ url: urlOf("/workstreams") }),
    ).model;
    expect(Option.map(next.workstreamsProgram, (list) => list.needsHuman)).toEqual(
      Option.some([row]),
    );

    const answered = update(
      next,
      Message.InboxStoreChanged({
        items: [],
        answered: [
          { id: 301, repository: "owner/shop", issue: 45, time: 0, item: true, mark: true },
        ],
      }),
    ).model;
    expect(Option.map(answered.workstreamsProgram, (list) => list.needsHuman)).toEqual(
      Option.some([]),
    );
  });

  it("drops the Submodel of a screen that the Owner leaves", () => {
    const tasks = update(createModel("/chat"), Message.UrlChanged({ url: urlOf("/tasks") })).model;
    expect(Option.isSome(tasks.tasksProgram)).toBe(true);

    const chat = update(
      tasks,
      Message.UrlChanged({ url: urlOf("/workstreams/owner/shop/12") }),
    ).model;
    expect(Option.isNone(chat.tasksProgram)).toBe(true);
    expect(Option.isSome(chat.chat)).toBe(true);
  });

  it("shows the error of a failed load only while the browser is online", () => {
    const failed = Message.LoadAppFailed({ error: "apps failed" });
    expect(update(createModel("/chat", { online: true }), failed).model.appError).toEqual(
      Option.some("apps failed"),
    );
    expect(update(createModel("/chat", { online: false }), failed).model.appError).toEqual(
      Option.none(),
    );
  });

  it("shows no connecting badge until the start limit ends", () => {
    const starting = createModel("/chat");
    expect(starting.link).toBe("starting");
    expect(update(starting, Message.LiveConnecting()).model.link).toBe("starting");
    expect(update(starting, Message.StartLimitReached()).model.link).toBe("connecting");

    const connected = update(starting, Message.LiveOpened()).model;
    expect(connected.link).toBe("connected");
    expect(update(connected, Message.StartLimitReached()).model.link).toBe("connected");
    expect(update(connected, Message.LiveConnecting()).model.link).toBe("connecting");
  });

  it("goes back to the parent page of a settings screen", () => {
    story(
      update,
      given(createModel("/settings/switches")),
      message(Message.GotSwitchesMessage({ message: SwitchesMessage.ClickedBack() })),
      StoryCommand.expectExact(GoBack({ parent: "/settings" })),
      StoryCommand.resolve(GoBack, Message.CompletedGoBack()),
    );
  });

  it("opens a memory file from the fifth segment of the path", () => {
    const { model: next } = update(
      createModel("/chat"),
      Message.UrlChanged({ url: urlOf("/settings/memory/owner/shop/shared") }),
    );
    expect(next.route).toEqual(
      AppRoute.MemoryFile({ owner: "owner", name: "shop", file: "shared" }),
    );
  });

  it("stores the width of the screen", () => {
    story(
      update,
      given(createModel("/chat")),
      message(Message.WideChanged({ wide: false })),
      model((m) => {
        expect(m.wide).toBe(false);
      }),
    );
  });

  it("does not navigate away when staying in same org workstream", () => {
    const route = AppRoute.WorkstreamChat({ owner: "owner", name: "repo", number: 1 });
    story(
      update,
      given(createModel("/workstreams/owner/repo/1", { route, organization: "owner" })),
      message(Message.SelectOrganization({ organization: "owner" })),
      model((m) => {
        expect(m.organization).toBe("owner");
      }),
    );
  });
});
