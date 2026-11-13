import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import {
  CreateWorkstream,
  FetchCheckup,
  Navigate,
  RequestLogin,
  SaveIssueRepository,
} from "./commands";
import { Message } from "./message";
import { initialModel, type Model } from "./model";
import { maintenanceWorkstreamRequest, skillsWorkstreamRequest } from "./requests";

export function init(organization: string): UpdateReturn<Model, Message> {
  return { model: initialModel(organization), commands: [FetchCheckup({ organization })] };
}

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    CheckupLoaded: ({ checkup }) => ({
      model: { ...model, checkup: Option.some(checkup), checkupError: "" },
    }),
    CheckupFailed: ({ error }) => ({ model: { ...model, checkupError: error } }),
    LoginRequired: () => ({
      model: {
        ...model,
        picker: Option.map(model.picker, (picker) => ({ ...picker, saving: false })),
      },
      commands: [RequestLogin()],
    }),
    OpenedPicker: () => ({
      model: {
        ...model,
        picker: Option.map(
          Option.filter(model.checkup, (checkup) => checkup.issueRepositorySource !== "config"),
          (checkup) => ({ selected: checkup.issueRepository, error: "", saving: false }),
        ),
      },
    }),
    ClosedPicker: () => ({ model: { ...model, picker: Option.none() } }),
    SelectedRepository: ({ repository }) => ({
      model: {
        ...model,
        picker: Option.map(model.picker, (picker) => ({
          ...picker,
          selected: repository,
          error: "",
        })),
      },
    }),
    ClickedSave: () => {
      if (Option.isNone(model.picker) || model.picker.value.saving) {
        return { model };
      }
      const { selected } = model.picker.value;
      if (selected === "") {
        return { model };
      }
      return {
        model: { ...model, picker: Option.some({ selected, error: "", saving: true }) },
        commands: [SaveIssueRepository({ organization: model.organization, repository: selected })],
      };
    },
    Saved: () => ({
      model: { ...model, picker: Option.none() },
      commands: [FetchCheckup({ organization: model.organization })],
    }),
    SaveFailed: ({ error }) => ({
      model: {
        ...model,
        picker: Option.map(model.picker, (picker) => ({ ...picker, error, saving: false })),
      },
    }),
    ClickedCreate: ({ kind }) => {
      if (Option.isNone(model.checkup) || model.checkup.value.issueRepository === "") {
        return { model };
      }
      const { issueRepository, skillRepository } = model.checkup.value;
      const request =
        kind === "maintenance"
          ? maintenanceWorkstreamRequest(issueRepository)
          : skillsWorkstreamRequest(issueRepository, skillRepository);
      return {
        model: { ...model, creating: Option.some(kind), createError: "" },
        commands: [CreateWorkstream({ organization: model.organization, request })],
      };
    },
    WorkstreamRequested: () => ({ model }),
    CreateFailed: ({ error }) => ({
      model: { ...model, createError: error, creating: Option.none() },
    }),
    NavigateTo: ({ path }) => ({ model, commands: [Navigate({ path })] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedFocusDialog: () => ({ model }),
  });
}
