import type { LinkProps } from "@tanstack/react-router";
import { ArrowLeftIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useGoBack } from "@/lib/goBack";

export function BackButton({ parent, onBack }: { parent: LinkProps["to"]; onBack?: () => void }) {
  const goBack = useGoBack(parent);

  return (
    <Button variant="ghost" size="icon" aria-label="Back" onClick={onBack ?? goBack}>
      <ArrowLeftIcon />
    </Button>
  );
}
