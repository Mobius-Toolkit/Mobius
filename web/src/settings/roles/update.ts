import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { keepEffort } from "@/lib/harness";
import { GoBack, RequestLogin, ResetRole, SaveRole } from "./commands";
import { Message } from "./message";
import { effortsOf, hasContextLimit, modelsOf, type Model, type Sheet } from "./model";

const withSheet = (model: Model, change: (sheet: Sheet) => Sheet): Model => ({
  ...model,
  sheet: Option.map(model.sheet, change),
});

const closedChoice = (sheet: Sheet): Sheet => ({ ...sheet, openChoice: Option.none() });

function pickHarness(model: Model, sheet: Sheet, value: string): Sheet {
  if (value === sheet.harness) {
    return closedChoice(sheet);
  }
  const first = modelsOf(model, value)[0];
  return {
    ...closedChoice(sheet),
    harness: value,
    model: first?.model ?? "",
    effort: keepEffort(sheet.effort, first?.efforts ?? []),
    context: hasContextLimit(value) ? sheet.context : "",
  };
}

function pickModel(model: Model, sheet: Sheet, value: string): Sheet {
  const chosen = modelsOf(model, sheet.harness).find((other) => other.model === value);
  return {
    ...closedChoice(sheet),
    model: value,
    effort: keepEffort(sheet.effort, chosen?.efforts ?? []),
  };
}

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    RolesLoaded: ({ roles }) => ({ model: { ...model, roles: Option.some(roles) } }),
    RolesFailed: ({ error }) => ({ model: { ...model, error } }),
    HarnessesLoaded: ({ harnesses }) => ({
      model: { ...model, harnesses: Option.some(harnesses) },
    }),
    HarnessesFailed: ({ error }) => ({ model: { ...model, harnesses: Option.some([]), error } }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    OpenedRole: ({ role: name }) => {
      const role = Option.getOrElse(model.roles, () => []).find((other) => other.role === name);
      if (role === undefined) {
        return { model };
      }
      return {
        model: {
          ...model,
          sheet: Option.some({
            role,
            harness: role.harness,
            model: role.model,
            effort: role.effort,
            context: role.context > 0 ? String(role.context) : "",
            max: String(role.max),
            openChoice: Option.none(),
            error: "",
            saving: false,
          }),
        },
      };
    },
    ClosedSheet: () => ({ model: { ...model, sheet: Option.none() } }),
    ToggledChoice: ({ choice }) => ({
      model: withSheet(model, (sheet) => ({
        ...sheet,
        openChoice: Option.isSome(sheet.openChoice) ? Option.none() : Option.some(choice),
      })),
    }),
    ClosedChoice: () => ({ model: withSheet(model, closedChoice) }),
    PressedEscape: () =>
      Option.exists(model.sheet, (sheet) => Option.isSome(sheet.openChoice))
        ? { model: withSheet(model, closedChoice) }
        : { model: { ...model, sheet: Option.none() } },
    PickedHarness: ({ value }) => ({
      model: withSheet(model, (sheet) => pickHarness(model, sheet, value)),
    }),
    PickedModel: ({ value }) => ({
      model: withSheet(model, (sheet) => pickModel(model, sheet, value)),
    }),
    PickedEffort: ({ value }) => ({
      model: withSheet(model, (sheet) => ({ ...closedChoice(sheet), effort: value })),
    }),
    ChangedMax: ({ text }) => ({ model: withSheet(model, (sheet) => ({ ...sheet, max: text })) }),
    ChangedContext: ({ text }) => ({
      model: withSheet(model, (sheet) => ({ ...sheet, context: text })),
    }),
    ClickedSave: () =>
      Option.match(model.sheet, {
        onNone: () => ({ model }),
        onSome: (sheet) => ({
          model: withSheet(model, (current) => ({ ...current, saving: true, error: "" })),
          commands: [
            SaveRole({
              role: sheet.role.role,
              harness: sheet.harness,
              model: sheet.model,
              effort: effortsOf(model, sheet).length > 0 ? sheet.effort : "",
              context: hasContextLimit(sheet.harness) ? Number(sheet.context) : 0,
              max: Number(sheet.max),
            }),
          ],
        }),
      }),
    ClickedReset: () =>
      Option.match(model.sheet, {
        onNone: () => ({ model }),
        onSome: (sheet) => ({
          model: withSheet(model, (current) => ({ ...current, saving: true, error: "" })),
          commands: [ResetRole({ role: sheet.role.role })],
        }),
      }),
    RoleChanged: ({ role }) => ({
      model: {
        ...model,
        roles: Option.map(model.roles, (roles) =>
          roles.map((other) => (other.role === role.role ? role : other)),
        ),
        sheet: Option.filter(model.sheet, (sheet) => sheet.role.role !== role.role),
      },
    }),
    SaveFailed: ({ error }) => ({
      model: withSheet(model, (sheet) => ({ ...sheet, saving: false, error })),
    }),
    SaveLoginRequired: () => ({
      model: withSheet(model, (sheet) => ({ ...sheet, saving: false })),
      commands: [RequestLogin()],
    }),
    ClickedBack: () => ({ model, commands: [GoBack()] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedGoBack: () => ({ model }),
    CompletedFocusSheet: () => ({ model }),
  });
}
