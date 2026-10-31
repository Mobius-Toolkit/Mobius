import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { List, PageHeader, Row } from "@/components/page";
import { useShell } from "@/lib/shell";
import { switchKinds } from "@/lib/switches";
import { TopBar } from "./TopBar";

export function SwitchSettings() {
  const { switches, switchErrors, changeSwitch } = useShell();

  return (
    <>
      <TopBar title="Switches" back="/settings" />
      <PageHeader title="Switches" back="/settings" />
      <List>
        {switchKinds.map(({ kind, title, description }) => (
          <Row key={kind}>
            <div className="grid min-w-0 grow gap-0.5">
              <Label htmlFor={`switch-${kind}`}>{title}</Label>
              <span className="text-sm text-muted-foreground">{description}</span>
              {/* The amber line shows the error of a switch that is off. */}
              {switches?.[kind] !== false && switchErrors[kind] && (
                <Badge
                  variant="destructive"
                  className="h-auto justify-self-start whitespace-normal"
                >
                  {switchErrors[kind]}
                </Badge>
              )}
            </div>
            <Switch
              id={`switch-${kind}`}
              checked={switches?.[kind] ?? true}
              onCheckedChange={(on) => void changeSwitch(kind, on)}
            />
          </Row>
        ))}
      </List>
    </>
  );
}
