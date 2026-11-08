import { ChevronDownIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";

export function UiBadge() {
  return (
    <Badge variant="secondary" className="shrink-0">
      UI
    </Badge>
  );
}

export function Choice({
  id,
  value,
  values,
  loading,
  onSelect,
}: {
  id: string;
  value: string;
  values: string[];
  loading: boolean;
  onSelect: (value: string) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button id={id} variant="outline" className="min-w-0 grow justify-between">
          <span className="truncate">{value}</span>
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {loading ? (
          <div className="flex justify-center p-2">
            <Spinner />
          </div>
        ) : (
          <DropdownMenuRadioGroup value={value} onValueChange={onSelect}>
            {values.map((other) => (
              <DropdownMenuRadioItem key={other} value={other}>
                {other}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function Field({
  label,
  htmlFor,
  ui = false,
  children,
}: {
  label: string;
  htmlFor: string;
  ui?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[4rem_1fr] items-center gap-2">
      <Label htmlFor={htmlFor}>{label}</Label>
      <div className="flex min-w-0 items-center gap-2">
        {children}
        {ui && <UiBadge />}
      </div>
    </div>
  );
}
