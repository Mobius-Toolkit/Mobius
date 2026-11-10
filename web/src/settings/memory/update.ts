import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import {
  FetchOlder,
  FetchVersions,
  GoBack,
  Navigate,
  RequestLogin,
  RevertMemory,
  SaveMemory,
} from "./commands";
import { Message } from "./message";
import { currentText, newestId, shown, versionsOf, type Editing, type Model } from "./model";

const withEditing = (model: Model, change: (editing: Editing) => Editing): Model => ({
  ...model,
  editing: Option.map(model.editing, change),
});

// A clean draft closes the dialog at once. A changed draft asks for the discard first. Nothing closes while a save runs
// or while the dialog of the discard is open.
function requestClose(model: Model): Model {
  return Option.match(model.editing, {
    onNone: () => model,
    onSome: (editing) => {
      if (editing.saving || editing.discarding) {
        return model;
      }
      return editing.draft === currentText(model)
        ? { ...model, editing: Option.none() }
        : withEditing(model, (current) => ({ ...current, discarding: true }));
    },
  });
}

const reload = (model: Model) =>
  FetchVersions({ owner: model.owner, name: model.name, file: model.file });

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedApps: ({ apps }) => ({ model: { ...model, apps } }),
    ReceivedOrganizations: ({ organizations }) => ({ model: { ...model, organizations } }),
    FilesLoaded: ({ files }) => ({ model: { ...model, files: Option.some(files) } }),
    FilesFailed: ({ error }) => ({ model: { ...model, error } }),
    VersionsLoaded: ({ versions, older }) => ({
      model: { ...model, versions: Option.some(versions.map(shown)), older },
    }),
    VersionsFailed: ({ error }) => ({ model: { ...model, error } }),
    OlderLoaded: ({ versions, older }) => ({
      model: {
        ...model,
        versions: Option.some([...versionsOf(model), ...versions.map(shown)]),
        older,
        loadingOlder: false,
      },
    }),
    OlderFailed: ({ error }) => ({ model: { ...model, error, loadingOlder: false } }),
    LoginRequired: () => ({
      model: { ...model, loadingOlder: false },
      commands: [RequestLogin()],
    }),
    ClickedShowOlder: () => {
      const before = versionsOf(model).at(-1)?.id;
      if (before === undefined) {
        return { model };
      }
      return {
        model: { ...model, loadingOlder: true },
        commands: [FetchOlder({ owner: model.owner, name: model.name, file: model.file, before })],
      };
    },
    ClickedEdit: () =>
      Option.isNone(model.versions)
        ? { model }
        : {
            model: {
              ...model,
              editing: Option.some({
                draft: currentText(model),
                base: newestId(model),
                saving: false,
                error: "",
                discarding: false,
              }),
            },
          },
    ChangedDraft: ({ text }) => ({
      model: withEditing(model, (editing) => ({ ...editing, draft: text })),
    }),
    RequestedClose: () => ({ model: requestClose(model) }),
    PressedEscape: () =>
      Option.exists(model.editing, (editing) => editing.discarding)
        ? { model: withEditing(model, (editing) => ({ ...editing, discarding: false })) }
        : { model: requestClose(model) },
    ClickedKeepEditing: () => ({
      model: withEditing(model, (editing) => ({ ...editing, discarding: false })),
    }),
    ClickedDiscard: () => ({ model: { ...model, editing: Option.none() } }),
    ClickedSave: () =>
      Option.match(model.editing, {
        onNone: () => ({ model }),
        onSome: (editing) => ({
          model: withEditing(model, (current) => ({ ...current, saving: true })),
          commands: [
            SaveMemory({
              owner: model.owner,
              name: model.name,
              file: model.file,
              text: editing.draft,
              base: editing.base,
            }),
          ],
        }),
      }),
    Saved: () => ({ model: { ...model, editing: Option.none() }, commands: [reload(model)] }),
    SaveFailed: ({ error }) => ({
      model: withEditing(model, (editing) => ({ ...editing, saving: false, error })),
    }),
    SaveLoginRequired: () => ({
      model: withEditing(model, (editing) => ({ ...editing, saving: false })),
      commands: [RequestLogin()],
    }),
    ClickedRevert: ({ id }) => ({
      model: { ...model, reverting: Option.some(id) },
      commands: [RevertMemory({ owner: model.owner, name: model.name, file: model.file, id })],
    }),
    Reverted: () => ({
      model: { ...model, reverting: Option.none(), error: "" },
      commands: [reload(model)],
    }),
    RevertFailed: ({ error }) => ({ model: { ...model, reverting: Option.none(), error } }),
    RevertLoginRequired: () => ({
      model: { ...model, reverting: Option.none() },
      commands: [RequestLogin()],
    }),
    NavigateTo: ({ path }) => ({ model, commands: [Navigate({ path })] }),
    ClickedBack: () => ({ model, commands: [GoBack()] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedGoBack: () => ({ model }),
    CompletedFocusDialog: () => ({ model }),
  });
}
