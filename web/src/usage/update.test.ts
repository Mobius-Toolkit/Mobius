import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import {
  ChangeSearch,
  FetchQuality,
  FetchSkills,
  FetchTimes,
  FetchUsage,
  RequestLogin,
} from "./commands";
import { Message } from "./message";
import { initialModel, rangeOf, type Model, type Search } from "./model";
import type { SkillUsage } from "./schemas";
import { update } from "./update";

const now = Date.UTC(2026, 9, 15, 12);

const start = (search: Search = {}): Model => initialModel({ search, now, timeZone: "UTC" });

const searchAfter = (m: Model, ...messages: Message[]) =>
  messages.reduce((current, next) => update(current, next).model, m).search;

const names = (tab: "cost" | "tokens" | "times" | "skills" | "quality") =>
  update(start(), Message.SelectedTab({ tab })).commands?.map((command) => command.name);

const open = (menu: "period" | "chart") => update(start(), Message.ToggledMenu({ menu })).model;

const skills: SkillUsage = {
  options: { harnesses: ["claude-code"], repositories: [], roles: [], sources: ["mobius"] },
  rows: [],
  totals: { sessions: 0, skills: 0, uses: 0 },
};

describe("the tabs", () => {
  it("writes the tab in the search, and the tab Cost writes no tab", () => {
    story(
      update,
      given(start({ group: ["harness"] })),
      message(Message.SelectedTab({ tab: "tokens" })),
      StoryCommand.expectExact(
        ChangeSearch({ search: { group: ["harness"], tab: "tokens" } }),
        FetchUsage({
          request: 1,
          search: { group: ["harness"], tab: "tokens" },
          range: rangeOf(start()),
          timeZone: "UTC",
        }),
      ),
      StoryCommand.resolve(ChangeSearch, Message.CompletedChangeSearch()),
      StoryCommand.resolve(FetchUsage, Message.LoadFailed({ request: 1, error: "nope" })),
      message(Message.SelectedTab({ tab: "cost" })),
      model((m) => {
        expect(m.search).toEqual({ group: ["harness"], tab: undefined });
      }),
      StoryCommand.resolve(ChangeSearch, Message.CompletedChangeSearch()),
      StoryCommand.resolve(FetchUsage, Message.LoadFailed({ request: 2, error: "nope" })),
    );
  });

  it("loads the data of the new tab with the command of the tab", () => {
    expect(names("cost")).toEqual([ChangeSearch.name, FetchUsage.name]);
    expect(names("tokens")).toEqual([ChangeSearch.name, FetchUsage.name]);
    expect(names("times")).toEqual([ChangeSearch.name, FetchTimes.name]);
    expect(names("skills")).toEqual([ChangeSearch.name, FetchSkills.name]);
    expect(names("quality")).toEqual([ChangeSearch.name, FetchQuality.name]);
  });

  it("drops the answer of an old load, and keeps the answer of the last load", () => {
    story(
      update,
      given(start({ tab: "skills" })),
      message(Message.ToggledFilter({ key: "harness", option: "claude-code" })),
      StoryCommand.resolve(ChangeSearch, Message.CompletedChangeSearch()),
      StoryCommand.resolve(FetchSkills, Message.LoadedSkills({ request: 0, skills })),
      model((m) => {
        expect(Option.isNone(m.skills)).toBe(true);
      }),
      message(Message.LoadFailed({ request: 0, error: "old" })),
      model((m) => {
        expect(m.error).toBe("");
      }),
      message(Message.LoadedSkills({ request: 1, skills })),
      model((m) => {
        expect(Option.isSome(m.skills)).toBe(true);
      }),
    );
  });

  it("shows the reason of a failed load until the next load ends, and asks for the login on a 401", () => {
    story(
      update,
      given(start({ tab: "skills" })),
      message(Message.LoadFailed({ request: 0, error: "boom" })),
      model((m) => {
        expect(m.error).toBe("boom");
      }),
      message(Message.LoadedSkills({ request: 0, skills })),
      model((m) => {
        expect(m.error).toBe("");
      }),
      message(Message.LoadLoginRequired({ request: 0 })),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

describe("the period", () => {
  it("writes the last 30 days as the first range of the custom period", () => {
    const search = searchAfter(start(), Message.SelectedPeriod({ period: "custom" }));
    expect(search).toMatchObject({ period: "custom", from: "2026-09-16", to: "2026-10-15" });
  });

  it("removes the days of a custom period when the Owner picks another period", () => {
    const custom = start({ period: "custom", from: "2026-09-01", to: "2026-09-10" });
    expect(searchAfter(custom, Message.SelectedPeriod({ period: "month" }))).toEqual({
      period: "month",
      from: undefined,
      to: undefined,
    });
  });

  it("changes one day of the custom period, and ignores an empty day", () => {
    const custom = start({ period: "custom", from: "2026-09-01", to: "2026-09-10" });
    expect(searchAfter(custom, Message.ChangedFrom({ value: "2026-09-03" }))).toMatchObject({
      from: "2026-09-03",
      to: "2026-09-10",
    });
    expect(searchAfter(custom, Message.ChangedTo({ value: "2026-09-12" }))).toMatchObject({
      from: "2026-09-01",
      to: "2026-09-12",
    });
    expect(update(custom, Message.ChangedFrom({ value: "" })).commands).toBeUndefined();
  });
});

describe("the filters", () => {
  it("adds an option to the filter of a field, and removes the field when no option is left", () => {
    const first = Message.ToggledFilter({ key: "role", option: "lead" });
    const second = Message.ToggledFilter({ key: "role", option: "reviewer" });
    expect(searchAfter(start(), first).role).toEqual(["lead"]);
    expect(searchAfter(start(), first, second).role).toEqual(["lead", "reviewer"]);
    expect(searchAfter(start(), first, second, first).role).toEqual(["reviewer"]);
    expect(searchAfter(start(), first, first).role).toBeUndefined();
  });

  it("keeps the menu of a filter open while the Owner picks options", () => {
    const opened = update(start(), Message.ToggledMenu({ menu: "role" })).model;
    const picked = update(opened, Message.ToggledFilter({ key: "role", option: "lead" })).model;
    expect(picked.openMenu).toEqual(Option.some("role"));
  });

  it("keeps the other filters", () => {
    const search = searchAfter(
      start({ harness: ["dsh"] }),
      Message.ToggledFilter({ key: "repository", option: "plants/garden" }),
    );
    expect(search).toMatchObject({ harness: ["dsh"], repository: ["plants/garden"] });
  });
});

describe("the groups", () => {
  it("writes the fields in the order of the tab, not in the order of the choice", () => {
    const search = searchAfter(
      start(),
      Message.ToggledGroup({ key: "role" }),
      Message.ToggledGroup({ key: "harness" }),
    );
    expect(search.group).toEqual(["harness", "role"]);
  });

  it("removes the field of a group, and the group when it has no field", () => {
    const harnessAndRole = start({ group: ["harness", "role"] });
    expect(searchAfter(harnessAndRole, Message.ToggledGroup({ key: "harness" })).group).toEqual([
      "role",
    ]);
    expect(
      searchAfter(
        harnessAndRole,
        Message.ToggledGroup({ key: "harness" }),
        Message.ToggledGroup({ key: "role" }),
      ).group,
    ).toBeUndefined();
  });

  it("offers on the tab Skills only the fields of the tab, and drops the other fields", () => {
    const skillsTab = start({ tab: "skills", group: ["model", "role"] });
    expect(searchAfter(skillsTab, Message.ToggledGroup({ key: "source" })).group).toEqual([
      "role",
      "source",
    ]);
  });

  it("opens and closes the sheet of the groups on a phone", () => {
    const opened = update(start(), Message.OpenedGroupSheet()).model;
    expect(opened.groupSheetOpen).toBe(true);
    expect(update(opened, Message.ClosedGroupSheet()).model.groupSheetOpen).toBe(false);
  });
});

describe("the menus", () => {
  it("opens one menu at a time, and a second click on its button closes it", () => {
    const period = update(start(), Message.ToggledMenu({ menu: "period" })).model;
    expect(period.openMenu).toEqual(Option.some("period"));
    const role = update(period, Message.ToggledMenu({ menu: "role" })).model;
    expect(role.openMenu).toEqual(Option.some("role"));
    expect(update(role, Message.ToggledMenu({ menu: "role" })).model.openMenu).toEqual(
      Option.none(),
    );
  });

  it("closes the menu of a period, a stat or a value when the Owner picks an item", () => {
    expect(
      update(open("period"), Message.SelectedPeriod({ period: "30d" })).model.openMenu,
    ).toEqual(Option.none());
    const stat = update(open("chart"), Message.SelectedStat({ stat: "step:check" })).model;
    expect(stat.openMenu).toEqual(Option.none());
    expect(stat.search.stat).toBe("step:check");
    const value = update(open("chart"), Message.SelectedValue({ value: "fixRounds" })).model;
    expect(value.openMenu).toEqual(Option.none());
    expect(value.search.value).toBe("fixRounds");
  });

  it("closes the menu with the Escape key first, and then the sheet", () => {
    const both = {
      ...start(),
      openMenu: Option.some("group" as const),
      groupSheetOpen: true,
    };
    const menuClosed = update(both, Message.PressedEscape()).model;
    expect(menuClosed.openMenu).toEqual(Option.none());
    expect(menuClosed.groupSheetOpen).toBe(true);
    expect(update(menuClosed, Message.PressedEscape()).model.groupSheetOpen).toBe(false);
  });
});

describe("the hover of a chart", () => {
  it("keeps the position of the pointer until the mouse leaves, and clears it with a new search", () => {
    const hovered = update(start(), Message.HoveredChart({ x: 10, y: 20 })).model;
    expect(hovered.hover).toEqual(Option.some({ x: 10, y: 20 }));
    expect(update(hovered, Message.LeftChart()).model.hover).toEqual(Option.none());
    expect(update(hovered, Message.SelectedTab({ tab: "quality" })).model.hover).toEqual(
      Option.none(),
    );
  });
});

describe("the search from the route", () => {
  it("keeps the model when the route search matches the current search", () => {
    const current = start({ tab: "tokens" });
    const result = update(current, Message.ReceivedSearch({ search: { tab: "tokens" } }));
    expect(result.model).toBe(current);
    expect(result.commands).toBeUndefined();
  });

  it("loads the new data when the route search differs", () => {
    story(
      update,
      given(start({ tab: "tokens" })),
      message(Message.ReceivedSearch({ search: {} })),
      model((m) => {
        expect(m.search).toEqual({});
        expect(m.request).toBe(1);
      }),
      StoryCommand.expectExact(
        FetchUsage({
          request: 1,
          search: {},
          range: rangeOf(start()),
          timeZone: "UTC",
        }),
      ),
      StoryCommand.resolve(FetchUsage, Message.LoadFailed({ request: 1, error: "nope" })),
    );
  });
});
