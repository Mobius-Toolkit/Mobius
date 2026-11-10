import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { formatDay, usageRange } from "@/lib/usage";
import {
  ChangeSearch,
  FetchQuality,
  FetchSkills,
  FetchTimes,
  FetchUsage,
  RequestLogin,
} from "./commands";
import { Message } from "./message";
import { groupOf, groupsOf, rangeOf, tabOf, type Model, type Search } from "./model";

export function load(model: Model) {
  const args = {
    request: model.request,
    search: model.search,
    range: rangeOf(model),
    timeZone: model.timeZone,
  };
  switch (tabOf(model.search)) {
    case "cost":
    case "tokens":
      return FetchUsage(args);
    case "times":
      return FetchTimes(args);
    case "skills":
      return FetchSkills(args);
    case "quality":
      return FetchQuality(args);
  }
}

// A new search loads the data of the tab again. The last 30 days are the first range of the custom period.
function changed(model: Model, search: Search, closeMenu: boolean): UpdateReturn<Model, Message> {
  const next: Model = {
    ...model,
    search,
    request: model.request + 1,
    openMenu: closeMenu ? Option.none() : model.openMenu,
    hover: Option.none(),
  };
  return { model: next, commands: [ChangeSearch({ search }), load(next)] };
}

function customDays(now: number) {
  const range = usageRange({ period: "30d" }, new Date(now));
  const last = new Date(range.to.getFullYear(), range.to.getMonth(), range.to.getDate() - 1);
  return { from: formatDay(range.from), to: formatDay(last) };
}

const answered = (model: Model, request: number, change: (model: Model) => Model) =>
  request === model.request ? { model: change(model) } : { model };

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  const search = model.search;
  return Message.match<UpdateReturn<Model, Message>>(message, {
    LoadedUsage: ({ request, usage, range }) =>
      answered(model, request, (current) => ({
        ...current,
        usage: Option.some({ usage, range }),
        error: "",
      })),
    LoadedTimes: ({ request, times, range }) =>
      answered(model, request, (current) => ({
        ...current,
        times: Option.some({ times, range }),
        error: "",
      })),
    LoadedSkills: ({ request, skills }) =>
      answered(model, request, (current) => ({
        ...current,
        skills: Option.some(skills),
        error: "",
      })),
    LoadedQuality: ({ request, quality, range }) =>
      answered(model, request, (current) => ({
        ...current,
        quality: Option.some({ quality, range }),
        error: "",
      })),
    LoadFailed: ({ request, error }) =>
      answered(model, request, (current) => ({ ...current, error })),
    LoadLoginRequired: ({ request }) =>
      request === model.request ? { model, commands: [RequestLogin()] } : { model },
    SelectedTab: ({ tab }) =>
      changed(model, { ...search, tab: tab === "cost" ? undefined : tab }, true),
    SelectedPeriod: ({ period }) =>
      changed(
        model,
        period === "custom"
          ? { ...search, period, ...customDays(model.now) }
          : { ...search, period, from: undefined, to: undefined },
        true,
      ),
    ToggledFilter: ({ key, option }) => {
      const selected = search[key] ?? [];
      const next = selected.includes(option)
        ? selected.filter((value) => value !== option)
        : [...selected, option];
      return changed(model, { ...search, [key]: next.length > 0 ? next : undefined }, false);
    },
    ToggledGroup: ({ key }) => {
      const tab = tabOf(search);
      const group = groupOf(search, tab);
      const checked = !group.includes(key);
      const next = groupsOf(tab)
        .map((item) => item.key)
        .filter((other) => (other === key ? checked : group.includes(other)));
      return changed(model, { ...search, group: next.length > 0 ? next : undefined }, false);
    },
    ChangedFrom: ({ value }) =>
      value === "" ? { model } : changed(model, { ...search, from: value }, false),
    ChangedTo: ({ value }) =>
      value === "" ? { model } : changed(model, { ...search, to: value }, false),
    SelectedStat: ({ stat }) => changed(model, { ...search, stat }, true),
    SelectedValue: ({ value }) => changed(model, { ...search, value }, true),
    ToggledMenu: ({ menu }) => ({
      model: {
        ...model,
        openMenu: Option.exists(model.openMenu, (open) => open === menu)
          ? Option.none()
          : Option.some(menu),
      },
    }),
    ClosedMenu: () => ({ model: { ...model, openMenu: Option.none() } }),
    OpenedGroupSheet: () => ({ model: { ...model, groupSheetOpen: true } }),
    ClosedGroupSheet: () => ({ model: { ...model, groupSheetOpen: false } }),
    PressedEscape: () =>
      Option.isSome(model.openMenu)
        ? { model: { ...model, openMenu: Option.none() } }
        : { model: { ...model, groupSheetOpen: false } },
    ResizedChart: ({ width, height }) => ({ model: { ...model, chart: { width, height } } }),
    HoveredChart: ({ x, y }) => ({ model: { ...model, hover: Option.some({ x, y }) } }),
    LeftChart: () => ({ model: { ...model, hover: Option.none() } }),
    CompletedRequestLogin: () => ({ model }),
    CompletedChangeSearch: () => ({ model }),
    CompletedFocusSheet: () => ({ model }),
  });
}
