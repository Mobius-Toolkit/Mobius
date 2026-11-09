import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import {
  CreateWorkstream,
  FetchCheckup,
  FixLabels,
  GoBack,
  Navigate,
  OpenChat,
  ReportFixButton,
  RequestLogin,
  SetAutoUpgrade,
  UpgradeTool,
} from "./commands";
import { Message } from "./message";
import { fixButtonOf, type Model } from "./model";
import { maintenanceWorkstreamRequest, skillsWorkstreamRequest } from "./requests";

function step(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedOrganizations: ({ organizations }) => {
      const added = organizations.filter(
        (organization) => !model.organizations.includes(organization),
      );
      return {
        model: { ...model, organizations },
        commands:
          model.page === "overview"
            ? added.map((organization) => FetchCheckup({ organization }))
            : [],
      };
    },
    ReceivedPauses: ({ pauses }) => ({ model: { ...model, pauses } }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    ToolsLoaded: ({ tools }) => ({ model: { ...model, tools: Option.some(tools) } }),
    ToolsFailed: ({ error }) => ({ model: { ...model, toolsError: error } }),
    CheckupLoaded: ({ organization, checkup }) => ({
      model: { ...model, checkups: { ...model.checkups, [organization]: checkup } },
    }),
    CheckupFailed: ({ error }) => ({ model: { ...model, checkupError: error } }),
    OpenedTool: ({ name }) => ({ model: { ...model, openTool: Option.some(name) } }),
    ClosedTool: () => ({
      model: {
        ...model,
        openTool: Option.none(),
        upgrading: false,
        togglingAuto: false,
        upgradeError: "",
      },
    }),
    ClickedUpgrade: ({ tool }) => ({
      model: { ...model, upgrading: true, upgradeError: "" },
      commands: [UpgradeTool({ tool })],
    }),
    ClickedAutoUpgrade: ({ tool, on }) => ({
      model: { ...model, togglingAuto: true, upgradeError: "" },
      commands: [SetAutoUpgrade({ tool, on })],
    }),
    ToolsUpgraded: ({ tools }) => ({
      model: { ...model, tools: Option.some(tools), upgrading: false, togglingAuto: false },
    }),
    UpgradeFailed: ({ error }) => ({
      model: {
        ...model,
        upgrading: false,
        togglingAuto: false,
        upgradeError: Option.isSome(model.openTool) ? error : "",
      },
    }),
    ClickedFix: () =>
      model.fixing
        ? { model }
        : {
            model: { ...model, fixing: true },
            commands: [FixLabels({ organization: model.organization })],
          },
    // The fix can change labels before it fails, so the status loads again.
    LabelsFixed: () => ({
      model: { ...model, fixing: false, checkupError: "" },
      commands: [FetchCheckup({ organization: model.organization })],
    }),
    LabelFixFailed: ({ error }) => ({
      model: { ...model, fixing: false, checkupError: error },
      commands: [FetchCheckup({ organization: model.organization })],
    }),
    ClickedCreate: ({ kind }) => {
      const checkup = model.checkups[model.organization];
      if (checkup === undefined) {
        return { model };
      }
      const request =
        kind === "maintenance"
          ? maintenanceWorkstreamRequest(checkup.issueRepository)
          : skillsWorkstreamRequest(checkup.issueRepository, checkup.skillRepository);
      return {
        model: { ...model, creating: Option.some(kind) },
        commands: [CreateWorkstream({ organization: model.organization, request })],
      };
    },
    WorkstreamRequested: ({ organization }) => ({ model, commands: [OpenChat({ organization })] }),
    CreateFailed: ({ error }) => ({
      model: { ...model, createError: error, creating: Option.none() },
    }),
    NavigateTo: ({ path }) => ({ model, commands: [Navigate({ path })] }),
    ClickedBack: () => ({ model, commands: [GoBack()] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedGoBack: () => ({ model }),
    CompletedOpenChat: () => ({ model }),
    CompletedReportFixButton: () => ({ model }),
    CompletedFocusDialog: () => ({ model }),
  });
}

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  const result = step(model, message);
  const before = fixButtonOf(model);
  const after = fixButtonOf(result.model);
  if (before?.text === after?.text && before?.pending === after?.pending) {
    return result;
  }
  return {
    model: result.model,
    commands: [...(result.commands ?? []), ReportFixButton({ button: after })],
  };
}
