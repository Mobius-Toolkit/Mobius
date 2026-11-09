import { type LinkProps, useRouter } from "@tanstack/react-router";
import { useCallback } from "react";

export function useGoBack(parent: LinkProps["to"]) {
  const router = useRouter();

  return useCallback(() => {
    if (router.history.canGoBack()) {
      router.history.back();
    } else {
      void router.navigate({ to: parent, replace: true });
    }
  }, [router, parent]);
}
