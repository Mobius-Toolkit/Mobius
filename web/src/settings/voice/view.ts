import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { checkIcon } from "@/ui/icons";
import { inputClass } from "@/ui/input";
import {
  inset,
  renderErrorBadge,
  renderList,
  renderPageHeader,
  renderSection,
  rowClass,
} from "@/ui/page";
import type { VoiceLanguage } from "../schemas";
import { Message } from "./message";
import { allLanguages, matches, searching, suggested, type Model } from "./model";

type H = HtmlBuilder<Message>;

function renderLanguages(h: H, voice: VoiceLanguage, items: VoiceLanguage["languages"]): Html {
  return renderList(
    h,
    items.map((item) => {
      const selected = item.code === voice.language;
      return h.keyed("li")(
        item.code,
        [],
        [
          h.button(
            [
              h.Type("button"),
              h.AriaCurrent(selected ? "true" : "false"),
              h.Class(cn(rowClass, "w-full text-left hover:bg-muted")),
              h.OnClick(Message.PickedLanguage({ code: item.code })),
            ],
            [
              h.span([h.Class("min-w-0 grow break-words")], [item.name]),
              selected ? checkIcon(h, "size-4 shrink-0") : h.empty,
            ],
          ),
        ],
      );
    }),
  );
}

function renderResults(h: H, model: Model, voice: VoiceLanguage): Html[] {
  if (!searching(model)) {
    return [
      renderSection(h, "Suggested", [renderLanguages(h, voice, suggested(voice))]),
      renderSection(h, "All languages", [renderLanguages(h, voice, allLanguages(voice))]),
    ];
  }
  const found = matches(model, voice);
  return found.length > 0
    ? [renderLanguages(h, voice, found)]
    : [h.p([h.Class(cn("text-muted-foreground", inset))], ["No language has this name."])];
}

export function view(model: Model, h: H): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Voice", Message.ClickedBack()),
      h.p(
        [h.Class(cn("text-muted-foreground", inset))],
        ["The language that you speak. The next recording uses it."],
      ),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      h.div(
        [h.Class(inset)],
        [
          h.input([
            h.Type("search"),
            h.AriaLabel("Search languages"),
            h.Placeholder("Search languages"),
            h.Value(model.search),
            h.OnInput((text) => Message.SearchChanged({ text })),
            h.Class(inputClass()),
          ]),
        ],
      ),
      ...Option.match(model.voice, {
        onNone: () => [],
        onSome: (voice) => renderResults(h, model, voice),
      }),
    ],
  );
}
