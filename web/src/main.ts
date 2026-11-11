import "./index.css";
import type { UrlRequest } from "foldkit/navigation";
import * as Runtime from "foldkit/runtime";
import type { Url } from "foldkit/url";
import { Message } from "./app/message";
import { Model } from "./app/model";
import { subscriptions } from "./app/subscriptions";
import { init, update } from "./app/update";
import { view } from "./app/view";

const dark = window.matchMedia("(prefers-color-scheme: dark)");
const applyTheme = () => document.documentElement.classList.toggle("dark", dark.matches);
applyTheme();
dark.addEventListener("change", applyTheme);

const container = document.getElementById("root");

const app = Runtime.makeApplication({
  Model,
  init,
  update,
  view,
  subscriptions,
  routing: {
    onUrlRequest: (request: UrlRequest) => Message.UrlRequested({ request }),
    onUrlChange: (url: Url) => Message.UrlChanged({ url }),
  },
  container,
});

Runtime.run(app);
