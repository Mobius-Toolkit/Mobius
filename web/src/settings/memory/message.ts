import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { GitHubApp, MemoryFile, MemoryVersion } from "../schemas";

export const Message = defineMessageUnion({
  ReceivedApps: { apps: Schema.Array(GitHubApp) },
  ReceivedOrganizations: { organizations: Schema.Array(Schema.String) },
  FilesLoaded: { files: Schema.Array(MemoryFile) },
  FilesFailed: { error: Schema.String },
  VersionsLoaded: { versions: Schema.Array(MemoryVersion), older: Schema.Boolean },
  VersionsFailed: { error: Schema.String },
  OlderLoaded: { versions: Schema.Array(MemoryVersion), older: Schema.Boolean },
  OlderFailed: { error: Schema.String },
  LoginRequired: {},
  ClickedShowOlder: {},
  ClickedEdit: {},
  ChangedDraft: { text: Schema.String },
  RequestedClose: {},
  PressedEscape: {},
  ClickedKeepEditing: {},
  ClickedDiscard: {},
  ClickedSave: {},
  Saved: {},
  SaveFailed: { error: Schema.String },
  SaveLoginRequired: {},
  ClickedRevert: { id: Schema.Number },
  Reverted: {},
  RevertFailed: { error: Schema.String },
  RevertLoginRequired: {},
  NavigateTo: { path: Schema.String },
  ClickedBack: {},
  CompletedRequestLogin: {},
  CompletedNavigate: {},
  CompletedGoBack: {},
  CompletedFocusDialog: {},
});

export type Message = typeof Message.Type;
