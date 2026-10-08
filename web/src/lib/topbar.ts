import { createContext } from "react";

// TopBarContext gives the element of the top bar of a phone. It is null until the element exists.
export const TopBarContext = createContext<HTMLElement | null>(null);
