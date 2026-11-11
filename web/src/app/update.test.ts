import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { fromString } from "foldkit/url";
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

  it("goes back in browser history when historyDepth > 0", () => {
    story(
      update,
      given(createModel("/workstreams", { historyDepth: 2 })),
      message(Message.BackRequested({ parent: "/workstreams" })),
      StoryCommand.expectHas(GoBack),
      StoryCommand.resolve(GoBack, Message.CompletedGoBack()),
    );
  });

  it("replaces URL with parent when historyDepth is 0", () => {
    story(
      update,
      given(createModel("/workstreams", { historyDepth: 0 })),
      message(Message.BackRequested({ parent: "/workstreams" })),
      StoryCommand.expectHas(ReplaceUrl),
      StoryCommand.resolve(ReplaceUrl, Message.CompletedReplaceUrl()),
    );
  });
});

describe("login update logic", () => {
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
        Message.UpgradeInfoLoaded({ version: "", drain: null, failure: "" }),
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
  it("switches current organization", () => {
    story(
      update,
      given(createModel("/chat", { organization: "owner" })),
      message(Message.SelectOrganization({ organization: "other-org" })),
      model((m) => {
        expect(m.organization).toBe("other-org");
      }),
    );
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
