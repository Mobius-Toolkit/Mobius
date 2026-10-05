import { ChevronDownIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

// counts has the number of Inbox items and unread chat messages of each organization.
export function OrganizationSwitch({
  organizations,
  organization,
  counts,
  onSelect,
}: {
  organizations: string[];
  organization: string;
  counts: Map<string, number>;
  onSelect: (organization: string) => void;
}) {
  const elsewhere = organizations.some(
    (name) => name !== organization && (counts.get(name) ?? 0) > 0,
  );
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline">
          {organization}
          {elsewhere && (
            <span
              aria-label="Work in another organization"
              className="size-2 rounded-full bg-green-600"
            />
          )}
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuLabel>Organizations</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={organization} onValueChange={onSelect}>
          {organizations.map((name) => (
            <DropdownMenuRadioItem key={name} value={name}>
              <span className="grow">{name}</span>
              {name !== organization && (counts.get(name) ?? 0) > 0 && (
                <Badge>{counts.get(name)}</Badge>
              )}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
