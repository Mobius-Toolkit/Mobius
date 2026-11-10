import * as Port from "foldkit/port";
import { loginRequested } from "../settings/ports";
import { Search } from "./model";

// searchChanged gives the new search of the page. The host writes it in the address of the page.
export const searchChanged = Port.outbound(Search);

// search gives each new search of the route to the program.
export const search = Port.inbound(Search);

export const ports = {
  inbound: { search },
  outbound: { loginRequested, searchChanged },
} as const;

export type UsagePorts = typeof ports;
