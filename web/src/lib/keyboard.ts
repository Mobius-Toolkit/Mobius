import { useEffect } from "react";

// The keyboard of a phone covers the bottom of the layout viewport and does not resize it. --keyboard is the height
// of the covered part.
export function useKeyboardHeight() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) {
      return;
    }
    const update = () =>
      document.documentElement.style.setProperty(
        "--keyboard",
        `${Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop)}px`,
      );
    update();
    viewport.addEventListener("resize", update);
    viewport.addEventListener("scroll", update);
    return () => {
      viewport.removeEventListener("resize", update);
      viewport.removeEventListener("scroll", update);
      document.documentElement.style.removeProperty("--keyboard");
    };
  }, []);
}
