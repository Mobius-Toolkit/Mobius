import { Option, Schema } from "effect";
import { Page, type Flags } from "./flags";
import * as WorkstreamSetup from "@/workstreamSetup/model";
import { Checkup, Pause, ToolCheck, type FixButton } from "./schemas";

export const Model = Schema.Struct({
  page: Page,
  organization: Schema.String,
  organizations: Schema.Array(Schema.String),
  pauses: Schema.Array(Pause),
  tools: Schema.Option(Schema.Array(ToolCheck)),
  toolsError: Schema.String,
  checkups: Schema.Record(Schema.String, Checkup),
  checkupError: Schema.String,
  openTool: Schema.Option(Schema.String),
  upgrading: Schema.Boolean,
  togglingAuto: Schema.Boolean,
  upgradeError: Schema.String,
  fixing: Schema.Boolean,
  workstreamSetup: WorkstreamSetup.Model,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(flags: Flags): Model {
  return {
    page: flags.page,
    organization: flags.organization,
    organizations: [],
    pauses: [],
    tools: Option.none(),
    toolsError: "",
    checkups: {},
    checkupError: "",
    openTool: Option.none(),
    upgrading: false,
    togglingAuto: false,
    upgradeError: "",
    fixing: false,
    workstreamSetup: WorkstreamSetup.initialModel(flags.organization),
  };
}

const fixTexts = { create: "Create labels", fix: "Fix labels" };

// The Labels page shows its fix button in the top bar of the host too.
export function fixButtonOf(model: Model): FixButton | null {
  const checkup = model.checkups[model.organization];
  if (model.page !== "labels" || checkup === undefined || checkup.labelFix === "none") {
    return null;
  }
  return { text: fixTexts[checkup.labelFix], pending: model.fixing };
}

export const toolNeedsYou = (tool: ToolCheck) =>
  tool.outdated || (tool.status !== "" && tool.required);

export const toolProblem = (tool: ToolCheck) =>
  tool.outdated ? "outdated" : tool.status.replace("-", " ");

export function permissionsNeedYou(checkup: Checkup) {
  return (
    checkup.permissionsError !== "" ||
    checkup.permissions.some((permission) => permission.status !== "present")
  );
}

export function labelsNeedYou(checkup: Checkup) {
  return (
    checkup.labelFix !== "none" ||
    checkup.repositories.some((repository) =>
      repository.labels.some((label) => label.status !== "present"),
    )
  );
}

export function workstreamsNeedYou(checkup: Checkup) {
  return (
    checkup.maintenanceWorkstream === 0 ||
    (checkup.skillRepository !== "" && checkup.skillsWorkstream === 0)
  );
}
