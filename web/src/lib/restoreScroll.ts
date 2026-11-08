import { useElementScrollRestoration } from "@tanstack/react-router";
import { type RefObject, useEffect } from "react";

// The router restores the scroll position when it renders a page. A Foldkit program renders in the next animation
// frame, so the page is too short at that time. The hook restores the position of a back navigation when the program
// has made the page tall enough. It stops at the first scroll of the Owner.
export function useRestoredScroll(wrapper: RefObject<HTMLElement | null>) {
  const contentTop = useElementScrollRestoration({ id: "content" })?.scrollY ?? 0;
  const pageTop = useElementScrollRestoration({ getElement: () => window })?.scrollY ?? 0;

  useEffect(() => {
    const element = wrapper.current;
    const content = document.getElementById("content");
    if (!element || !content || (contentTop === 0 && pageTop === 0)) {
      return;
    }

    const stop = () => {
      observer.disconnect();
      document.removeEventListener("scroll", stop, true);
    };
    const observer = new MutationObserver(() => {
      const contentReady = content.scrollHeight - content.clientHeight >= contentTop;
      const pageReady = document.documentElement.scrollHeight - window.innerHeight >= pageTop;
      if (!contentReady || !pageReady) {
        return;
      }
      stop();
      content.scrollTop = contentTop;
      window.scrollTo({ top: pageTop });
    });
    observer.observe(element, { childList: true, subtree: true });
    document.addEventListener("scroll", stop, true);
    return stop;
  }, [wrapper, contentTop, pageTop]);
}
