import { Schema } from "effect";
import * as Port from "foldkit/port";

// loginRequested shows the login. navigationRequested gives a path of the host router. backRequested is the back
// button of the page: the host goes back in the history, or to the parent page when the history has no step back.
export const loginRequested = Port.outbound(Schema.Void);
export const navigationRequested = Port.outbound(Schema.String);
export const backRequested = Port.outbound(Schema.Void);
