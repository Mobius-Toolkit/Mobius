# Foldkit proof: the chat screen

This document reports a proof of Foldkit on the chat screen of Mobius. The Owner uses it to decide about the move of `web/` to Foldkit.

- Tested versions: `foldkit` 0.170.0, `@foldkit/ui` 0.170.0, `@foldkit/markdown` 0.15.0, `@foldkit/vite-plugin` 0.29.0, `@foldkit/oxlint-plugin` 0.15.4, `effect` 4.1.2, `@effect/platform-browser` 4.1.2.
- Screen: `Conversation` (`/chat` and `/workstreams/$owner/$name/$number`). It runs as one Foldkit program in `web/src/chat/`. `web/src/Conversation.tsx` is a React host that calls `Runtime.embed`.
- Base commit for all comparisons: `7c8beea`.

## Result in short

- The chat screen runs in Foldkit on `/chat` and on `/workstreams/$owner/$name/$number`, inside a React host. The program covers the message list, the live events, the send queue with the retry waits, the chat input with the voice button and the pictures, the scroll rules, the Brief bubble, the tray of pending actions, the question and action cards, and the answer panel.
- The Model, the Messages, the Ports and the Commands have real Schemas, and the chat code has no type cast, no `!`, no `any` and no `unknown` type. orval (now `8.47.0`) writes the Effect Schemas of the API to `web/src/api/schemas.gen.ts`. Each Promise call runs in `Effect.tryPromise` with a tagged error, and the live events, the drafts and the queue records are decoded with a Schema. The real Schemas made the program reject the incomplete fixtures of the e2e specs, so the fixtures have all fields now (section 6). The gzip size of the JavaScript of `/chat` is 516.4 kB (it was 499.9 kB before the Schemas). At commit `4f518e94`, 407 Playwright tests of the 12 spec files of the Done list passed, 293 tests of `screenshots` and the 9 other spec files except `load-time` passed, and 63 Vitest tests pass. See sections 3, 5 and 7.
- 459 Playwright tests ran at commit `c20807bd`: 458 passed and 1 flaky (a Tasks tab test, React code, which passed on the retry). The last later change (the end of the error record of a panel, in `update.ts`) was tested only with Vitest: 63 Vitest tests pass at the head, and the Playwright run was not repeated. The CI `e2e` job of `9f5c9cc1` failed in `screenshots.spec.ts` because of a Foldkit cause, fixed in `c20807bd` (see "Tests"). Commits after it change only this file. The spec files of the Done list ran: `chat`, `attachments`, `voice`, `voice-language`, `actions`, `answered`, `pending`, `live`, `notice`, `navigation`, `inbox` and `screenshots`. See "Tests" for the flaky tests.
- The CI comparison of the screenshots listed 80 changed images for the code of `481d6eff` (the run of `13f5f478`, image commit `1c935ae1`). Each one differed only at the heading of the tray, and commit `9b56ed24` fixed the cause. The run of `b98e19de` (the code of `9b56ed24`) shows "No screen changed". The CI workflow `CI` passed at `b98e19de`. See "Screenshots" for each image.
- The move cost much more work than the line counts show. The first session of the port stopped at the point where about 50 specs failed, and the next session needed about 15 rounds of fixes, with a full test run of 5 to 10 minutes after each round, to get all of them to pass. The causes were not limits of Foldkit. They were behavior that the first port left out: the retry waits, the pictures, the voice text, the scroll rules and the answer panel. Plan for this cost.
- Foldkit gave no blocker. It has three limits that cost time: the Markdown parser throws on text such as `09:30`, the attributes `OnPaste` and `OnKeyDown` give too little data for the chat input, and the view renders in the next frame, so a test that reads the DOM right after an input event can see the old state. Each one has a workaround. See "Problems".
- The cost on the wire is large because the Mobius server sends the files with no compression: +52 % raw JavaScript and +46 % load time on the emulated phone. See "Size and load time".

## 1. Live events

The program opens its own `EventSource` of `/api/events`. It does not read the events of `App.tsx`.

- `web/src/chat/subscriptions.ts` has the Subscription `liveEvents`. Its dependencies are `organization`, `repository` and `workstream`. `dependenciesToStream` returns a `Stream.callback`.
- The Stream creates the `EventSource`, the timers and the window listeners in its setup. It registers one finalizer with `Effect.addFinalizer`. The finalizer closes the source, clears the timers and removes the listeners. Foldkit runs the finalizer when the dependencies change or when the program stops.
- Each event goes to the Model as a Message through `Queue.offerUnsafe`: `ReceivedLiveMessage`, `ReceivedLiveChatState`, `ReceivedLiveAction` and `ReceivedLiveQuestion`. The typed listener `onEvent` of `web/src/lib/events.ts` is the same as in React, so a wrong event name is a type error.
- The reconnect rules of `App.tsx` are in the Stream, with the same values:

  | Case of `App.tsx` | Foldkit code |
  |---|---|
  | No ping in `silenceLimit` (35 s) | `resetSilenceWatchdog` calls `reconnect`, which closes the source and calls `connect`. |
  | The source is `CLOSED` | The `error` listener starts a timer of `retryDelay`. `handleRetry` calls `listGitHubApps`. A 401 gives the Message `PageFailed("login required")`, and the update runs the Command `RequestLogin`. Else the Stream reconnects. |
  | A new connection while a retry waits | `reconnect` clears the retry timer, as the one timer of `App.tsx` does. |
  | `online` | `handleWake` calls `reconnect` when the page is visible. |
  | `pageshow` with `persisted` | `handleRestore` calls `reconnect`. |
  | `visibilitychange` | `handleWake` calls `reconnect` when the page is visible. |

- After each new connection, the `open` event gives the Message `ConnectionOpened`. The update runs the Command `FetchPage` with the id of the newest known message. The Command reads pages until it reaches that id, so a message that came while the connection was down is not lost. This is the `load` function of the React code.
- Proof by test: `web/e2e/live.spec.ts`, the test "the chat screen receives a live message after the live connection closes". The test answers each request of `/api/events` with a 502. That closes the source for good (`readyState` is `CLOSED`), so only the retry timer and the check of `/api/github/apps` can reconnect. The test sends a message from a second page, then lets the requests pass, and the first page must show the message after the retry (the load after `ConnectionOpened`). Then the second page sends one more message, and the first page must show it with no `/api/chat` read. That proves the `message` listener of the new source. The `online`, `pageshow` and `visibilitychange` cases and the silence timer have the same `reconnect` function, but the proof has no test for them. The `online` event starts the test. The test adds two Owner messages to the Lead chat of `plants/garden#12`, which no later spec reads (the comment in `web/playwright.config.ts` says it).
- A difference from React: `App.tsx` keeps its own `EventSource` for the other pages. Thus an open chat has two connections to `/api/events`. The move of the whole client removes the second one.

## 2. Replacements

### The 18 components of `web/src/components/ui`

The right column comes from the exports of `@foldkit/ui` 0.170.0 and from its documentation. This proof did not use `@foldkit/ui` in code. The chat screen uses plain markup with the same Tailwind classes. The classes of `button` and `badge` are in framework-free files (`button-variants.ts` and `badge-variants.ts`) that React and Foldkit both import.

| Component | Replacement |
|---|---|
| `badge.tsx` | Plain `span` with `badgeVariants` (`class-variance-authority` works without React). Used in this proof. |
| `button.tsx` | Plain `button` or `a` with `buttonVariants`. Used in this proof. `@foldkit/ui/button` is available for a button with a state. |
| `card.tsx` | Plain markup. |
| `chart.tsx` | See the recommendation for `recharts` below. |
| `collapsible.tsx` | `@foldkit/ui/disclosure`. This proof used a flag in the Model and a button (`EventToggled`, `TrayToggled`). |
| `dialog.tsx` | `@foldkit/ui/dialog`. It uses the native `dialog` element. This proof wrote the markup by hand and put the modal behavior in the Mount `PanelOpened` (the focus goes into the dialog, Tab stays in it, the page does not scroll, the focus returns to the opener, `--keyboard` follows the keyboard of a phone, `aria-modal`). It does not mark the rest of the page as `inert` or `aria-hidden`, as Radix does. |
| `dropdown-menu.tsx` | `@foldkit/ui/menu`. |
| `field.tsx` | `@foldkit/ui/fieldset` and plain markup. |
| `input.tsx` | `@foldkit/ui/input`. |
| `item.tsx` | Plain markup. |
| `label.tsx` | Plain `label`. |
| `notice.tsx` | Plain markup. |
| `separator.tsx` | Plain `div` or `hr`. |
| `sheet.tsx` | `@foldkit/ui/dialog` with the classes of a side sheet. Foldkit has no drawer component. |
| `spinner.tsx` | Inline SVG (`spinnerIcon` in `web/src/chat/icons.ts`). Used in this proof. |
| `switch.tsx` | `@foldkit/ui/switch`. |
| `tabs.tsx` | `@foldkit/ui/tabs`. |
| `textarea.tsx` | Plain `textarea` with `textareaClass`. Used in this proof. `@foldkit/ui/textarea` is available. |

### `lucide-react`

The chat screen uses 15 icons. A first version drew them by hand, and 10 pixels of 5 of the icons differed from the React icons. `web/src/chat/icons.ts` now holds the icon data of `lucide-react`, which a one-off script copied, and one small function draws all icons. The app uses 25 icons. For the full client, generate the data from the package `lucide` (it has no React code) in the same way. The `lucide-react` chunk of the base build in section 5 is 7.1 kB gzip, so the size is not a reason to keep `lucide-react`.

### `recharts`

Recommendation: draw the two charts of `Usage.tsx` as plain SVG in the Foldkit view, and keep the tooltip and the legend in the Model. Reasons:

- `recharts` needs React, so it cannot stay. Its gzip size is 105.2 kB (the chunk of `recharts`, `d3-*`, `redux` and `immer` in the base build of section 5). This is 32 % of the 330.1 kB of the base.
- ECharts (the library of the Foldkit Charting example) is larger: 213 kB gzip for the bar chart, the line chart, the grid, the legend, the tooltip and the SVG renderer. It also draws its own text and tooltip, so it does not give the same look.
- uPlot is small (25 kB gzip), but it draws on a canvas and has no legend and no tooltip in the DOM. It does not give the same look.
- Recharts creates SVG with a grid, two axes, bars or lines, an HTML tooltip and an HTML legend. All of these use CSS variables of the theme. Plain SVG with the same classes can give the same look. The proof did not build the charts, so this is an estimate from the code of `Usage.tsx` and not a test.

## 3. API client

`web/src/api/api.gen.ts` stays. The Commands call its fetch functions (`getChat`, `answerInboxItem`, `approveAction`) in `Effect.tryPromise`, and they keep its types for the React screens. `web/orval.config.ts` changed in three ways:

- A second output, `schemas`, has `client: "effect"` and writes `web/src/api/schemas.gen.ts`. It has the same input as the output of `api.gen.ts`, with the same transform (the transform is now one object that both outputs share).
- `orval` is now `8.47.0`. Version `8.39.0` writes only Effect 3 syntax. The new version also writes the comments of `api.gen.ts` in another layout, and it adds nothing else to that file. pnpm 12 added `minimumReleaseAgeExclude` for the orval packages to `web/pnpm-workspace.yaml`.
- `web/.oxfmtrc.json` and `web/.oxlintrc.json` ignore `schemas.gen.ts`. The file has escapes in its descriptions (`no-useless-escape`), and no rule is turned off for another file.

What the generator gives:

- It writes one Schema for each part of each operation (`GetChatResponse`, `AnswerInboxItemBody`, `GetChatQueryParams`, ...). It does not write a Schema for a component, so there is no exported `ChatMessage` or `InboxItem`: each component is inline in the responses that use it. `web/src/chat/schemas.ts` takes each component by its path, for example `GetChatResponse.fields.data.fields.messages.value` for `ChatMessage`, `ListInboxResponse.fields.data.value` for `InboxItem`, `ListWorkstreamsResponse.fields.data.value` for `Workstream`. A change of the API changes these Schemas with `make generate`, and a wrong path is a type error.
- An operation with only an event stream response is removed by the transform, so the generator writes no Schema for the live events. `ChatState` is written by hand in `schemas.ts`, and `ChatMessage`, `PendingAction` and `InboxItem` are the same components that the other responses give.
- A time is a `String` with a pattern check, an enum is `Literals`, a nullable field is `NullOr`. The Schemas have a `Type` with `readonly` arrays, and the generated TypeScript types of `api.gen.ts` have mutable arrays. A readonly array does not go into a parameter of a mutable array, so the chat code uses the Schema types, and one helper of the React side (`actionState` in `web/src/lib/actions.ts`) takes a `Pick` of the type.
- The local types are Schemas in `schemas.ts`: the draft, the queued message, the panel, the voice region, the failure of an attempt. A `File` is `Schema.instanceOf(File)`. A list that goes to a function of the React side (`web/src/lib/draft.ts`, `outbox.ts`) uses `Schema.mutable(Schema.Array(...))`, so its type has a mutable array.

Where Foldkit decodes data with a Schema at run time, and where it does not (read from `node_modules/foldkit/dist` and tested with small probes):

- It decodes the value of an inbound Port (`runtime/hostConnector.js`, `Schema.decodeUnknownExit`). A value that fails goes to `console.error` (`Inbound port "trayItems" rejected a value: ... Missing key`), and the program does not get it. This found the incomplete fixtures of the e2e specs (see section 6): the tray stayed empty.
- It decodes the Model that a hot reload keeps (`runtime/modelPreservationBridge.js`).
- A Message constructor (`Message.OpenAnswerPanel({ item })`) does not decode a value in general. A probe with `unread: "x"` for a `NullOr(Number)` did not throw, and a probe with an item that had no keys threw a `SchemaError`. So a Message is checked only in part. It is not decoded at run time as a whole. The Model after `update` is not checked, and a result of a Command is not decoded.
- The generated fetch client does not decode the body of a response. It parses the JSON and casts it. The chat code does not decode these responses either: the Message that carries a page or an item gets the typed value of the client.
- The chat code decodes three kinds of data that no Foldkit part decodes: the data of each live event (`Schema.fromJsonString` with the Schema of the event, in `web/src/chat/subscriptions.ts`), each draft record and each record of the send queue that comes from the browser store (`readDraft` and `ListQueued` in `web/src/chat/commands.ts`). An event or a draft that fails is dropped with a log line (events) or replaced by an empty draft (drafts), and a queue that fails shows the error of `QueueFailed`.

The real Schemas found two defects that the casts hid. The e2e fixtures of the Inbox items, of the actions and of the chat messages had only some of the fields of the API, and the program rejected them. And the refusal of a queued message (`MessageRefused`) restored the Inbox items of the draft with no `id`, because the cast hid that the queue holds `ChatInboxItem` and not `DraftInboxItem`. The Command `EnqueueMessage` now keeps the draft items and sends the stored form without the `id`.

## 4. Voice, outbox and service worker

- Voice: `web/src/lib/voice.ts` is a module with its own store and no React in its core. It stays. The Foldkit side has three parts.
  - A Subscription (`createVoiceStream`) reads the store: it sends the phase and the error as Messages, and it registers an input with `registerVoiceInput`. The input callbacks put the live text and the final text in the field. They keep the text around the voice text (`region`) in the Stream, so an edit of the Owner ends the region. They send `VoiceTextPlaced` to the Model.
  - Commands call the actions of the store: `ToggleVoice`, `StopVoice`, `ResendVoice`, `DiscardVoice`, `FinishVoiceForSend`, `ProbeVoice`.
  - The Send button first runs `ProbeVoice`. If a live text shows in the field, the Model waits for `FinishVoiceForSend`. Else it stops the voice and sends at once.
- Outbox and draft: `lib/outbox.ts` and `lib/draft.ts` stay. Commands call them: `EnqueueMessage`, `PostQueued`, `RemoveQueued`, `ListQueued`, `LoadDraft`, `SaveDraft`. The retry waits are a Command: `PostQueued` gives `SendAttemptFailed`, the update runs `WaitToRetry`, and `WaitToRetry` gives `RetryDue` after the wait. A counter (`retrySequence`) in the Model ignores a wait that "Retry now" already replaced. This replaces the loop with `setTimeout` of the React code, and the Story tests prove it.
- The draft changes by React code (for example the Chat button of the Inbox screen adds an Inbox item) are not read while the program runs. The program reads the draft one time at its start. A full move has no second writer, so the problem goes away.
- Service worker: `web/public/sw.js` has only a pass-through `fetch` handler, and nothing in the chat screen uses it. The proof did not change it. In a full client, one Command at the start registers it, and a Subscription on `navigator.serviceWorker` gives the Messages for an update.

## 5. Size and load time

Measured on this machine (macOS), one run for each commit, as the issue asks. The branch numbers of the first three rows are from the run at commit `c20807bd`. The two rows with the Schemas come from a build only: the issue forbids a new run of the load-time spec, so the median is the one of `c20807bd`. The Schemas and the Effect code added 16.5 kB gzip: `index` grew by 15.0 kB (the generated Schemas that the chat code imports, with their descriptions and patterns, and the code of the tagged errors and of the Commands), and `foldkit-effect` grew by 1.4 kB (more of the Effect `Schema` module). The load time follows the raw size on the emulated network, so expect it to be slightly higher than the median above. A Chromium emulation is not a real phone: it throttles the CPU by a factor of 4 and the network to the Slow 4G values of Chrome DevTools (1.6 Mbit/s down, 750 kbit/s up, 150 ms latency), but the CPU and the memory of a real phone differ.

| | Base `7c8beea` | Branch | Change |
|---|---|---|---|
| JavaScript that `/chat` loads, gzip | 330.1 kB (one chunk) | 499.9 kB (`index` 355.1 kB, `foldkit-effect` 144.4 kB, runtime 0.4 kB) | +169.8 kB (+51 %) |
| JavaScript that `/chat` loads, raw | 1,082.7 kB | 1,648.6 kB | +565.9 kB (+52 %) |
| CSS, gzip | 15.1 kB | 15.3 kB | +0.2 kB |
| JavaScript that `/chat` loads, gzip, after the Schemas and the typed Effect code (build of commit `4f518e94`) | 330.1 kB | 516.4 kB (`index` 370.1 kB, `foldkit-effect` 145.9 kB, runtime 0.4 kB) | +186.3 kB (+56 %) |
| JavaScript that `/chat` loads, raw, after the Schemas (commit `4f518e94`) | 1,082.7 kB | 1,775.3 kB | +692.6 kB (+64 %) |
| Median of 5 loads until the chat input is editable (`web/e2e/load-time.spec.ts`) | 6,537 ms | 9,559 ms | +3,022 ms (+46 %) |

Notes:

- Foldkit and Effect are 144.4 kB gzip in their own chunk (`foldkit-effect`, from `manualChunks` in `web/vite.config.ts`). The Foldkit part of the app (`web/src/chat/`) is in the main chunk.
- The Mobius server sends the files with no compression (`web/embed.go` has no gzip code). Thus the emulated network carries the raw bytes, and the load time follows the raw size (+52 % bytes, +46 % time). Compression on the server would remove most of the cost. The proof did not add it, because the issue forbids a change of the Go code.
- Estimate of a client with no React. The inputs come from two real builds of this branch and of the base commit, with `manualChunks` that put each library group in its own chunk (a temporary Vite config, not in the branch). The gzip sizes of the chunks:

  | Chunk | Base `7c8beea` | Branch |
  |---|---|---|
  | `react` (`react`, `react-dom`, `scheduler`) | 65.1 kB | 65.1 kB |
  | `router` (`@tanstack/*`) | 25.8 kB | 25.8 kB |
  | `radix` (`radix-ui` and its helpers) | 33.0 kB | 32.4 kB |
  | `recharts` (and `d3-*`, `redux`, `immer`) | 105.2 kB | 105.2 kB |
  | `markdown-to-jsx` | 28.3 kB | 28.3 kB |
  | `lucide-react` | 7.1 kB | 6.7 kB |
  | `foldkit-effect` (Foldkit, Effect, `@foldkit/markdown` and its parser) | none | 144.4 kB |
  | `index` (app code and the small shared libraries) | 66.2 kB | 91.1 kB |
  | Sum | 331.1 kB | 499.5 kB |

  The sums are about 1 kB above the numbers of a normal build, because each chunk compresses on its own.

  Derivation, in gzip kB:

  1. The base has 264.5 kB in the libraries that need React (`react`, `router`, `radix`, `recharts`, `markdown-to-jsx`, `lucide`). A client with no React removes all of them: 331.1 − 264.5 = 66.6 kB stay. This is the app code (written in React) and the shared libraries, and it is the `index` chunk of the base.
  2. The Foldkit version of the chat screen made `index` grow by 24.9 kB (91.1 − 66.2). Most of the React code of that screen stays in `index`, because the Inbox screen imports `AnswerPanel`, which imports `MessageInput`, `ActionCard` and `Markdown`, and `MessageInput` imports `InboxItemCard` and `FileImage`. About 1,030 of the 2,384 lines left: `Conversation.tsx` (977 lines to the 107 lines of the host), `ActionTray.tsx` and `QuestionCard.tsx` (no file imports the last two now). The base `index` is 66.2 kB for the 12,600 lines of the client, so about 5.25 bytes a line, and the lines that left are about 5.4 kB. Thus the Foldkit code of the screen is about 24.9 + 5.4 = 30.3 kB, against about 2,384 × 5.25 = 12.5 kB for the React code of the screen. The ratio is about 2.4 (the line ratio of section 7 is 2.3). This ratio is from one screen only. The estimate below was not computed again after the Schemas. The Schemas add about 15 kB to the app code of the chat screen and 1.4 kB to the Foldkit chunk, so add up to 16 kB to its Foldkit ranges.
  3. Foldkit and Effect add 144.4 kB. This number covers the chat screen. The router, `@foldkit/ui` and other Foldkit modules that the proof did not import add more. The size is not measured, and 10 to 30 kB is a guess.
  4. The charts: plain SVG adds a few kB (a guess). uPlot adds 25 kB, and ECharts adds 213 kB (both measured in standalone builds).

  Result with plain SVG charts: from 66.6 (the app code does not grow) + 144.4 + 5 = **216 kB** to 66.6 × 2.4 + 144.4 + 30 + 10 = **344 kB** (the app code grows 2.4 times, as the chat screen did, and Foldkit modules add 30 kB and a guess adds 10 kB). With ECharts, add 213 kB: **429 to 557 kB**. With uPlot, add 25 kB: **241 to 369 kB**. The base is 331 kB. Thus the range with plain SVG charts contains the base: a client with no React is smaller than the base only if the code per screen grows less than about 2.2 times (66.6 × 2.2 + 144.4 + 40 = 331), and the chat screen grew 2.4 times. With ECharts, it is larger than the base in all cases. The estimate has a large error: it uses one screen to give the growth of the app code.
- The size of the Foldkit chunk is fixed (Effect), and the size of the app code per screen is small. Thus the first screen costs the most, and the later screens cost little.

## 6. Tests

### Playwright

Last run, at commit `6d46caf8` (the fix of the scroll, after the merge of `main`): `pnpm e2e` with the 12 spec files of the Done list and `--retries=0`: 410 passed, 0 failed. Earlier runs, at commit `4f518e94` (the Schemas and the typed Effect code): `pnpm e2e` with the 12 spec files of the Done list (`chat`, `attachments`, `voice`, `voice-language`, `actions`, `answered`, `pending`, `live`, `notice`, `navigation`, `inbox`, `screenshots`) and `--retries=0`: 407 passed, 0 failed. `pnpm e2e` with `screenshots` and the 9 other spec files except `load-time` (`agents`, `checkup`, `installations`, `memory`, `roles`, `settings`, `switches`, `tasks`, `usage`) and `--retries=2`: 293 passed, none flaky or failed. The `load-time` spec did not run again, as the issue asks. 63 Vitest tests passed at that commit (64 at the head, with one new Story test of the last round). Command of the earlier runs: `pnpm e2e --retries=2` in `web` (all 459 tests of 22 spec files). The last full local run before the Schemas, at commit `c20807bd`: 458 passed, 0 failed, 1 flaky ("Resume gives a needs-human task ...", second in the list below, which passed on the retry). The runs before it: at `754de151` 458 passed, 1 flaky (a Tasks tab test); at `6f197e36` 459 passed, 0 flaky; at `9b56ed24` 459 passed, 0 flaky; at `481d6eff` 459 passed, 0 flaky; at `20d5bb40` 459 passed, 0 flaky; at `9fa7bc86` 459 passed, 0 flaky; at `133fc9f0` 458 passed, 1 flaky (a Tasks tab test, first in the list below, which passed on the retry). These tests failed sometimes in the runs of this work. The first three have no Foldkit cause that the proof found, and each passed in the next run. The CI failure at the end of the list has a Foldkit cause:

- `chat.spec.ts` "the Tasks tab shows owner/name#N and the Start button ..." (error `route.fetch: Test ended`), and "Resume gives a needs-human task ..." (error `Response has been disposed`). Both belong to the Tasks tab (React code). A route callback of one test is still in flight when the test ends, and the error shows in the next test. They failed in 3 of the 10 full runs. The proof did not change the Tasks tab.
- `chat.spec.ts` "two taps on Send in one turn send one message ...". It failed in 2 runs before the Send button got `type="submit"` in all states (see Problems, item 4), and it passed in the 5 runs after the change.
- `screenshots.spec.ts` (the step `chat-voice-send-wait`): the field had the text two times. A `fill("")` of the step before it did not wait for the draft in the browser store, so the next page loaded the old draft. The step now waits for the stored draft, as the three other voice steps of the spec do. It failed in 4 runs before this wait.
- CI `e2e` job of the commit `9b1da749` merged with `main` (run 42422150406): `chat.spec.ts:962` "a scroll up loads older messages and keeps the position" failed. The list stayed at 20 messages. The trace shows the list at its end, two reads of `/api/chat` and no read with `before`, no console error. The `CI` job `check` of the same run failed for another reason: `make generate` changed `schemas.gen.ts`, because `main` had added the field `harness` to the API (fixed by the merge of `main` and `make generate`, commit `845004f8`). I did not find the exact order of events of the failed test in the trace. I found a race in the Mount `ChatList` that gives the same result, and a test reproduces it: a layout or a resize of the list that runs in the same frame as a scroll of the Owner, before the scroll event, scrolled the list back to its end (while it was pinned) or to the old place, so `ListNearTop` never came. The Mount now keeps the `scrollTop` that it knows (`knownTop`) and handles a scroll that it did not make (`catchUp`) at the start of each layout, resize and image load. The resize and the load do nothing before the first layout. The new test "a scroll up that comes with a layout of the list in the same frame loads older messages" (`web/e2e/chat.spec.ts`) fails on the code before the change (5.5 s timeout, checked) and passes after it. I cannot prove that this race is the one of the failed CI run, because the trace has no record of a layout in that frame.
- CI `e2e` job of `9f5c9cc1` (run 41895609099): `screenshots.spec.ts:2613`, step `action-failed`, `getByRole('dialog').getByRole('alert')` found no element. The cause is in the Foldkit code. The tray data reaches the program through a React render and a passive effect, so it can come after the end of a request. A late Port with a list without the row of the item closed the panel of a request that had failed, and the next Port brought the row back, but nothing opened the panel again. The Owner saw the error in the row and no panel. In the base code, the row opens the panel from `inboxFailure`, so the order of the reads has no effect. Commit `c20807bd` keeps a panel that shows the error of its own request when the tray without its row comes, and a panel that closed before the end of the request opens again at the end (`finishRequest`). Two Story tests cover both orders: "keeps the panel with the error when the tray without its row comes after the failed request" and "opens the panel again with the error when the tray without its row comes before the failed request". The first test fails without the change. The local runs did not show this failure, because the order depends on timing. The CI has not run on `c20807bd` when this text was written.

Changed selectors: none. The specs use roles, labels and `data-*` attributes, and the Foldkit markup keeps them (`data-message`, `data-queued`, `data-action`, `data-question`, `data-slot`).

Changed tests (the issue asks for each change by name):

- `web/e2e/live.spec.ts`: one new test, "the chat screen receives a live message after the live connection closes". It answers the next requests of `/api/events` with a 502, which closes the source for good. It sends a message from a second page, then lets the requests pass, and the first page must show the message. The test sends a second message after the first one shows, and expects it with no `/api/chat` read, so the new source must have a `message` listener. The test is a parity test: its current version (two messages, the file of commit `754de151`) also passes on the base commit (run in a worktree of `7c8beea`), because the React `App.tsx` has the same reconnect rules. It does not fail on the base commit, because the proof must not change the behavior.
- `web/e2e/screenshots.spec.ts`: two waits, no selector. These are not changes of a selector, so they break the limit of the issue, and the reason is named here. (1) `clearComposer` waits for the count of the remove buttons after each click. The spec failed one time on the base commit in this loop, so the race is in the old spec. (2) The voice step `chat-voice-send-wait` waits for the stored draft after `fill("")`. The rule of `AGENTS.md` is to wait for the stored draft after a `fill("")` before a `page.goto`, and the old spec did not do it at this step, as it does at the three other voice steps. The React code won this race by luck, and the Foldkit program writes the draft a few microtasks later (the Message, then the Command), so the next page could load the old draft. I did not find a change of the app that removes this race. The test shows a timing difference of Foldkit, and it is a difference of behavior, not a port gap. The alternative is to keep the old step with `test.fail`: it fails in about one run of three, so `test.fail` would also fail in the other runs.
- The fixtures of the specs that fake the API now have all fields of the API, because the program rejects a value of an inbound Port that has missing keys (section 3). This is a change of a fixture, not of a selector. The files: `web/e2e/actions.ts` and `web/e2e/stops.ts` (the sample Inbox items and actions: `action`, `answer`, `merging`, `mergeRefusal`, `todo`, `withdrawn`, and for an action `oldBrief`, `sender` and `labels` of each argument), `web/e2e/answered.spec.ts`, `inbox.spec.ts`, `navigation.spec.ts`, `notice.spec.ts`, `pending.spec.ts` and `screenshots.spec.ts` (the base object of the Inbox items), and `web/e2e/attachments.spec.ts`, `chat.spec.ts` and `screenshots.spec.ts` (the faked chat messages: `action`, `question`, `browserId`, `cancelledAt`, `deliveredAt`, `failedAt`, `stoppedAt`). The assertions of the specs did not change.
- `web/e2e/chat.spec.ts`: one new test, "a scroll up that comes with a layout of the list in the same frame loads older messages" (see the failures above).
- `web/e2e/load-time.spec.ts`: new spec for the load time, with the project `load-time` in `web/playwright.config.ts`. It runs in CI, and it asserts nothing but that the input becomes editable.
- Tests with `test.fail`: none. Each failure that the proof met was a port gap, except the race of the voice step above.

### Story tests

`web/src/chat/update.test.ts` has 50 tests with Vitest and the Story tools of Foldkit, `web/src/chat/markdown.test.ts` has 6 tests of the Markdown parse and its cache, and `web/src/chat/view.test.ts` has 5 tests of the Markdown view (`pnpm test`; `.mobius/check` runs all of them, in 0.6 s). They cover:

- the send queue: queue, send, the next item, discard, and the removal of a sent item after the next load, also when the server gives no `browserId`;
- the retry waits: the wait grows to the last value and stays, a wait that "Retry now" replaced is ignored;
- the merge of a live message into the list: sort, longer text wins, shorter text ignored, older than the first message dropped, queued item removed;
- the first steps of the answer panel, a todo with an empty note, a failed decision (the list stays, the panel opens again with the error), the Details panel of a card (it stays when the host sends the tray again) and of a tray row (it closes with the row);
- the pending state of the Stop button (it stays after a success, and a second tap does nothing), and of Merge and Send again in the tray, with the refusal;
- the send queue after a lost answer: the item that the server stored ends the wait, and the next item goes, also when the request of that item fails after the item left the queue;
- two taps on Approve, Reject or Send in the answer panel send one request for the item, and a request of one item does not block another item;
- an answer that waits for the final voice text goes to the item of the tap, also when the panel closes or another panel opens, and the voice region of the chat field stays when a panel opens;
- a panel that opens again reads the draft of its own item, and a panel that opens from the tray clears the error of its item even when no `ClosePanel` came before (the request of A fails while the panel of B is open). The test "clears the error of an item when its panel opens from the tray with no close before" fails when the clear in `OpenAnswerPanel` is removed (checked). The test "shows no old error in a panel that the Owner opens again" passes also without that clear, because `ClosePanel` clears the error first, so it covers `ClosePanel` only;
- a failed read of the browser store ends the load of the queue and shows the error;
- the panel of a request: a success, an error and a new request change only the panel and the error of their own item, and the local merge answer ends when the row comes with other data;
- a sent item leaves the queue at the end of its own load, and stays when a live message or the load of another send comes.
- a `javascript:` link and a `javascript:` image show as text with no link, and a link with a safe scheme keeps its `href`.

The test "removes a sent item at the end of its own load, also when the server gives no browserId" fails on the earlier code (the item stayed), and the Playwright test `attachments.spec.ts` "the bubble of a sent message shows the card of the Inbox item" failed in the same way before the fix.

### Screenshots

Two comparisons exist, and they differ.

**Local comparison.** The base commit and the branch were rendered on the same machine (macOS) and compared pixel by pixel (a tolerance of 2 of 255 for each color channel). Result on the head `76bc329b`: **4 of 298 images differ**. The earlier versions of the port differed in 141 images. The causes were: classes of the buttons and of the tray row, the gap of the Markdown blocks, the icons, the dialog markup, and `" · "` in a card line that React splits in three text nodes (the sub-pixel position of the next word differs when the line is one text node). This comparison does not decide the Done item, because the Owner sees the CI comparison.

**CI comparison.** `screenshots.yml` renders on Linux and compares the files with `cmp`, without a tolerance. For the code of commit `481d6eff` (the run of `13f5f478`, the image commit `1c935ae1` of the branch `screenshots`) it listed **80 images as changed**. The run of `20dbdabd` (image commit `c3a3f743`) listed the same 80 names. `main` has no change in `web/src` or `web/e2e` after the base commit, so this pull request causes each one. I compared the 80 pairs of the image commit `1c935ae1` pixel by pixel. In each pair, the differing pixels lie in one small block at the heading "Waits for you (N)" of the tray (in `chat-desktop` the block is the `(3`, and the chevron after the heading shifts with it). The groups are:

| Differing pixels | Block | Images |
|---|---|---|
| 353 | 27x22 | `action-lead-sender-phone` |
| 401 | 27x29 | `action-brief-diff-desktop`, `action-brief-hidden-desktop`, `action-lead-sender-desktop`, `chat-details-close-desktop`, `question-long-title-desktop` |
| 46 | 7x9 | `chat-agents-desktop`, `chat-coordinator-failed-desktop`, `chat-coordinator-failed-phone`, `chat-details-desktop`, `chat-details-evergreen-desktop`, `chat-tasks-closed-desktop`, `chat-tasks-desktop`, `chat-tasks-long-title-desktop`, `question-open-desktop`, `question-open-phone`, `transcript-panel-desktop`, `transcript-panel-start-check-desktop` |
| 488 | 30x29 | `action-approve-desktop`, `action-labels-desktop`, `action-reject-desktop`, `action-reject-phone` |
| 498 | 29x29 | `action-failed-phone`, `answer-options-desktop`, `autopilot-levels-phone`, `chat-stop-details-desktop`, `chat-stop-details-phone` |
| 499 | 29x29 | `action-failed-desktop` |
| 68 | 9x9 | `action-rejected-desktop`, `action-rejected-phone`, `action-tray-desktop`, `action-tray-phone`, `action-withdrawn-desktop`, `action-withdrawn-phone`, `chat-coordinator-waits-collapsed-desktop`, `chat-coordinator-waits-collapsed-phone`, `chat-coordinator-waits-desktop`, `chat-coordinator-waits-phone`, `chat-stop-long-title-desktop`, `chat-stop-long-title-phone`, `chat-waits-collapsed-desktop`, `chat-waits-collapsed-phone`, `chat-waits-desktop`, `chat-waits-phone`, `chat-waits-todo-desktop`, `chat-waits-todo-phone`, `chat-waits-typing-desktop`, `notice-answer-desktop`, `notice-answer-phone` |
| 74 | 9x10 | `action-failed-card-desktop`, `action-failed-card-phone`, `autopilot-levels-desktop`, `autopilot-trigger-desktop`, `autopilot-trigger-phone`, `chat-converting-desktop`, `chat-converting-phone`, `chat-desktop`, `chat-images-desktop`, `chat-inbox-item-desktop`, `chat-inbox-item-phone`, `chat-listening-desktop`, `chat-listening-phone`, `chat-live-desktop`, `chat-live-phone`, `chat-paused-desktop`, `chat-paused-phone`, `chat-phone`, `chat-stop-reason-desktop`, `chat-stop-reason-phone`, `chat-typing-desktop`, `chat-usage-limit-desktop`, `chat-usage-limit-phone`, `chat-voice-error-desktop`, `chat-voice-error-phone`, `chat-voice-send-wait-desktop`, `chat-voice-send-wait-phone`, `chat-waiting-desktop`, `chat-waiting-phone`, `chat-writing-desktop`, `chat-writing-phone` |

The cause: the base code in `ActionTray.tsx` has `Waits for you ({rows.length})`, which React renders as three text nodes. The Foldkit view had one text node with a template string, so the sub-pixel position of the count differed. The blocks of 27 to 30 pixels (16 images) are the same heading behind the overlay of a dialog, and the blur spreads the difference. Commit `9b56ed24` gives the heading three children (`"Waits for you ("`, the count and `")"`). The run of `b98e19de` (the code of `9b56ed24`) confirms the cause: its screenshots section shows "No screen changed", and its `CI` workflow passed. The runs of `20dbdabd` and `13f5f478` had a failed `CI` workflow because of one Go test of `internal/engine` (`TestAnEndEventStartsANewLeadSessionWhenNoSessionIsOpen`). This pull request changes no Go code, and the same test passed in the other runs.

Of the 4 images of the local comparison, only `chat-usage-limit-phone` is in the CI list, and there it differs only in the heading block of 74 pixels. The two Checkup images and `chat-brief-all-desktop` are identical in CI.

| Image of the local comparison | Pixels | Cause |
|---|---|---|
| `checkup-tools-desktop`, `checkup-tools-phone` | 8,439 and 8,429 | The Checkup screen is React code that the proof did not change. The text shows the path of a temporary folder of the test server, and its number changes from run to run (`...-56750/harnesses/claude` in the base, `...-57158/...` in the branch). Not a difference of the code. |
| `chat-brief-all-desktop` | 1,922 | The button "Show less" of the Brief bubble has the gray background of `hover`. The mouse stays on the button after the click. In the base code, React builds the button again when the text changes, so the browser does not apply `hover` until the mouse moves. Foldkit keeps the same button. |
| `chat-usage-limit-phone` | 36,431 | The list is scrolled by a few pixels differently, so each line of the list shifts. The branch shows the last line of the text "The Lead waits for the usage limit until Sep 28, 12:00 PM." (`PM.` on the second line), and the base cuts it, because the base scrolled before the line got its second line. The Mount `ChatList` follows late growth of the list, so the branch is at the end of the list. |

## 7. Problems

Each item has the workaround that the proof used.

1. `@foldkit/markdown` has a small vocabulary, and `parseMarkdown` throws on each node outside it (`normalize.js`): a link or an image URL with a scheme other than `http:`, `https:`, `mailto:` or `tel:` (for example `javascript:`, `ftp:` or `slack:`; the base code shows a link with such a safe scheme, and removes only unsafe ones), raw HTML, a link definition, a footnote, a `---` block at the start of the text, and each inline directive. `remark-directive` is always on, so a text such as `Keep 09:30 UTC` also throws (`Unsupported markdown node "textDirective"`). Agent text has colons and `<` often. The base code (`markdown-to-jsx`) shows raw HTML as text and keeps the Markdown of the rest of the message. Workaround in `web/src/chat/markdown.ts` (`parseChatMarkdown`): the first parse reads the text as it is. If it throws, the second parse escapes each colon between two word characters and each `<`, but not in code (a backslash shows in code) and not in a URL (it ends the link). A text that fails both parses shows as plain text, with visible `**`, `#` and table pipes. The unfixed limit is: a text with a link definition, a footnote, a start `---` block or other nodes outside the vocabulary loses all its Markdown. The result of the parse, also a failure, is kept for each text. A text with a URL scheme outside the list loses all its Markdown, and a text with `[x](javascript:alert(1))` shows as plain text, so the link is not active. The views of `Link` and `Image` still give the URL to `h.Href`, because `h.Attribute` has no URL filter: only `h.Href` and `h.Src` empty a `javascript:` or `vbscript:` URL. The Owner can ask for an option to turn directives off and for a mode that shows unknown nodes as text.
2. Flags are read one time, and the host must send the inbound Ports again for each new program. The proof did not test whether a Port message that the host sends before the program starts is lost. It avoids the question: a React `key` on the host component mounts a new host for a new chat, and its Port effects run after the embed effect.
3. `OnPaste` does not give the clipboard files, and `OnKeyDown` does not give `isComposing` or `keyCode`. The chat input needs both: a pasted picture, and the Enter of an IME. Workaround: one `Mount` on the form (`ComposerBehavior` in `web/src/chat/composer.ts`) adds the DOM listeners. The same `Mount` keeps a button from taking the focus (`mousedown` with `preventDefault`), because `OnMouseDown` cannot call `preventDefault`.
4. Foldkit renders in the next animation frame. A Message goes through a queue, and a Command starts in a microtask. A DOM event that Playwright sends right after another one can see the old view. Two tests saw it: the type of the Send button (it was `button` while the Model had an empty draft and a running agent), and the draft in the browser store (see "Tests"). Workaround: the Send button has `type="submit"` in all states, the Mount `ComposerBehavior` enables the Send button on an `input` event at once (the button of a draft with text is not disabled for one frame), and the spec waits for the stored draft. React updates a controlled input in the same turn, so the old tests relied on it.
5. A controlled `textarea` and the voice text. The voice code puts text at the cursor and sets the scroll position of the field. The Model cannot hold the cursor. Workaround: the Subscription sets the DOM value and the cursor directly, and sends `VoiceTextPlaced` so the Model gets the same value. The region state (the text around the voice text) lives in the Stream, and an `input` event ends it.
6. Scroll rules need DOM data that the view cannot give: the position at the end, the position of the first unread message, and the place of the message after a prepend. Workaround: one `Mount` (`ChatList` in `web/src/chat/scroll.ts`) with a `MutationObserver`, a `ResizeObserver` and a `scroll` listener. The view gives it two data attributes (`data-ready` and `data-first-unread`). This is the largest piece of imperative code in the program. It does the work of 6 refs, 3 effects and 2 layout effects of the React code, and it is as hard to read. At least six of the Playwright tests of `chat.spec.ts` failed because of details of this Mount. A scroll event comes in the next frame, so a layout or a resize of the list that runs first sees the old position, and it can undo the scroll of the Owner. The Mount keeps the `scrollTop` that it knows and handles a scroll that it did not make before it acts (`catchUp`).
7. The Model must be an Effect Schema, so each API type needs a Schema. The first version of the port used `Schema.Unknown` with a cast, and Foldkit checked no data. Now the Schemas are real (section 3). The costs: orval writes no Schema for a component, so `schemas.ts` takes each component by a path into a response Schema; a list of a Schema has a readonly type, so the chat code uses the Schema types and a few helpers of the React side take a `Pick` or a mutable array Schema; and a Message constructor checks its payload only in part, so a fixture with missing keys fails in the Port and in some Message constructors, but a wrong primitive does not.
8. A Mount reads its arguments one time. The data for the Brief bubble and the list go through attributes, and the Mount reads them with an observer. This works, but it looks like the pattern that Foldkit wants to avoid. The program has six Mounts: `ComposerBehavior`, `ChatList`, `BriefLength`, `PanelOpened`, `RouterLink` and `ShowFile`. `ShowFile` creates the blob URL of a picture and revokes it when the picture leaves the page. Its key comes from the name, the size, the date and the place among equal files, because a Mount reads its file only at insert.
9. React inside Foldkit: `Runtime.embed` replaces the container element with its own root. A container that React owns breaks React. Workaround: a wrapper `div` that has only the container as child (see the comment in `web/src/Conversation.tsx`).
10. The type of a Mount Stream must list only the Messages that the Mount declares, so a `Stream.callback` needs a narrow type (`ComposerMessage`). This is a small cost, but each Mount repeats it.
11. A dialog inside the chat section hides the tray, because a Tailwind rule of the chat section (`group-has-[textarea:focus]/chat`) sees the focused field of the dialog. The React dialog is in a Radix portal. Workaround: the Mount `PanelOpened` on the portal element moves the dialog to the body. The move comes before the focus goes into the dialog, because a move takes the focus away, and the insert hook of a child runs before the insert hook of its parent, so the move and the focus are in one Mount. A Playwright check (`chat.spec.ts` "Answer of a row in the chat opens a panel with the rendered text") tests that the dialog has the focus.
12. The cost of a faithful port. The Tailwind classes and the markup of each component came from the React code, and the first version of the port guessed them. 141 of 298 screenshots differed at that time, and about 50 specs failed. A port needs the exact classes, and a check by screenshot comparison after each component finds the errors early. The proof built a comparison tool for this (a few lines with the PNG reader of Playwright).
13. Modal behavior. Radix gives a dialog its focus trap, its scroll lock, its `aria-modal`, its return of the focus and its `aria-hidden` on the rest of the page. The docs of `@foldkit/ui` list a Dialog component, and the proof did not test it. The proof wrote the first four in the Mount `PanelOpened`. A Playwright check tests the focus into the dialog. No test uses the Tab key, the scroll lock or the return of the focus, so these are checked by reading the code only. The `aria-hidden` of the rest of the page is not done.
14. The answer panel and the tray need the same host data as the React code: the `--keyboard` variable (`web/src/lib/keyboard.ts`, now shared by the React hook and the Mount), the login after a 401 (a helper `withLogin` in `web/src/chat/commands.ts` sends each 401 to the outbound Port), and the actions of the tray row (Merge, Send again, Dismiss and Resume, each with a pending state). The first version of the port did not have them. A review found them, and the specs did not, because the specs test only the answers with a 200 status. A Foldkit move needs a list of these side effects for each React component, and it needs Story tests for the failure paths. The host data comes through a React render and a passive effect, so a Port can come before or after the end of a Command. The update must give the same result in both orders (the CI failure in section 6 came from this).
15. Cost of the view after a Message. The view renders again after each Message, for example after each key in the input. The React code has `memo(Message)` for each message. The Markdown parse is the slow part, so `parseChatMarkdown` keeps the parsed result of each text, also a failure. `view` calls `beginMarkdownRender` first, and a text that the last render did not use leaves the cache, so the cache holds the texts of the chat on the screen, however long the chat is (a test in `markdown.test.ts` uses 700 texts). The vnodes of each message are still built again. A one-time test in Node (not committed) called `view` for chats of messages of about 500 characters each, with Markdown. For 200 messages the first call took 206 ms and the next calls 36 to 38 ms each. For 700 messages the first call took 420 ms and the next calls 126 to 130 ms each. The time grows with the number of messages, because each render builds the vnodes and the Markdown tree of each message again, with the parse kept. This is the cost of the `view` function only, before the patch of the DOM, with no CPU throttle. On a phone, expect about 4 times this. The cost grows with the length of the chat, and a lazy view for each message (`foldkit/html`) is the next step if the Owner wants it. The proof did not try it, and the load-time spec does not measure this.
16. A Mount gets its arguments when its element is inserted, and a patch of an element for another message does not give new arguments. The list of messages needs a `h.Key` for each message, separator and queued item, or a moved element keeps the `RouterLink` path of the element that was there before. The lint rule `keyed-required-for-mapped-rows` does not find a `flatMap`, so the proof found it by review. Each list that holds elements with a Mount needs a key.
17. The Brief diff and `==`. `briefMarkdown` marks an added text with `==`, and the Brief text can have its own `==`. The views of the Brief diff split a text node at each `==`, because an added text can open before an inline code span and close after it. The proof shields each `==` of the Brief text with a private-use character before the diff (`shieldEquals` in `web/src/chat/markdown.ts`) and restores it in the text, code and code block views. `web/src/chat/view.test.ts` has a test for an unchanged line with `==`. A Brief that already has the private-use character `U+E000` would show it as `=`. `markdown-to-jsx` needs none of this, because it renders only a closed `==text==` pair.
18. The lint rule `foldkit/no-nonportable-server-globals` is one of the 38 rules of the recommended list of `@foldkit/oxlint-plugin`. The proof does not turn it on, and it does not turn it off: it is not in `web/.oxlintrc.json`, which has the other 37 rules for `web/src/chat/`. With the rule on, `pnpm lint` reports 33 errors for the browser globals `document`, `window`, `localStorage`, `ResizeObserver` and `MutationObserver`. The rule protects code that runs on a Foldkit server target (server rendering). Mobius ships the client as a browser app only (`web/embed.go` serves `web/dist`), and the Mounts, the Subscriptions and the Commands of the chat screen need the DOM. A change to pass the rule would move each of these calls into the Foldkit `Dom` module or into a Resource, and the proof did not try it. If the Owner wants the rule, the move needs this work.
19. Type escapes. `web/src/chat/` and `web/src/Conversation.tsx` have no `as T`, no `!`, no `any`, no `Schema.Unknown`, and no `unknown` type (`as const` is the only `as`). The changes: a DOM element or event is narrowed with `instanceof` (`HTMLTextAreaElement`, `KeyboardEvent`, `ClipboardEvent`, `MouseEvent`, `HTMLImageElement`, `HTMLElement`), `renderMarkdown` takes `HtmlBuilder<M>` so the test passes `inertHtml`, and the Message payloads have Schemas, so the casts of `update.ts` went away. A Mount gets `element` as `Element`, so each Mount that needs an HTML element checks it, and returns at once when it is not one. No place needed an escape. Two places still take a value of the type `unknown` in the library code that the chat code only calls: `JSON.parse` in `web/src/lib/events.ts` (used by the React screens, and no longer by the chat code) and the `any` data of `MessageEvent` (lib.dom), which `Schema.decodeUnknownEffect` takes as its `unknown` input.
20. Promise calls. Each Promise call of the Commands and of the Subscriptions runs in `Effect.tryPromise` (`request`, `store`, `voice` and `fitted` in `web/src/chat/errors.ts`) with a `Schema.TaggedError` class (`RequestFailed`, `StoreFailed`, `VoiceFailed`, `ImageFailed`), and a failure is handled with `Effect.catchTag` or ignored with `Effect.ignore`. `Effect.try` and `Result.try` wrap the synchronous calls that throw (`localStorage`, the Markdown parse), with the tagged errors `StoreFailed` and `MarkdownFailed`. `PostQueued` passes the `AbortSignal` of Effect to `fetch`, so an interrupted Command stops its request (the old code made a controller and never used it). A Command that ran the old `Promise` code now has the same result messages. The libraries of the React side (`web/src/lib/outbox.ts`, `draft.ts`, `voice.ts`, `images.ts`) still return Promises, and the Commands wrap them at the boundary.
21. Effect inside DOM callbacks. The live Stream has timers and event listeners that call `Effect.runFork` (the check of the login after a closed connection, the decode of an event). The effects need no service, so `runFork` is enough. `Stream.callback` and `Queue.offerUnsafe` stay for the push from a listener, because a listener callback runs outside of any Effect. A `withLogin` helper of the Commands needs `NoInfer` in its return type, because TypeScript otherwise infers the Message union of the whole program from the type of `execute`.
22. Changes of `main` after the proof started. The merge of `main` (#1494) lets the Owner dismiss an optional deployment todo: the React `InboxRow` shows Dismiss when `item.todo?.dismissable`. The tray of the Foldkit program is a copy of the code of the base commit, and it shows Dismiss only for the kinds that `InboxRow` showed before (a failed Lead and a tool upgrade). So the chat tray does not show Dismiss for a dismissable todo. The issue says not to copy later changes of other tasks into this branch, so I did not copy it. This is a cost of the move screen by screen: a copy of a component that React still renders needs the same change twice until the move ends. The generated Schemas needed `make generate` after each merge of `main` that changes the API (twice in this branch), and the fixtures of the specs needed the new field, because the Port rejects a value with a missing key.

### Line counts

| | Lines |
|---|---|
| React code of the screen at the base commit: `Conversation.tsx` 977, `MessageInput.tsx` 507, `ActionCard.tsx` 247, `ActionTray.tsx` 73, `QuestionCard.tsx` 89, `InboxItemCard.tsx` 54, `AnswerPanel.tsx` 376, `Markdown.tsx` 40, `FileImage.tsx` 21 | 2,384 (and about 270 lines of `InboxRow` in `Inbox.tsx`, which the tray reuses) |
| Foldkit code: `web/src/chat/*.ts` without the tests: `view` 1,592, `update` 1,335, `commands` 750, `composer` 541, `subscriptions` 372, `model` 173, `icons` 172, `scroll` 179, `message` 141, `markdown` 69, `schemas` 82, `errors` 37, `program`, `ports`, `flags` and `ui` 96 | 5,539 |
| React host (`web/src/Conversation.tsx`) | 107 |
| Vitest tests (`update.test.ts` 1,424, `markdown.test.ts` 48, `view.test.ts` 43) | 1,515 |

The Foldkit code is about 2.3 times as long as the React code (5,539 lines against 2,384). Part of the difference is explicit code that React hides: each effect is a Command, and each change of state is a Message with a handler. Another part is a copy of the markup that a generator or shared helper can remove. `icons.ts` now has the data of the `lucide-react` icons, and `ui.ts` shares the class helpers (`buttonVariants` and `badgeVariants` moved to files that React and Foldkit both import).

## 8. Input for the plan

### Sketch of the Model and the modules of the full client

```
web/src/
  main.ts                 Runtime.makeApplication: Model, Flags, router, subscriptions
  app/
    model.ts              { route, session, organization, link, activities, shell }
    message.ts            GotRouteMessage, GotLiveEvent, GotChatMessage, GotInboxMessage, ...
    update.ts             pure update, delegates to the screens with Submodels
    live-events.ts        ONE Subscription of /api/events (silence, retry, online, pageshow, visibility)
    router.ts             typed routes for the existing URLs
  screens/
    chat/                 the proof, with Model, Message, update, view, commands
    inbox/  tasks/  usage/  checkup/  settings/  roles/  ...   one folder for each screen
  ui/                     markup helpers: button, badge, dialog (from @foldkit/ui), icons (generated)
  lib/                    framework-free code that stays: voice, outbox, draft, time, diff, images, api
  api/api.gen.ts          unchanged
```

Rules for the full client:

- One Subscription owns the `EventSource` and sends each event as a Message. The screens react to it through the `update` of the parent. This removes the second connection of the proof.
- A screen is a Submodel with its own `Model`, `Message` and `update`, as the Foldkit docs say for large programs. The proof has one flat Model with 60 fields. This works for one screen and does not scale.
- `lib/` keeps the code that has no React code: voice, outbox, draft, time, images, diff.

### Move screen by screen with Embedding

Facts from the proof:

- It works. The host in `web/src/Conversation.tsx` is 107 lines. The Flags, the inbound Ports and the outbound Ports carried what the chat screen needed: the login request (each 401 of a Command) and the navigation (the Mount `RouterLink` on the link of an action sender, with a modified click left to the browser).
- Each screen that is embedded adds a Foldkit runtime start (an Effect runtime, a Subscription system and one Port set). The size of Foldkit and Effect (144 kB gzip) is paid at the first screen, and later screens add only their code.
- Two programs on a page need care: a Port holds only data that a Schema can encode. A function, a DOM reference or a React element cannot cross it. The chat host keeps `head` and `note` in React for this reason. The wrapper `div` and the `key` on the host are needed (see Problems 2 and 9).
- The shared state is the hard part: the Inbox screen and the chat screen write the same draft store, the Workstream list and the unread counts live in React hooks, and `App.tsx` owns the `EventSource`. Each of them must cross the Port, or must move to `lib/` as a store with a subscription, before the second screen moves.
- The size grows during the move: React stays until the last screen moves. The bundle is the sum of both, so the Foldkit move is +170 kB gzip until the end.
- `main` stays releasable: each screen is a separate pull request, and the Playwright specs of that screen are the check. The screens that other Workstreams change (`Inbox.tsx`, `Pauses.tsx`, `Checkup.tsx`, and the files of voice) move last.

### Move in one step on a long branch

Facts from the proof:

- The proof screen took about two full sessions of agent time for 2,400 lines of React. The full client is 12,600 lines. A linear estimate is far above ten sessions, and the first screen is the one that has the new patterns (Mounts, Subscriptions, Ports), so the later screens may be faster.
- A long branch has no size cost in the middle. The Foldkit router replaces `@tanstack/react-router` at once. The size at the end is 216 to 344 kB gzip with plain SVG charts (section 5), and the base is 331 kB, so the bundle is not clearly smaller.
- The branch is behind `main` all the time, and other Workstreams change `Inbox.tsx`, `MessageInput.tsx`, `voice.ts`, `outbox.ts`, `Pauses.tsx` and `Checkup.tsx`. Each merge from `main` needs a manual port of those changes into Foldkit code. The proof already saw this risk: Workstream #1420 changes the files that the chat input uses.
- The specs of the whole client can check the branch only at the end, because the Playwright projects depend on each other (a failed project stops the next ones, as this proof saw: one failed spec left 258 tests that did not run).

Recommendation for the plan: move screen by screen with Embedding, in this order: chat (this proof), Inbox, Tasks, Checkup and the settings screens, Usage (with the SVG charts), and last the shell and the router. Fix the facts in section 7 first: ask for the directive option of `@foldkit/markdown`, and decide the compression of the Mobius server, because the size of the bundle is the main cost that the Owner can see.
