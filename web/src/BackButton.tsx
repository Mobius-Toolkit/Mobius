import { type LinkProps, useRouter } from "@tanstack/react-router";
import { ArrowLeftIcon } from "lucide-react";
import { Button } from "@/components/ui/button";

export function BackButton({ parent }: { parent: LinkProps["to"] }) {
  const router = useRouter();

  function back() {
    if (router.history.canGoBack()) {
      router.history.back();
    } else {
      void router.navigate({ to: parent, replace: true });
    }
  }

  return (
    <Button variant="ghost" size="icon" aria-label="Back" onClick={back}>
      <ArrowLeftIcon />
    </Button>
  );
}
