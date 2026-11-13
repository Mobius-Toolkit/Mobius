import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { CreateWorkstream, FetchCheckup, SaveIssueRepository } from "./commands";
import { Message } from "./message";
import { initialModel, type Model } from "./model";
import { maintenanceWorkstreamRequest, skillsWorkstreamRequest } from "./requests";
import type { Checkup } from "./schemas";
import { init, update } from "./update";

const checkup = (overrides: Partial<Checkup> = {}): Checkup => ({
  issueRepository: "plants/garden",
  issueRepositoryError: "",
  issueRepositorySource: "choice",
  labelFix: "none",
  maintenanceWorkstream: 0,
  permissions: [],
  permissionsError: "",
  repositories: [
    { repository: "plants/garden", labels: [] },
    { repository: "plants/shed", labels: [] },
  ],
  skillRepository: "plants/mobius-skills",
  skillsWorkstream: 0,
  skillsWorkstreamRepository: "",
  ...overrides,
});

const withNoIssueRepository = () =>
  checkup({
    issueRepository: "",
    issueRepositoryError: "Mobius needs the issue repository of plants.",
    issueRepositorySource: "none",
  });

const loaded = (found: Checkup = checkup(), overrides: Partial<Model> = {}): Model => ({
  ...initialModel("plants"),
  checkup: Option.some(found),
  ...overrides,
});

const picker = (overrides: Partial<{ selected: string; error: string; saving: boolean }> = {}) =>
  Option.some({ selected: "plants/garden", error: "", saving: false, ...overrides });

describe("the load of the checkup", () => {
  it("reads the checkup of its organization when it opens", () => {
    const start = init("plants");
    expect(start.model).toEqual(initialModel("plants"));
    story(
      update,
      given(start.model),
      message(Message.CheckupLoaded({ checkup: checkup() })),
      model((m) => {
        expect(m.checkup).toEqual(Option.some(checkup()));
      }),
    );
    expect(start.commands?.map(({ name, args }) => ({ name, args }))).toEqual([
      { name: "FetchCheckup", args: { organization: "plants" } },
    ]);
  });

  it("keeps the error of a failed read", () => {
    story(
      update,
      given(initialModel("plants")),
      message(Message.CheckupFailed({ error: "The server failed." })),
      model((m) => {
        expect(m.checkupError).toBe("The server failed.");
      }),
    );
  });
});

describe("the picker of the issue repository", () => {
  it("opens with the issue repository selected", () => {
    story(
      update,
      given(loaded()),
      message(Message.OpenedPicker()),
      model((m) => {
        expect(m.picker).toEqual(picker());
      }),
      message(Message.ClosedPicker()),
      model((m) => {
        expect(Option.isNone(m.picker)).toBe(true);
      }),
    );
  });

  it("opens with nothing selected when the organization has no issue repository", () => {
    story(
      update,
      given(loaded(withNoIssueRepository())),
      message(Message.OpenedPicker()),
      model((m) => {
        expect(m.picker).toEqual(picker({ selected: "" }));
      }),
    );
  });

  it("does not open when the config file sets the issue repository", () => {
    story(
      update,
      given(loaded(checkup({ issueRepositorySource: "config" }))),
      message(Message.OpenedPicker()),
      model((m) => {
        expect(Option.isNone(m.picker)).toBe(true);
      }),
    );
  });

  it("selects a repository and clears the error", () => {
    story(
      update,
      given(loaded(checkup(), { picker: picker({ error: "The App does not see it." }) })),
      message(Message.SelectedRepository({ repository: "plants/shed" })),
      model((m) => {
        expect(m.picker).toEqual(picker({ selected: "plants/shed" }));
      }),
    );
  });

  it("saves the selected repository, closes, and reads the checkup again", () => {
    story(
      update,
      given(loaded(checkup(), { picker: picker({ selected: "plants/shed" }) })),
      message(Message.ClickedSave()),
      model((m) => {
        expect(m.picker).toEqual(picker({ selected: "plants/shed", saving: true }));
      }),
      StoryCommand.expectExact(
        SaveIssueRepository({ organization: "plants", repository: "plants/shed" }),
      ),
      StoryCommand.resolve(SaveIssueRepository, Message.Saved()),
      model((m) => {
        expect(Option.isNone(m.picker)).toBe(true);
      }),
      StoryCommand.expectExact(FetchCheckup({ organization: "plants" })),
      StoryCommand.resolve(
        FetchCheckup,
        Message.CheckupLoaded({ checkup: checkup({ issueRepository: "plants/shed" }) }),
      ),
      model((m) => {
        expect(Option.map(m.checkup, (found) => found.issueRepository)).toEqual(
          Option.some("plants/shed"),
        );
      }),
    );
  });

  it("keeps the picker open with the refusal", () => {
    story(
      update,
      given(loaded(checkup(), { picker: picker({ selected: "plants/shed" }) })),
      message(Message.ClickedSave()),
      StoryCommand.resolve(
        SaveIssueRepository,
        Message.SaveFailed({ error: "The Mobius App does not see plants/shed." }),
      ),
      model((m) => {
        expect(m.picker).toEqual(
          picker({ selected: "plants/shed", error: "The Mobius App does not see plants/shed." }),
        );
      }),
      StoryCommand.expectNone(),
    );
  });

  it("saves nothing with no selection or while it saves", () => {
    story(
      update,
      given(loaded(withNoIssueRepository(), { picker: picker({ selected: "" }) })),
      message(Message.ClickedSave()),
      StoryCommand.expectNone(),
    );
    story(
      update,
      given(loaded(checkup(), { picker: picker({ saving: true }) })),
      message(Message.ClickedSave()),
      StoryCommand.expectNone(),
    );
  });
});

describe("the Create buttons", () => {
  it("sends the request of the maintenance Workstream", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedCreate({ kind: "maintenance" })),
      model((m) => {
        expect(m.creating).toEqual(Option.some("maintenance"));
      }),
      StoryCommand.expectExact(
        CreateWorkstream({
          organization: "plants",
          request: maintenanceWorkstreamRequest("plants/garden"),
        }),
      ),
      StoryCommand.resolve(
        CreateWorkstream,
        Message.WorkstreamRequested({ organization: "plants" }),
      ),
      StoryCommand.expectNone(),
    );
  });

  it("sends the request of the Skills Workstream", () => {
    story(
      update,
      given(loaded(checkup({ maintenanceWorkstream: 9 }))),
      message(Message.ClickedCreate({ kind: "skills" })),
      StoryCommand.expectExact(
        CreateWorkstream({
          organization: "plants",
          request: skillsWorkstreamRequest("plants/garden", "plants/mobius-skills"),
        }),
      ),
      StoryCommand.resolve(
        CreateWorkstream,
        Message.WorkstreamRequested({ organization: "plants" }),
      ),
    );
  });

  it("shows the error of a message that the device cannot store, and stops the pending state", () => {
    story(
      update,
      given(loaded()),
      message(Message.ClickedCreate({ kind: "skills" })),
      StoryCommand.resolve(CreateWorkstream, Message.CreateFailed({ error: "QuotaExceededError" })),
      model((m) => {
        expect(m.createError).toBe("QuotaExceededError");
        expect(Option.isNone(m.creating)).toBe(true);
      }),
    );
  });

  it("sends nothing when the organization has no issue repository", () => {
    story(
      update,
      given(loaded(withNoIssueRepository())),
      message(Message.ClickedCreate({ kind: "maintenance" })),
      StoryCommand.expectNone(),
      model((m) => {
        expect(Option.isNone(m.creating)).toBe(true);
      }),
    );
  });

  it("sends nothing before the checkup is loaded", () => {
    story(
      update,
      given(initialModel("plants")),
      message(Message.ClickedCreate({ kind: "maintenance" })),
      StoryCommand.expectNone(),
    );
  });
});
