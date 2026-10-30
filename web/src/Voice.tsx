import { CheckIcon } from "lucide-react";
import { useState } from "react";
import type { VoiceLanguageItem } from "@/api/api.gen";
import { ErrorBadge, inset, List, PageHeader, rowClass, Section } from "@/components/page";
import { Input } from "@/components/ui/input";
import { useVoiceLanguage } from "@/lib/voiceLanguage";
import { cn } from "@/lib/utils";
import { TopBar } from "./TopBar";

function LanguageRow({
  item,
  selected,
  onSelect,
}: {
  item: VoiceLanguageItem;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        aria-current={selected}
        className={cn(rowClass, "w-full text-left hover:bg-muted")}
        onClick={onSelect}
      >
        <span className="min-w-0 grow break-words">{item.name}</span>
        {selected && <CheckIcon className="size-4 shrink-0" />}
      </button>
    </li>
  );
}

export function Voice() {
  const { voice, error, select } = useVoiceLanguage();
  const [search, setSearch] = useState("");

  const languages = voice?.languages ?? [];
  const text = search.trim().toLowerCase();

  const rows = (items: VoiceLanguageItem[]) => (
    <List>
      {items.map((item) => (
        <LanguageRow
          key={item.code}
          item={item}
          selected={item.code === voice?.language}
          onSelect={() => {
            if (item.code !== voice?.language) {
              void select(item.code);
            }
          }}
        />
      ))}
    </List>
  );

  const matches = languages.filter(
    (item) => item.name.toLowerCase().includes(text) || item.code.toLowerCase().includes(text),
  );
  const suggested = [...new Set([voice?.language, "en", "auto"])].flatMap((code) =>
    languages.filter((item) => item.code === code),
  );

  return (
    <>
      <TopBar title="Voice" back="/settings" />
      <PageHeader title="Voice" back="/settings" />
      <p className={cn("text-muted-foreground", inset)}>
        The language that you speak. The next recording uses it.
      </p>
      {error && <ErrorBadge>{error}</ErrorBadge>}
      <div className={inset}>
        <Input
          type="search"
          aria-label="Search languages"
          placeholder="Search languages"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
      </div>
      {voice && text === "" && (
        <>
          <Section title="Suggested">{rows(suggested)}</Section>
          <Section title="All languages">
            {rows(languages.filter((item) => item.code !== "auto"))}
          </Section>
        </>
      )}
      {voice && text !== "" && matches.length > 0 && rows(matches)}
      {voice && text !== "" && matches.length === 0 && (
        <p className={cn("text-muted-foreground", inset)}>No language has this name.</p>
      )}
    </>
  );
}
