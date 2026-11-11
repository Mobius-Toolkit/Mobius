import { Option } from "effect";
import { Command } from "foldkit";
import type { Command as Cmd } from "foldkit/command";
import * as Update from "foldkit/update";
import { toString as urlToString, type Url } from "foldkit/url";
import { update as updateAgents } from "@/agents/update";
import { FetchPage, ListQueued } from "@/chat/commands";
import { initialModel as initialChatModel } from "@/chat/model";
import { update as updateChat } from "@/chat/update";
import { FetchCheckup, FetchTools } from "@/checkup/commands";
import { initialModel as initialCheckupModel } from "@/checkup/model";
import { update as updateCheckup } from "@/checkup/update";
import { initialModel as initialPanelModel } from "@/panel/model";
import { commandsFor as panelCommandsFor, update as updatePanel } from "@/panel/update";
import { FetchDevices } from "@/settings/devices/commands";
import { initialModel as initialDevicesModel } from "@/settings/devices/model";
import { update as updateDevices } from "@/settings/devices/update";
import { initialModel as initialGitHubModel } from "@/settings/github/model";
import { update as updateGitHub } from "@/settings/github/update";
import { FetchServers } from "@/settings/mcp-servers/commands";
import { initialModel as initialMcpServersModel } from "@/settings/mcp-servers/model";
import { update as updateMcpServers } from "@/settings/mcp-servers/update";
import { FetchFiles, FetchVersions } from "@/settings/memory/commands";
import { initialModel as initialMemoryModel } from "@/settings/memory/model";
import { update as updateMemory } from "@/settings/memory/update";
import { FetchVersion, FetchVoice as FetchOverviewVoice } from "@/settings/overview/commands";
import { initialModel as initialOverviewModel } from "@/settings/overview/model";
import { update as updateOverview } from "@/settings/overview/update";
import { FetchHarnesses, FetchRoles } from "@/settings/roles/commands";
import { initialModel as initialRolesModel } from "@/settings/roles/model";
import { update as updateRoles } from "@/settings/roles/update";
import { update as updateSwitches } from "@/settings/switches/update";
import { FetchVoice } from "@/settings/voice/commands";
import { initialModel as initialVoiceModel } from "@/settings/voice/model";
import { update as updateVoice } from "@/settings/voice/update";
import { FetchBoard } from "@/tasks/commands";
import { initialModel as initialTasksModel } from "@/tasks/model";
import { update as updateTasks } from "@/tasks/update";
import { initialModel as initialUsageModel } from "@/usage/model";
import { load as usageLoad, update as updateUsage } from "@/usage/update";
import { initialModel as initialWorkstreamsModel } from "@/workstreams/model";
import { update as updateWorkstreams } from "@/workstreams/update";
import { filterNeedsHuman } from "@/lib/inbox";
import { sameChat } from "@/lib/unread";
import {
  CancelUpgrade,
  CheckUpgrade,
  CloseWorkstream,
  FetchCIStops,
  FetchPauses,
  FetchSwitches,
  GoBack,
  LoadApp,
  LoadExternal,
  LoadReleaseChanges,
  Navigate,
  ReplaceUrl,
  ReloadOnNewBuild,
  ResumePause,
  SelectAutopilot,
  SettleScroll,
  StartUpgrade,
  SubmitLogin,
  ToggleSwitch,
  WaitForStart,
} from "./commands";
import { Message } from "./message";
import {
  initialModel,
  Model,
  savedOrganization,
  saveOrganization,
  type Upgrade,
  type UpgradeState,
} from "./model";
import { coordinatorTrayItems, workstreamTrayItems } from "./tray";
import { parentPath, parseRoute, type AppRoute } from "./route";

type UpdateReturn = Update.Return<Model, Message>;

function applyUpgrade(state: UpgradeState, upgrade: Upgrade): UpgradeState {
  return {
    ...state,
    step: upgrade.step,
    tries: upgrade.tries,
    failure: upgrade.failure,
    nextTry: Option.fromNullishOr(upgrade.nextTry).pipe(Option.map(Date.parse)),
  };
}

function upgradeChanged(model: Model, upgrade: Upgrade): UpdateReturn {
  return {
    model: { ...model, upgrade: applyUpgrade(model.upgrade, upgrade) },
    commands:
      upgrade.step === "restart" && model.upgrade.step !== "restart" ? [ReloadOnNewBuild()] : [],
  };
}

const foldChat = Update.foldChild({
  update: updateChat,
  read: (model: Model) => model.chat,
  write: (model, chat) => ({ ...model, chat: Option.some(chat) }),
  toParentMessage: (message) => Message.GotChatMessage({ message }),
});

const foldPanel = Update.foldChild({
  update: updatePanel,
  read: (model: Model) => model.panel,
  write: (model, panel) => ({ ...model, panel: Option.some(panel) }),
  toParentMessage: (message) => Message.GotPanelMessage({ message }),
});

const foldInbox = Update.foldChild({
  update: updateChat,
  read: (model: Model) => model.inboxProgram,
  write: (model, inboxProgram) => ({ ...model, inboxProgram: Option.some(inboxProgram) }),
  toParentMessage: (message) => Message.GotInboxMessage({ message }),
});

const foldWorkstreams = Update.foldChild({
  update: updateWorkstreams,
  read: (model: Model) => model.workstreamsProgram,
  write: (model, workstreamsProgram) => ({
    ...model,
    workstreamsProgram: Option.some(workstreamsProgram),
  }),
  toParentMessage: (message) => Message.GotWorkstreamsMessage({ message }),
});

const foldTasks = Update.foldChild({
  update: updateTasks,
  read: (model: Model) => model.tasksProgram,
  write: (model, tasksProgram) => ({ ...model, tasksProgram: Option.some(tasksProgram) }),
  toParentMessage: (message) => Message.GotTasksMessage({ message }),
});

const foldAgents = Update.foldChild({
  update: updateAgents,
  read: (model: Model) => model.agentsProgram,
  write: (model, agentsProgram) => ({ ...model, agentsProgram: Option.some(agentsProgram) }),
  toParentMessage: (message) => Message.GotAgentsMessage({ message }),
});

const foldCheckup = Update.foldChild({
  update: updateCheckup,
  read: (model: Model) => model.checkupProgram,
  write: (model, checkupProgram) => ({ ...model, checkupProgram: Option.some(checkupProgram) }),
  toParentMessage: (message) => Message.GotCheckupMessage({ message }),
});

const foldUsage = Update.foldChild({
  update: updateUsage,
  read: (model: Model) => model.usageProgram,
  write: (model, usageProgram) => ({ ...model, usageProgram: Option.some(usageProgram) }),
  toParentMessage: (message) => Message.GotUsageMessage({ message }),
});

const foldSettings = Update.foldChild({
  update: updateOverview,
  read: (model: Model) => model.settingsProgram,
  write: (model, settingsProgram) => ({ ...model, settingsProgram: Option.some(settingsProgram) }),
  toParentMessage: (message) => Message.GotSettingsMessage({ message }),
});

const foldRoles = Update.foldChild({
  update: updateRoles,
  read: (model: Model) => model.rolesProgram,
  write: (model, rolesProgram) => ({ ...model, rolesProgram: Option.some(rolesProgram) }),
  toParentMessage: (message) => Message.GotRolesMessage({ message }),
});

const foldMemory = Update.foldChild({
  update: updateMemory,
  read: (model: Model) => model.memoryProgram,
  write: (model, memoryProgram) => ({ ...model, memoryProgram: Option.some(memoryProgram) }),
  toParentMessage: (message) => Message.GotMemoryMessage({ message }),
});

const foldMcpServers = Update.foldChild({
  update: updateMcpServers,
  read: (model: Model) => model.mcpServersProgram,
  write: (model, mcpServersProgram) => ({
    ...model,
    mcpServersProgram: Option.some(mcpServersProgram),
  }),
  toParentMessage: (message) => Message.GotMcpServersMessage({ message }),
});

const foldSwitches = Update.foldChild({
  update: updateSwitches,
  read: (model: Model) => model.switchesProgram,
  write: (model, switchesProgram) => ({ ...model, switchesProgram: Option.some(switchesProgram) }),
  toParentMessage: (message) => Message.GotSwitchesMessage({ message }),
});

const foldVoice = Update.foldChild({
  update: updateVoice,
  read: (model: Model) => model.voiceProgram,
  write: (model, voiceProgram) => ({ ...model, voiceProgram: Option.some(voiceProgram) }),
  toParentMessage: (message) => Message.GotVoiceMessage({ message }),
});

const foldDevices = Update.foldChild({
  update: updateDevices,
  read: (model: Model) => model.devicesProgram,
  write: (model, devicesProgram) => ({ ...model, devicesProgram: Option.some(devicesProgram) }),
  toParentMessage: (message) => Message.GotDevicesMessage({ message }),
});

const foldGitHub = Update.foldChild({
  update: updateGitHub,
  read: (model: Model) => model.githubProgram,
  write: (model, githubProgram) => ({ ...model, githubProgram: Option.some(githubProgram) }),
  toParentMessage: (message) => Message.GotGitHubMessage({ message }),
});

// A screen that the Owner left has no Submodel, so no live event reaches it.
const noScreens: Pick<
  Model,
  | "chat"
  | "panel"
  | "inboxProgram"
  | "workstreamsProgram"
  | "tasksProgram"
  | "agentsProgram"
  | "checkupProgram"
  | "usageProgram"
  | "settingsProgram"
  | "rolesProgram"
  | "memoryProgram"
  | "mcpServersProgram"
  | "switchesProgram"
  | "voiceProgram"
  | "devicesProgram"
  | "githubProgram"
> = {
  chat: Option.none(),
  panel: Option.none(),
  inboxProgram: Option.none(),
  workstreamsProgram: Option.none(),
  tasksProgram: Option.none(),
  agentsProgram: Option.none(),
  checkupProgram: Option.none(),
  usageProgram: Option.none(),
  settingsProgram: Option.none(),
  rolesProgram: Option.none(),
  memoryProgram: Option.none(),
  mcpServersProgram: Option.none(),
  switchesProgram: Option.none(),
  voiceProgram: Option.none(),
  devicesProgram: Option.none(),
  githubProgram: Option.none(),
};

function initScreenForRoute(
  initialScreenModel: Model,
  route: AppRoute,
): { model: Model; commands: Cmd<Message>[] } {
  let model: Model = { ...initialScreenModel, ...noScreens };
  const commands: Cmd<Message>[] = [];

  switch (route._tag) {
    case "CoordinatorChat": {
      const organization = model.organization;
      const chatModel = initialChatModel({
        organization,
        repository: "",
        workstream: 0,
        agent: "Coordinator",
      });
      const trayItems = coordinatorTrayItems(model.inbox, organization);
      model = {
        ...model,
        chat: Option.some({ ...chatModel, trayItems }),
        panel: Option.none(),
      };
      commands.push(
        Command.mapMessage(
          FetchPage({
            organization,
            repository: "",
            workstream: 0,
            isInitial: true,
            known: null,
            sent: null,
          }),
          (message) => Message.GotChatMessage({ message }),
        ),
        Command.mapMessage(ListQueued({ organization, repository: "", workstream: 0 }), (message) =>
          Message.GotChatMessage({ message }),
        ),
      );
      break;
    }

    case "WorkstreamChat": {
      const { owner, name, number } = route;
      const repository = `${owner}/${name}`;
      const chatModel = initialChatModel({
        organization: owner,
        repository,
        workstream: number,
        agent: "Lead",
      });
      const panelModel = initialPanelModel({
        owner,
        name,
        number,
        instance: "side",
        tab: "tasks",
      });
      const inWorkstream = workstreamTrayItems(model.inbox, repository, number);
      const workstream = model.workstreams.find(
        (other) => other.repository === repository && other.number === number,
      );

      model = {
        ...model,
        chat: Option.some({
          ...chatModel,
          brief: workstream ?? null,
          trayItems: inWorkstream,
        }),
        panel: Option.some(panelModel),
      };
      commands.push(
        Command.mapMessage(
          FetchPage({
            organization: owner,
            repository,
            workstream: number,
            isInitial: true,
            known: null,
            sent: null,
          }),
          (message) => Message.GotChatMessage({ message }),
        ),
        Command.mapMessage(
          ListQueued({ organization: owner, repository, workstream: number }),
          (message) => Message.GotChatMessage({ message }),
        ),
        ...Command.mapMessages(panelCommandsFor(panelModel), (message) =>
          Message.GotPanelMessage({ message }),
        ),
      );
      break;
    }

    case "Inbox": {
      const inboxModel = initialChatModel({
        organization: model.organization,
        repository: "",
        workstream: 0,
        agent: "Coordinator",
      });
      const trayItems = model.inbox.filter((item) => item.organization === model.organization);
      model = {
        ...model,
        inboxProgram: Option.some({ ...inboxModel, trayItems }),
      };
      break;
    }

    case "Workstreams": {
      model = {
        ...model,
        workstreamsProgram: Option.some({
          ...initialWorkstreamsModel({ organization: model.organization }),
          workstreams:
            model.workstreams.length > 0 ? Option.some(model.workstreams) : Option.none(),
          needsHuman: filterNeedsHuman(model.needsHuman, model.inboxAnswered),
          working: model.workstreamsWorking,
          unread: model.unread,
        }),
      };
      break;
    }

    case "Tasks": {
      const tasksModel = initialTasksModel({ organization: model.organization });
      model = {
        ...model,
        tasksProgram: Option.some(tasksModel),
      };
      commands.push(
        Command.mapMessage(FetchBoard(), (message) => Message.GotTasksMessage({ message })),
      );
      break;
    }

    case "Agents": {
      model = {
        ...model,
        agentsProgram: Option.some({
          agents: Option.none(),
          transcript: Option.none(),
          request: 0,
          error: "",
        }),
      };
      break;
    }

    case "Usage": {
      const usageModel = initialUsageModel({
        search: route.search,
        now: Date.now(),
        timeZone: new Intl.DateTimeFormat().resolvedOptions().timeZone,
      });
      model = {
        ...model,
        usageProgram: Option.some(usageModel),
      };
      commands.push(
        Command.mapMessage(usageLoad(usageModel), (message) =>
          Message.GotUsageMessage({ message }),
        ),
      );
      break;
    }

    case "Settings": {
      let settingsModel = initialOverviewModel;
      if (Option.isSome(model.switches)) {
        settingsModel = { ...settingsModel, switches: model.switches };
      }
      settingsModel = {
        ...settingsModel,
        errors: {
          start: model.switchErrors.start ?? "",
          merge: model.switchErrors.merge ?? "",
          repairs: model.switchErrors.repairs ?? "",
        },
      };
      model = {
        ...model,
        settingsProgram: Option.some(settingsModel),
      };
      commands.push(
        Command.mapMessage(FetchVersion(), (message) => Message.GotSettingsMessage({ message })),
        Command.mapMessage(FetchOverviewVoice(), (message) =>
          Message.GotSettingsMessage({ message }),
        ),
      );
      break;
    }

    case "Checkup": {
      const checkupModel = initialCheckupModel({
        page: "overview",
        organization: model.organization,
      });
      model = {
        ...model,
        checkupProgram: Option.some({
          ...checkupModel,
          pauses: model.pauses,
        }),
      };
      commands.push(
        Command.mapMessage(FetchTools(), (message) => Message.GotCheckupMessage({ message })),
      );
      if (model.organizations.length > 0) {
        const { model: nextModel, commands: subCommands } = foldCheckup(model, {
          _tag: "ReceivedOrganizations",
          organizations: model.organizations,
        });
        model = nextModel;
        commands.push(...(subCommands ?? []));
      }
      break;
    }

    case "CheckupTools": {
      const checkupModel = initialCheckupModel({ page: "tools", organization: model.organization });
      model = {
        ...model,
        checkupProgram: Option.some({
          ...checkupModel,
          pauses: model.pauses,
        }),
      };
      commands.push(
        Command.mapMessage(FetchTools(), (message) => Message.GotCheckupMessage({ message })),
      );
      break;
    }

    case "CheckupPermissions":
    case "CheckupLabels":
    case "CheckupWorkstreams": {
      const page =
        route._tag === "CheckupPermissions"
          ? ("permissions" as const)
          : route._tag === "CheckupLabels"
            ? ("labels" as const)
            : ("workstreams" as const);
      const checkupModel = initialCheckupModel({ page, organization: route.organization });
      model = {
        ...model,
        checkupProgram: Option.some({
          ...checkupModel,
          pauses: model.pauses,
        }),
      };
      commands.push(
        Command.mapMessage(FetchCheckup({ organization: route.organization }), (message) =>
          Message.GotCheckupMessage({ message }),
        ),
      );
      break;
    }

    case "Roles": {
      model = {
        ...model,
        rolesProgram: Option.some(initialRolesModel),
      };
      commands.push(
        Command.mapMessage(FetchRoles(), (message) => Message.GotRolesMessage({ message })),
        Command.mapMessage(FetchHarnesses(), (message) => Message.GotRolesMessage({ message })),
      );
      break;
    }

    case "MemoryRepositories": {
      const memoryModel = initialMemoryModel({
        page: "repositories",
        owner: "",
        name: "",
        file: "",
      });
      model = {
        ...model,
        memoryProgram: Option.some({
          ...memoryModel,
          apps: Option.isSome(model.apps) ? model.apps.value : [],
          organizations: model.organizations,
        }),
      };
      break;
    }

    case "Memory": {
      const memoryModel = initialMemoryModel({
        page: "files",
        owner: route.owner,
        name: route.name,
        file: "",
      });
      model = {
        ...model,
        memoryProgram: Option.some({
          ...memoryModel,
          apps: Option.isSome(model.apps) ? model.apps.value : [],
          organizations: model.organizations,
        }),
      };
      commands.push(
        Command.mapMessage(FetchFiles({ owner: route.owner, name: route.name }), (message) =>
          Message.GotMemoryMessage({ message }),
        ),
      );
      break;
    }

    case "MemoryFile": {
      const memoryModel = initialMemoryModel({
        page: "file",
        owner: route.owner,
        name: route.name,
        file: route.file,
      });
      model = {
        ...model,
        memoryProgram: Option.some({
          ...memoryModel,
          apps: Option.isSome(model.apps) ? model.apps.value : [],
          organizations: model.organizations,
        }),
      };
      commands.push(
        Command.mapMessage(
          FetchVersions({ owner: route.owner, name: route.name, file: route.file }),
          (message) => Message.GotMemoryMessage({ message }),
        ),
      );
      break;
    }

    case "McpServers": {
      const callback = {
        oauth: route.search.oauth ?? ("" as const),
        server: route.search.server ?? 0,
        reason: route.search.reason ?? "",
      };
      model = {
        ...model,
        mcpServersProgram: Option.some(
          initialMcpServersModel({ organization: model.organization, callback }),
        ),
      };
      commands.push(
        Command.mapMessage(FetchServers({ organization: model.organization }), (message) =>
          Message.GotMcpServersMessage({ message }),
        ),
      );
      break;
    }

    case "SwitchSettings": {
      model = {
        ...model,
        switchesProgram: Option.some({
          switches: model.switches,
          errors: {
            start: model.switchErrors.start ?? "",
            merge: model.switchErrors.merge ?? "",
            repairs: model.switchErrors.repairs ?? "",
          },
        }),
      };
      break;
    }

    case "Voice": {
      model = {
        ...model,
        voiceProgram: Option.some(initialVoiceModel),
      };
      commands.push(
        Command.mapMessage(FetchVoice(), (message) => Message.GotVoiceMessage({ message })),
      );
      break;
    }

    case "Devices": {
      model = {
        ...model,
        devicesProgram: Option.some(initialDevicesModel),
      };
      commands.push(
        Command.mapMessage(FetchDevices(), (message) => Message.GotDevicesMessage({ message })),
      );
      break;
    }

    case "GitHub": {
      const apps = model.apps._tag === "Some" ? model.apps.value : [];
      const setup = !(apps.length > 0);
      model = {
        ...model,
        githubProgram: Option.some({
          ...initialGitHubModel({ setup }),
          apps,
        }),
      };
      break;
    }

    default:
      break;
  }

  return { model, commands };
}

function withSwitchErrors(
  model: Model,
  errors: Record<string, string>,
  failures: Record<string, boolean>,
): Model {
  const shown = {
    start: errors.start ?? "",
    merge: errors.merge ?? "",
    repairs: errors.repairs ?? "",
  };
  return {
    ...model,
    switchErrors: errors,
    switchFailures: failures,
    switchesProgram: Option.map(model.switchesProgram, (program) => ({
      ...program,
      errors: shown,
    })),
    settingsProgram: Option.map(model.settingsProgram, (program) => ({
      ...program,
      errors: shown,
    })),
  };
}

function enterOrganization(
  model: Model,
  organization: string,
): { model: Model; commands: Cmd<Message>[] } {
  const next: Model = { ...model, organization };
  const route = model.route;
  if (
    route._tag === "CoordinatorChat" &&
    model.organizations.includes(organization) &&
    !Option.exists(model.chat, (chat) => chat.organization === organization)
  ) {
    return initScreenForRoute(next, route);
  }
  if (
    route._tag === "McpServers" &&
    Option.exists(model.mcpServersProgram, (servers) => servers.organization !== organization)
  ) {
    return initScreenForRoute(next, route);
  }
  if (Option.isSome(model.inboxProgram) && model.inboxProgram.value.organization !== organization) {
    return {
      model: {
        ...next,
        inboxProgram: Option.some({
          ...model.inboxProgram.value,
          organization,
          trayItems: model.inbox.filter((item) => item.organization === organization),
        }),
      },
      commands: [],
    };
  }
  return { model: next, commands: [] };
}

export function init(url: Url): UpdateReturn {
  const route = parseRoute(url);
  if (route._tag === "WorkstreamChat") {
    saveOrganization(route.owner);
  }
  const model = initialModel(route);
  const commands: Cmd<Message>[] = [
    LoadApp(),
    CheckUpgrade(),
    FetchPauses(),
    FetchCIStops(),
    FetchSwitches(),
    WaitForStart(),
  ];

  if (route._tag === "NotFound") {
    commands.push(ReplaceUrl({ path: "/chat" }));
  } else {
    const screenInit = initScreenForRoute(model, route);
    commands.push(...screenInit.commands);
    return { model: screenInit.model, commands };
  }

  return { model, commands };
}

const backRequested = (model: Model) =>
  Message.BackRequested({ parent: parentPath(model.route) ?? "/chat" });

export function update(model: Model, message: Message): UpdateReturn {
  switch (message._tag) {
    case "UrlRequested": {
      if (message.request._tag === "Internal") {
        return {
          model,
          commands: [Navigate({ path: urlToString(message.request.url) })],
        };
      }
      return {
        model,
        commands: [LoadExternal({ href: message.request.href })],
      };
    }

    case "UrlChanged": {
      const nextRoute = parseRoute(message.url);
      if (nextRoute._tag === "NotFound") {
        return {
          model: { ...model, route: nextRoute },
          commands: [ReplaceUrl({ path: "/chat" })],
        };
      }

      let org = model.organization;
      if (nextRoute._tag === "WorkstreamChat") {
        org = nextRoute.owner;
        saveOrganization(org);
      }

      if (
        model.route._tag === "Usage" &&
        nextRoute._tag === "Usage" &&
        Option.isSome(model.usageProgram)
      ) {
        const { model: nextModel, commands } = foldUsage(
          { ...model, route: nextRoute, organization: org },
          { _tag: "ReceivedSearch", search: nextRoute.search },
        );
        return { model: nextModel, commands: [...(commands ?? []), SettleScroll()] };
      }

      if (
        model.route._tag === "McpServers" &&
        nextRoute._tag === "McpServers" &&
        Option.isSome(model.mcpServersProgram)
      ) {
        return {
          model: { ...model, route: nextRoute, organization: org },
          commands: [SettleScroll()],
        };
      }

      const screenInit = initScreenForRoute(
        { ...model, route: nextRoute, organization: org },
        nextRoute,
      );

      return {
        model: screenInit.model,
        commands: [...screenInit.commands, SettleScroll()],
      };
    }

    case "NavigateRequested":
      return { model, commands: [Navigate({ path: message.path })] };

    case "BackRequested":
      return { model, commands: [GoBack({ parent: message.parent })] };

    case "CompletedNavigate":
    case "CompletedSettleScroll":
    case "CompletedReplaceUrl":
    case "CompletedGoBack":
    case "CompletedReloadOnNewBuild":
    case "CompletedLoadExternal":
    case "CompletedFocusSheet":
      return { model, commands: [] };

    case "ShowLogin":
      return { model: { ...model, loginShown: true }, commands: [] };

    case "HideLogin":
      return { model: { ...model, loginShown: false }, commands: [] };

    case "PasswordInputChanged":
      return { model: { ...model, passwordInput: message.password }, commands: [] };

    case "SubmitLoginRequested":
      return {
        model: { ...model, loginBusy: true, loginError: "" },
        commands: [SubmitLogin({ password: model.passwordInput })],
      };

    case "LoginSucceeded":
      return {
        model: { ...model, loginShown: false, loginBusy: false, passwordInput: "" },
        commands: [LoadApp(), CheckUpgrade(), FetchPauses(), FetchCIStops(), FetchSwitches()],
      };

    case "LoginFailed":
      return {
        model: { ...model, loginBusy: false, loginError: message.error },
        commands: [],
      };

    case "AppsAndOrganizationsLoaded": {
      const { apps, organizations } = message;
      let org = model.organization;
      if (model.route._tag === "WorkstreamChat" && organizations.includes(model.route.owner)) {
        saveOrganization(model.route.owner);
        org = model.route.owner;
      } else {
        const saved = savedOrganization();
        org = organizations.includes(org)
          ? org
          : organizations.includes(saved)
            ? saved
            : (organizations[0] ?? "");
      }

      let githubProgram = model.githubProgram;
      if (Option.isSome(githubProgram)) {
        const setup = !(apps.length > 0);
        githubProgram = Option.some({
          ...githubProgram.value,
          apps,
          setup,
        });
      }

      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          organization: org,
        });
      }

      let tasksProgram = model.tasksProgram;
      if (Option.isSome(tasksProgram)) {
        tasksProgram = Option.some({
          ...tasksProgram.value,
          organization: org,
        });
      }

      const entered = enterOrganization(
        {
          ...model,
          apps: Option.some(apps),
          organizations,
          githubProgram,
          workstreamsProgram,
          tasksProgram,
          appError: Option.none(),
        },
        org,
      );
      let updatedModel: Model = entered.model;
      const commands: Cmd<Message>[] = [...entered.commands];

      if (Option.isSome(updatedModel.checkupProgram)) {
        const { model: m, commands: c } = foldCheckup(updatedModel, {
          _tag: "ReceivedOrganizations",
          organizations,
        });
        updatedModel = m;
        commands.push(...(c ?? []));
      }

      if (Option.isSome(updatedModel.memoryProgram)) {
        const { model: m1 } = foldMemory(updatedModel, {
          _tag: "ReceivedApps",
          apps,
        });
        const { model: m2 } = foldMemory(m1, {
          _tag: "ReceivedOrganizations",
          organizations,
        });
        updatedModel = m2;
      }

      return { model: updatedModel, commands };
    }

    case "LoadAppFailed":
      return {
        // The Offline badge says why a load fails while the browser is offline.
        model: model.online ? { ...model, appError: Option.some(message.error) } : model,
        commands: [],
      };

    case "SelectOrganization": {
      const org = message.organization;
      saveOrganization(org);
      const commands: Cmd<Message>[] = [];
      if (model.route._tag === "WorkstreamChat" && model.route.owner !== org) {
        commands.push(Navigate({ path: "/workstreams" }));
      }
      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          organization: org,
        });
      }
      let tasksProgram = model.tasksProgram;
      if (Option.isSome(tasksProgram)) {
        tasksProgram = Option.some({
          ...tasksProgram.value,
          organization: org,
        });
      }
      const entered = enterOrganization({ ...model, workstreamsProgram, tasksProgram }, org);
      return { model: entered.model, commands: [...commands, ...entered.commands] };
    }

    case "OnlineChanged":
      return { model: { ...model, online: message.online }, commands: [] };

    case "WideChanged":
      return { model: { ...model, wide: message.wide }, commands: [] };

    case "LiveOpened": {
      const commands: Cmd<Message>[] = [
        LoadApp(),
        CheckUpgrade(),
        FetchPauses(),
        FetchCIStops(),
        FetchSwitches(),
      ];
      let currentModel: Model = { ...model, link: "connected" };

      const route = model.route._tag;
      if (
        (route === "CoordinatorChat" || route === "WorkstreamChat") &&
        Option.isSome(currentModel.chat)
      ) {
        const { model: m, commands: c } = foldChat(currentModel, { _tag: "ConnectionOpened" });
        currentModel = m;
        commands.push(...(c ?? []));
      }
      if (route === "WorkstreamChat" && Option.isSome(currentModel.panel)) {
        const { model: m, commands: c } = foldPanel(currentModel, { _tag: "ConnectionOpened" });
        currentModel = m;
        commands.push(...(c ?? []));
      }
      if (route === "Tasks" && Option.isSome(currentModel.tasksProgram)) {
        const { model: m, commands: c } = foldTasks(currentModel, { _tag: "ConnectionOpened" });
        currentModel = m;
        commands.push(...(c ?? []));
      }
      if (route === "Agents" && Option.isSome(currentModel.agentsProgram)) {
        const { model: m, commands: c } = foldAgents(currentModel, { _tag: "ConnectionOpened" });
        currentModel = m;
        commands.push(...(c ?? []));
      }

      return { model: currentModel, commands };
    }

    case "LiveConnecting":
      return {
        model: { ...model, link: model.link === "starting" ? "starting" : "connecting" },
        commands: [],
      };

    case "StartLimitReached":
      return {
        model: { ...model, link: model.link === "starting" ? "connecting" : model.link },
        commands: [],
      };

    case "LiveLoginRequired":
      return { model: { ...model, loginShown: true }, commands: [] };

    case "RepositoriesChanged":
      return { model, commands: [LoadApp()] };

    case "UnreadStoreChanged": {
      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          unread: message.unread,
        });
      }
      return { model: { ...model, unread: message.unread, workstreamsProgram }, commands: [] };
    }

    case "WorkstreamsStoreChanged": {
      const { workstreams, needsHuman, working } = message;
      const commands: Cmd<Message>[] = [];
      let chosenAutopilot = model.chosenAutopilot;
      if (model.route._tag === "WorkstreamChat") {
        const { owner, name, number } = model.route;
        const current = workstreams.find(
          (w) => w.repository === `${owner}/${name}` && w.number === number,
        );
        if (workstreams.length > 0 && !current) {
          commands.push(ReplaceUrl({ path: "/workstreams" }));
        }
        const serverLevel = current?.autopilot ?? "off";
        chosenAutopilot = Option.filter(chosenAutopilot, (chosen) => chosen.from === serverLevel);
      }
      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          workstreams: Option.some(workstreams),
          needsHuman: filterNeedsHuman(needsHuman, model.inboxAnswered),
          working,
        });
      }
      let chat = model.chat;
      if (Option.isSome(chat) && model.route._tag === "WorkstreamChat") {
        const repo = `${model.route.owner}/${model.route.name}`;
        const num = model.route.number;
        const workstream = workstreams.find(
          (other) => other.repository === repo && other.number === num,
        );
        if (workstream) {
          chat = Option.some({ ...chat.value, brief: workstream });
        }
      }
      return {
        model: {
          ...model,
          workstreams,
          needsHuman,
          workstreamsWorking: working,
          workstreamsProgram,
          chat,
          chosenAutopilot,
        },
        commands,
      };
    }

    case "InboxStoreChanged": {
      const { items, answered } = message;
      let currentModel: Model = {
        ...model,
        inbox: items,
        inboxAnswered: answered,
        workstreamsProgram: Option.map(model.workstreamsProgram, (program) => ({
          ...program,
          needsHuman: filterNeedsHuman(model.needsHuman, answered),
        })),
      };
      const commands: Cmd<Message>[] = [];

      if (Option.isSome(currentModel.chat)) {
        const { organization, repository, workstream } = currentModel.chat.value;
        const trayItems = repository
          ? workstreamTrayItems(items, repository, workstream)
          : coordinatorTrayItems(items, organization);
        const { model: m, commands: c } = foldChat(currentModel, {
          _tag: "ReceivedTrayItems",
          items: trayItems,
        });
        currentModel = m;
        commands.push(...(c ?? []));
      }

      if (Option.isSome(currentModel.inboxProgram)) {
        const { model: m, commands: c } = foldInbox(currentModel, {
          _tag: "ReceivedTrayItems",
          items: items.filter((item) => item.organization === currentModel.organization),
        });
        currentModel = m;
        commands.push(...(c ?? []));
      }

      return { model: currentModel, commands };
    }

    case "NoticeStoreChanged":
      return { model: { ...model, notice: message.notice }, commands: [] };

    case "NewBuildStoreChanged":
      return { model: { ...model, newBuild: message.newBuild }, commands: [] };

    case "CIStopsChanged":
      return { model: { ...model, ciStops: message.stops }, commands: [] };

    case "PausesChanged": {
      let currentModel: Model = { ...model, pauses: message.pauses };
      if (Option.isSome(currentModel.checkupProgram)) {
        const { model: nextModel } = foldCheckup(currentModel, {
          _tag: "ReceivedPauses",
          pauses: message.pauses,
        });
        currentModel = nextModel;
      }
      return { model: currentModel, commands: [] };
    }

    case "ResumePauseRequested":
      return {
        model: { ...model, pauseBusy: Option.some(message.harness) },
        commands: [ResumePause({ harness: message.harness })],
      };

    case "ResumePauseSucceeded":
      return {
        model: { ...model, pauseBusy: Option.none(), pauseError: Option.none() },
        commands: [],
      };

    case "ResumePauseFailed":
      return {
        model: {
          ...model,
          pauseBusy: Option.none(),
          pauseError: Option.some(message.error),
        },
        commands: [],
      };

    case "SwitchesChanged": {
      let switchesProgram = model.switchesProgram;
      if (Option.isSome(switchesProgram)) {
        switchesProgram = Option.some({
          ...switchesProgram.value,
          switches: Option.some(message.switches),
        });
      }
      let settingsProgram = model.settingsProgram;
      if (Option.isSome(settingsProgram)) {
        settingsProgram = Option.some({
          ...settingsProgram.value,
          switches: Option.some(message.switches),
        });
      }
      const server: Record<string, boolean> = {
        start: message.switches.start,
        merge: message.switches.merge,
        repairs: message.switches.repairs,
      };
      const keeps = (kind: string) => model.switchFailures[kind] === server[kind];
      return {
        model: withSwitchErrors(
          {
            ...model,
            switches: Option.some(message.switches),
            switchesProgram,
            settingsProgram,
          },
          Object.fromEntries(Object.entries(model.switchErrors).filter(([kind]) => keeps(kind))),
          Object.fromEntries(Object.entries(model.switchFailures).filter(([kind]) => keeps(kind))),
        ),
        commands: [],
      };
    }

    case "ToggleSwitchRequested": {
      const current = Option.getOrUndefined(model.switches);
      if (current) {
        const optimistic = { ...current, [message.kind]: message.on };
        const withoutKind = <A>(record: Record<string, A>) =>
          Object.fromEntries(Object.entries(record).filter(([kind]) => kind !== message.kind));
        const cleared = withSwitchErrors(
          model,
          withoutKind(model.switchErrors),
          withoutKind(model.switchFailures),
        );
        return {
          model: {
            ...cleared,
            switches: Option.some(optimistic),
            switchBusy: Option.some(message.kind),
            switchesProgram: Option.map(cleared.switchesProgram, (program) => ({
              ...program,
              switches: Option.some(optimistic),
            })),
          },
          commands: [ToggleSwitch({ kind: message.kind, on: message.on })],
        };
      }
      return { model, commands: [] };
    }

    case "ToggleSwitchSucceeded":
      return {
        model: { ...model, switchBusy: Option.none() },
        commands: [],
      };

    case "ToggleSwitchFailed": {
      const current = Option.getOrUndefined(model.switches);
      // A read can come between the request and its failure, so the value before the request is the opposite of the
      // requested value.
      const reverted = current ? { ...current, [message.kind]: !message.on } : current;
      const withErrors = withSwitchErrors(
        model,
        { ...model.switchErrors, [message.kind]: message.error },
        { ...model.switchFailures, [message.kind]: !message.on },
      );
      return {
        model: {
          ...withErrors,
          switches: Option.fromNullishOr(reverted),
          switchBusy: Option.none(),
          switchesProgram: Option.map(withErrors.switchesProgram, (program) => ({
            ...program,
            switches: Option.fromNullishOr(reverted),
          })),
          settingsProgram: Option.map(withErrors.settingsProgram, (program) => ({
            ...program,
            switches: Option.fromNullishOr(reverted),
          })),
        },
        commands: [FetchSwitches()],
      };
    }

    case "OpenHeldSheet":
      return {
        model: { ...model, heldSheet: Option.some({ title: message.title, held: message.held }) },
        commands: [],
      };

    case "CloseHeldSheet":
      return { model: { ...model, heldSheet: Option.none() }, commands: [] };

    case "DrainChanged":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            drain: Option.some(message.drain),
            cancelling: !message.drain.on ? false : model.upgrade.cancelling,
          },
        },
        commands: [],
      };

    case "UpgradeChanged":
      return upgradeChanged(model, message.upgrade);

    case "UpgradeTicked":
      return {
        model: { ...model, upgrade: { ...model.upgrade, now: message.now } },
        commands: [],
      };

    case "UpgradeInfoLoaded": {
      const { version, drain, upgrade } = message;
      const loaded = {
        ...model,
        upgrade: {
          ...model.upgrade,
          version: version ?? model.upgrade.version,
          drain: drain ? Option.some(drain) : model.upgrade.drain,
          cancelling: drain && !drain.on ? false : model.upgrade.cancelling,
        },
      };
      return upgrade ? upgradeChanged(loaded, upgrade) : { model: loaded, commands: [] };
    }

    case "ShowUpgradeChanges":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changesShown: true },
        },
        commands: [LoadReleaseChanges(), CheckUpgrade()],
      };

    case "HideUpgradeChanges":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changesShown: false },
        },
        commands: [],
      };

    case "ReleaseChangesLoaded":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changes: Option.some(message.changes) },
        },
        commands: [],
      };

    case "ReleaseChangesFailed":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changesError: message.error },
        },
        commands: [],
      };

    case "StartUpgradeRequested":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            changesShown: false,
            upgrading: true,
            failure: "",
          },
        },
        commands: [StartUpgrade()],
      };

    case "StartUpgradeSucceeded": {
      const started = { ...model, upgrade: { ...model.upgrade, upgrading: false } };
      return model.upgrade.step === "idle"
        ? upgradeChanged(started, message.upgrade)
        : { model: started, commands: [] };
    }

    case "StartUpgradeFailed":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            upgrading: false,
            failure: message.error,
          },
        },
        commands: [],
      };

    case "CancelUpgradeRequested":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, cancelling: true },
        },
        commands: [CancelUpgrade()],
      };

    case "CancelUpgradeSucceeded":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, cancelling: false },
        },
        commands: [CheckUpgrade()],
      };

    case "CancelUpgradeFailed":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            cancelling: false,
            failure: message.error,
          },
        },
        commands: [],
      };

    case "SelectAutopilotRequested": {
      if (model.route._tag === "WorkstreamChat") {
        const { owner, name, number } = model.route;
        const workstream = model.workstreams.find(
          (w) => w.repository === `${owner}/${name}` && w.number === number,
        );
        const from = workstream?.autopilot ?? "off";
        return {
          model: { ...model, autopilotBusy: true, autopilotError: "", openMenu: Option.none() },
          commands: [SelectAutopilot({ owner, name, number, from, to: message.level })],
        };
      }
      return { model, commands: [] };
    }

    case "SelectAutopilotSucceeded":
      return {
        model: {
          ...model,
          autopilotBusy: false,
          chosenAutopilot: Option.some({ from: message.from, to: message.to }),
        },
        commands: [],
      };

    case "SelectAutopilotFailed":
      return {
        model: { ...model, autopilotBusy: false, autopilotError: message.error },
        commands: [],
      };

    case "CloseWorkstreamRequested": {
      if (model.route._tag === "WorkstreamChat") {
        const { owner, name, number } = model.route;
        return {
          model: { ...model, closeBusy: true, closeError: "" },
          commands: [CloseWorkstream({ owner, name, number })],
        };
      }
      return { model, commands: [] };
    }

    case "CloseWorkstreamSucceeded":
      return {
        model: { ...model, closeBusy: false },
        commands: [],
      };

    case "CloseWorkstreamFailed":
      return {
        model: { ...model, closeBusy: false, closeError: message.error },
        commands: [],
      };

    case "SetMobileTasksOpen":
      return { model: { ...model, mobileTasksOpen: message.open }, commands: [] };

    case "ToggleMenu": {
      const isCurrentlyOpen = Option.isSome(model.openMenu) && model.openMenu.value === message.id;
      return {
        model: {
          ...model,
          openMenu: isCurrentlyOpen ? Option.none() : Option.some(message.id),
        },
        commands: [],
      };
    }

    case "PressedEscape":
      if (Option.isSome(model.openMenu)) {
        return update(model, Message.CloseMenu());
      }
      if (model.upgrade.changesShown) {
        return update(model, Message.HideUpgradeChanges());
      }
      if (Option.isSome(model.heldSheet)) {
        return update(model, Message.CloseHeldSheet());
      }
      if (model.loginShown) {
        return update(model, Message.HideLogin());
      }
      if (model.mobileTasksOpen) {
        return update(model, Message.SetMobileTasksOpen({ open: false }));
      }
      return { model, commands: [] };

    case "CloseMenu":
      return { model: { ...model, openMenu: Option.none() }, commands: [] };

    // Submodel event forwarders
    case "ForwardWorkstreamsEvent": {
      let m = model;
      const cmds: Cmd<Message>[] = [];
      if (Option.isSome(m.tasksProgram)) {
        const res = foldTasks(m, { _tag: "BoardChanged" });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      if (Option.isSome(m.panel)) {
        const res = foldPanel(m, { _tag: "WorkstreamsChanged" });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      return { model: m, commands: cmds };
    }

    case "ForwardAgentEvent": {
      let m = model;
      const cmds: Cmd<Message>[] = [];
      if (Option.isSome(m.agentsProgram)) {
        const res = foldAgents(m, { _tag: "AgentChanged" });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      if (Option.isSome(m.panel)) {
        const res = foldPanel(m, { _tag: "AgentChanged", agent: message.agent });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      return { model: m, commands: cmds };
    }

    case "ForwardRecurringEvent": {
      if (Option.isSome(model.panel)) {
        return foldPanel(model, {
          _tag: "RecurringChanged",
          repository: message.repository,
          number: message.number,
        });
      }
      return { model, commands: [] };
    }

    case "ForwardInboxEvent": {
      if (Option.isSome(model.tasksProgram)) {
        return foldTasks(model, { _tag: "BoardChanged" });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveMessage": {
      if (Option.isSome(model.chat) && sameChat(message.message, model.chat.value)) {
        return foldChat(model, { _tag: "ReceivedLiveMessage", message: message.message });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveChatState": {
      if (Option.isSome(model.chat) && sameChat(message.state, model.chat.value)) {
        return foldChat(model, { _tag: "ReceivedLiveChatState", state: message.state });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveAction": {
      if (Option.isSome(model.chat)) {
        return foldChat(model, { _tag: "ReceivedLiveAction", action: message.action });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveQuestion": {
      if (Option.isSome(model.chat)) {
        return foldChat(model, { _tag: "ReceivedLiveQuestion", question: message.question });
      }
      return { model, commands: [] };
    }

    // Submodel message handlers
    case "GotChatMessage":
      return foldChat(model, message.message);

    case "GotPanelMessage": {
      const panelMessage = message.message;
      if (panelMessage._tag === "GotTasksMessage" && panelMessage.message._tag === "AttachedTask") {
        return foldPanel({ ...model, mobileTasksOpen: false }, panelMessage);
      }
      return foldPanel(model, panelMessage);
    }

    case "GotInboxMessage":
      return foldInbox(model, message.message);

    case "GotWorkstreamsMessage":
      return foldWorkstreams(model, message.message);

    case "GotTasksMessage":
      return foldTasks(model, message.message);

    case "GotAgentsMessage":
      return foldAgents(model, message.message);

    case "GotCheckupMessage": {
      if (message.message._tag === "WorkstreamRequested") {
        const org = message.message.organization;
        const { model: m1, commands: c1 } = foldCheckup(model, message.message);
        const { model: m2, commands: c2 } = update(
          m1,
          Message.SelectOrganization({ organization: org }),
        );
        return {
          model: m2,
          commands: [...(c1 ?? []), ...(c2 ?? []), Navigate({ path: "/chat" })],
        };
      }
      if (message.message._tag === "ClickedBack") {
        return update(model, backRequested(model));
      }
      return foldCheckup(model, message.message);
    }

    case "GotUsageMessage":
      return foldUsage(model, message.message);

    case "GotSettingsMessage":
      return foldSettings(model, message.message);

    case "GotRolesMessage": {
      if (message.message._tag === "ClickedBack") {
        return update(model, backRequested(model));
      }
      return foldRoles(model, message.message);
    }

    case "GotMemoryMessage": {
      if (message.message._tag === "ClickedBack") {
        return update(model, backRequested(model));
      }
      return foldMemory(model, message.message);
    }

    case "GotMcpServersMessage": {
      if (message.message._tag === "ClickedBack") {
        return update(model, backRequested(model));
      }
      return foldMcpServers(model, message.message);
    }

    case "GotSwitchesMessage": {
      if (message.message._tag === "ClickedSwitch") {
        return update(
          model,
          Message.ToggleSwitchRequested({
            kind: message.message.kind,
            on: message.message.on,
          }),
        );
      }
      if (message.message._tag === "ClickedBack") {
        return update(model, backRequested(model));
      }
      return foldSwitches(model, message.message);
    }

    case "GotVoiceMessage": {
      if (message.message._tag === "ClickedBack") {
        return update(model, backRequested(model));
      }
      return foldVoice(model, message.message);
    }

    case "GotDevicesMessage":
      return foldDevices(model, message.message);

    case "GotGitHubMessage":
      return foldGitHub(model, message.message);

    default:
      return { model, commands: [] };
  }
}
