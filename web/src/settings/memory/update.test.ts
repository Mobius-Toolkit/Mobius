import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { FetchOlder, FetchVersions, RequestLogin, RevertMemory, SaveMemory } from "./commands";
import { Message } from "./message";
import { initialModel, shown, type Editing, type Model } from "./model";
import type { MemoryVersion } from "../schemas";
import { update } from "./update";

const version = (overrides: Partial<MemoryVersion> = {}): MemoryVersion => ({
  author: "owner",
  before: "",
  id: 2,
  reason: "",
  revert_problem: "",
  revertible: true,
  text: "Water the roses.\n",
  time: "2026-09-28T09:00:00Z",
  ...overrides,
});

const place = { owner: "plants", name: "garden", file: "shared" };

const fileModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel({ page: "file", ...place }),
  versions: Option.some([shown(version())]),
  ...overrides,
});

const editingModel = (overrides: Partial<Editing> = {}) =>
  fileModel({
    editing: Option.some({
      draft: "Water the roses.\n",
      base: 2,
      saving: false,
      error: "",
      discarding: false,
      ...overrides,
    }),
  });

const editing = (m: Model) => Option.getOrThrow(m.editing);

describe("the versions", () => {
  it("shows the changed parts of each version, and none for a change of the final newline", () => {
    story(
      update,
      given(initialModel({ page: "file", ...place })),
      message(
        Message.VersionsLoaded({
          versions: [
            version({
              id: 3,
              before: "Water the roses.\n",
              text: "Water the roses.\nCut the grass.\n",
            }),
            version({ id: 2, before: "Water the roses.\n", text: "Water the roses." }),
          ],
          older: true,
        }),
      ),
      model((m) => {
        const versions = Option.getOrThrow(m.versions);
        expect(versions.map((item) => item.id)).toEqual([3, 2]);
        expect(versions[0].hunks).toHaveLength(1);
        expect(versions[0].hunks[0]).toContain("==Cut the grass.==");
        expect(versions[1].hunks).toEqual([]);
        expect(m.older).toBe(true);
      }),
    );
  });

  it("adds the older versions at the end, and keeps the error of a failed page", () => {
    story(
      update,
      given(fileModel({ older: true })),
      message(Message.ClickedShowOlder()),
      model((m) => {
        expect(m.loadingOlder).toBe(true);
      }),
      StoryCommand.expectExact(FetchOlder({ ...place, before: 2 })),
      StoryCommand.resolve(FetchOlder, Message.OlderFailed({ error: "down" })),
      model((m) => {
        expect(m.loadingOlder).toBe(false);
        expect(m.error).toBe("down");
      }),
      message(Message.ClickedShowOlder()),
      StoryCommand.resolve(
        FetchOlder,
        Message.OlderLoaded({ versions: [version({ id: 1 })], older: false }),
      ),
      model((m) => {
        expect(Option.getOrThrow(m.versions).map((item) => item.id)).toEqual([2, 1]);
        expect(m.older).toBe(false);
        expect(m.loadingOlder).toBe(false);
      }),
    );
  });

  it("asks for the login when the older versions get a 401, and stops the pending state", () => {
    story(
      update,
      given(fileModel({ older: true })),
      message(Message.ClickedShowOlder()),
      StoryCommand.resolve(FetchOlder, Message.LoginRequired()),
      model((m) => {
        expect(m.loadingOlder).toBe(false);
      }),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

describe("the edit dialog", () => {
  it("starts with the current text and the id of the newest version", () => {
    story(
      update,
      given(fileModel()),
      message(Message.ClickedEdit()),
      model((m) => {
        expect(editing(m)).toEqual({
          draft: "Water the roses.\n",
          base: 2,
          saving: false,
          error: "",
          discarding: false,
        });
      }),
    );
  });

  it("starts with an empty draft and base 0 for a file with no version", () => {
    story(
      update,
      given(fileModel({ versions: Option.some([]) })),
      message(Message.ClickedEdit()),
      model((m) => {
        expect(editing(m).draft).toBe("");
        expect(editing(m).base).toBe(0);
      }),
    );
  });

  it("closes at once when the draft has no change", () => {
    story(
      update,
      given(editingModel()),
      message(Message.RequestedClose()),
      model((m) => {
        expect(Option.isNone(m.editing)).toBe(true);
      }),
    );
  });

  it("asks to discard a changed draft, and keeps the draft when the Owner keeps editing", () => {
    story(
      update,
      given(editingModel()),
      message(Message.ChangedDraft({ text: "Water the roses at noon.\n" })),
      message(Message.RequestedClose()),
      model((m) => {
        expect(editing(m).discarding).toBe(true);
        expect(editing(m).draft).toBe("Water the roses at noon.\n");
      }),
      message(Message.PressedEscape()),
      model((m) => {
        expect(editing(m).discarding).toBe(false);
        expect(editing(m).draft).toBe("Water the roses at noon.\n");
      }),
      message(Message.RequestedClose()),
      message(Message.ClickedKeepEditing()),
      model((m) => {
        expect(editing(m).discarding).toBe(false);
      }),
      message(Message.RequestedClose()),
      message(Message.ClickedDiscard()),
      model((m) => {
        expect(Option.isNone(m.editing)).toBe(true);
      }),
    );
  });

  it("closes a clean draft with the Escape key, and asks to discard a changed draft", () => {
    story(
      update,
      given(editingModel()),
      message(Message.PressedEscape()),
      model((m) => {
        expect(Option.isNone(m.editing)).toBe(true);
      }),
    );
    story(
      update,
      given(editingModel({ draft: "Other text.\n" })),
      message(Message.PressedEscape()),
      model((m) => {
        expect(editing(m).discarding).toBe(true);
      }),
    );
  });

  it("does not close while a save runs, and does not close under the dialog of the discard", () => {
    story(
      update,
      given(editingModel({ saving: true })),
      message(Message.RequestedClose()),
      message(Message.PressedEscape()),
      model((m) => {
        expect(editing(m).saving).toBe(true);
      }),
    );
    story(
      update,
      given(editingModel({ draft: "Other text.\n", discarding: true })),
      message(Message.RequestedClose()),
      model((m) => {
        expect(editing(m).discarding).toBe(true);
      }),
    );
  });
});

describe("the save", () => {
  it("saves the draft against the base version, closes the dialog and reads the versions again", () => {
    story(
      update,
      given(editingModel({ draft: "Water the roses at noon.\n" })),
      message(Message.ClickedSave()),
      model((m) => {
        expect(editing(m).saving).toBe(true);
      }),
      StoryCommand.expectExact(
        SaveMemory({ ...place, text: "Water the roses at noon.\n", base: 2 }),
      ),
      StoryCommand.resolve(SaveMemory, Message.Saved()),
      model((m) => {
        expect(Option.isNone(m.editing)).toBe(true);
      }),
      StoryCommand.expectExact(FetchVersions(place)),
      StoryCommand.resolve(
        FetchVersions,
        Message.VersionsLoaded({ versions: [version({ id: 3 })], older: false }),
      ),
    );
  });

  it("keeps the dialog, the draft and the error of a refused save, and clears nothing of the draft", () => {
    story(
      update,
      given(editingModel({ draft: "Too long.\n" })),
      message(Message.ClickedSave()),
      StoryCommand.resolve(SaveMemory, Message.SaveFailed({ error: "the maximum is 200" })),
      model((m) => {
        expect(editing(m)).toMatchObject({
          draft: "Too long.\n",
          error: "the maximum is 200",
          saving: false,
        });
        expect(m.error).toBe("");
      }),
    );
  });

  it("asks for the login when the save gets a 401, and keeps the dialog", () => {
    story(
      update,
      given(editingModel({ draft: "Other text.\n" })),
      message(Message.ClickedSave()),
      StoryCommand.resolve(SaveMemory, Message.SaveLoginRequired()),
      model((m) => {
        expect(editing(m).saving).toBe(false);
        expect(editing(m).draft).toBe("Other text.\n");
      }),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

describe("the revert", () => {
  it("reverts one version, and reads the versions again", () => {
    story(
      update,
      given(fileModel({ error: "An old error." })),
      message(Message.ClickedRevert({ id: 2 })),
      model((m) => {
        expect(m.reverting).toEqual(Option.some(2));
      }),
      StoryCommand.expectExact(RevertMemory({ ...place, id: 2 })),
      StoryCommand.resolve(RevertMemory, Message.Reverted()),
      model((m) => {
        expect(Option.isNone(m.reverting)).toBe(true);
        expect(m.error).toBe("");
      }),
      StoryCommand.expectExact(FetchVersions(place)),
      StoryCommand.resolve(
        FetchVersions,
        Message.VersionsLoaded({ versions: [version({ id: 3 })], older: false }),
      ),
    );
  });

  it("shows the reason of a refused revert", () => {
    story(
      update,
      given(fileModel()),
      message(Message.ClickedRevert({ id: 2 })),
      StoryCommand.resolve(
        RevertMemory,
        Message.RevertFailed({ error: "A later version changed this part." }),
      ),
      model((m) => {
        expect(Option.isNone(m.reverting)).toBe(true);
        expect(m.error).toBe("A later version changed this part.");
      }),
    );
  });

  it("asks for the login when the revert gets a 401", () => {
    story(
      update,
      given(fileModel()),
      message(Message.ClickedRevert({ id: 2 })),
      StoryCommand.resolve(RevertMemory, Message.RevertLoginRequired()),
      model((m) => {
        expect(Option.isNone(m.reverting)).toBe(true);
      }),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});
