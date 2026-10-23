import {
  ChevronDownIcon,
  PlaneIcon,
  PlaneLandingIcon,
  PlaneTakeoffIcon,
  PowerOffIcon,
} from "lucide-react";
import { useState } from "react";
import type { WorkstreamAutopilot } from "@/api/api.gen";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { levelName, levels } from "@/lib/autopilot";
import { cn } from "@/lib/utils";

const icons = {
  off: PowerOffIcon,
  start: PlaneTakeoffIcon,
  merge: PlaneLandingIcon,
  start_and_merge: PlaneIcon,
};

export function AutopilotIcon({ level }: { level: WorkstreamAutopilot }) {
  const Icon = icons[level];
  return <Icon className="size-3.5 text-muted-foreground" />;
}

function Trigger({
  level,
  compact,
  className,
  ...props
}: {
  level: WorkstreamAutopilot;
  compact?: boolean;
} & React.ComponentProps<typeof Button>) {
  const name = `Autopilot: ${levelName(level)}`;
  return (
    <Button
      variant="outline"
      size="sm"
      className={cn("ml-auto shrink-0", className)}
      aria-label={name}
      title={compact ? name : undefined}
      {...props}
    >
      <AutopilotIcon level={level} />
      {!compact && levelName(level)}
      <ChevronDownIcon />
    </Button>
  );
}

function Level({ value, name, text }: { value: WorkstreamAutopilot; name: string; text: string }) {
  return (
    <span className="flex min-w-0 gap-2">
      <span className="mt-[3px] shrink-0">
        <AutopilotIcon level={value} />
      </span>
      <span className="flex min-w-0 flex-col">
        <span>{name}</span>
        <span className="text-xs text-muted-foreground">{text}</span>
      </span>
    </span>
  );
}

export function AutopilotMenu({
  level,
  disabled,
  onSelect,
}: {
  level: WorkstreamAutopilot;
  disabled: boolean;
  onSelect: (level: WorkstreamAutopilot) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Trigger level={level} disabled={disabled} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuRadioGroup
          value={level}
          onValueChange={(value) => onSelect(value as WorkstreamAutopilot)}
        >
          {levels.map((other) => (
            <DropdownMenuRadioItem key={other.value} value={other.value} className="items-start">
              <Level value={other.value} name={other.name} text={other.text} />
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function AutopilotSheet({
  level,
  disabled,
  onSelect,
}: {
  level: WorkstreamAutopilot;
  disabled: boolean;
  onSelect: (level: WorkstreamAutopilot) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Trigger level={level} compact disabled={disabled} />
      </SheetTrigger>
      <SheetContent side="bottom" aria-describedby={undefined} className="gap-2 p-4 pt-3">
        <SheetTitle>Autopilot</SheetTitle>
        <div role="radiogroup" aria-label="Autopilot" className="flex flex-col">
          {levels.map((other) => (
            <button
              key={other.value}
              type="button"
              role="radio"
              aria-checked={other.value === level}
              className={cn(
                "flex items-start gap-2 rounded-md px-2 py-2 text-left hover:bg-accent",
                other.value === level && "bg-accent",
              )}
              onClick={() => {
                setOpen(false);
                onSelect(other.value);
              }}
            >
              <Level value={other.value} name={other.name} text={other.text} />
            </button>
          ))}
        </div>
      </SheetContent>
    </Sheet>
  );
}
