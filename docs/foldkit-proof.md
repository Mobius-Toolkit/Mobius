# Foldkit proof: the chat screen

This document reports a proof of Foldkit on the chat screen of Mobius. The Owner uses it to decide about the move of `web/` to Foldkit.

- Tested versions: `foldkit` 0.170.0, `@foldkit/ui` 0.170.0, `@foldkit/markdown` 0.15.0, `@foldkit/vite-plugin` 0.29.0, `@foldkit/oxlint-plugin` 0.15.4, `effect` 4.1.2, `@effect/platform-browser` 4.1.2.
- Screen: `Conversation` (`/chat` and `/workstreams/$owner/$name/$number`). It runs as one Foldkit program in `web/src/chat/`. `web/src/Conversation.tsx` is a React host that calls `Runtime.embed`.
- Base commit for all comparisons: `7c8beea`.

## Result in short

- The chat screen runs in Foldkit on `/chat` and on `/workstreams/$owner/$name/$number`, inside a React host. The program covers the message list, the live events, the send queue with the retry waits, the chat input with the voice button and the pictures, the scroll rules, the Brief bubble, the tray of pending actions, the question and action cards, and the answer panel.
- All 459 Playwright tests pass on the head of the branch, and the 18 Story tests pass. The spec files of the Done list ran: `chat`, `attachments`, `voice`, `voice-language`, `actions`, `answered`, `pending`, `live`, `notice`, `navigation`, `inbox` and `screenshots`. See "Tests" for the flaky tests.
- 28 of 298 screenshots differ from the base commit, and 23 of them differ by 835 pixels or less (a chevron icon, a scroll offset of 4 pixels). See "Screenshots".
- The move cost much more work than the line counts show. The first session of the port stopped at the point where about 50 specs failed, and the next session needed about 15 rounds of fixes, with a full test run of 5 to 10 minutes after each round, to get all of them to pass. The causes were not limits of Foldkit. They were behavior that the first port left out: the retry waits, the pictures, the voice text, the scroll rules and the answer panel. Plan for this cost.
- Foldkit gave no blocker. It has three limits that cost time: the Markdown parser throws on text such as `09:30`, the attributes `OnPaste` and `OnKeyDown` give too little data for the chat input, and the view renders in the next frame, so a test that reads the DOM right after an input event can see the old state. Each one has a workaround. See "Problems".
- The cost on the wire is large because the Mobius server sends the files with no compression: +51 % raw JavaScript and +45 % load time on the emulated phone. See "Size and load time".

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
  | `online` | `handleWake` calls `reconnect` when the page is visible. |
  | `pageshow` with `persisted` | `handleRestore` calls `reconnect`. |
  | `visibilitychange` | `handleWake` calls `reconnect` when the page is visible. |

- After each new connection, the `open` event gives the Message `ConnectionOpened`. The update runs the Command `FetchPage` with the id of the newest known message. The Command reads pages until it reaches that id, so a message that came while the connection was down is not lost. This is the `load` function of the React code.
- Proof by test: `web/e2e/live.spec.ts`, the test "the chat screen receives a live message after the live connection closes". The test answers each request of `/api/events` with a 502. That closes the source for good (`readyState` is `CLOSED`), so only the retry timer and the check of `/api/github/apps` can reconnect. The test sends a message from a second page, then lets the requests pass, and the first page must show the message after the retry. The `online`, `pageshow` and `visibilitychange` cases and the silence timer have the same `reconnect` function, but the proof has no test for them. The `online` event starts the test.
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
| `dialog.tsx` | `@foldkit/ui/dialog`. It uses the native `dialog` element. This proof wrote the markup by hand and has no focus trap. |
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

The chat screen uses 15 icons. A first version drew them by hand, and 10 pixels of 5 of the icons differed from the React icons. `web/src/chat/icons.ts` now holds the icon data of `lucide-react`, which a one-off script copied, and one small function draws all icons. The app uses 25 icons. For the full client, generate the data from the package `lucide` (it has no React code) in the same way. The 25 icons are 4.2 kB gzip in a React build, so the size is not a reason to keep `lucide-react`.

### `recharts`

Recommendation: draw the two charts of `Usage.tsx` as plain SVG in the Foldkit view, and keep the tooltip and the legend in the Model. Reasons:

- `recharts` needs React, so it cannot stay. Its gzip size for `BarChart`, `LineChart`, the axes, the grid, the tooltip and the legend is 122 kB. This is 37 % of the current React bundle.
- ECharts (the library of the Foldkit Charting example) is larger: 213 kB gzip for the bar chart, the line chart, the grid, the legend, the tooltip and the SVG renderer. It also draws its own text and tooltip, so it does not give the same look.
- uPlot is small (25 kB gzip), but it draws on a canvas and has no legend and no tooltip in the DOM. It does not give the same look.
- Recharts creates SVG with a grid, two axes, bars or lines, an HTML tooltip and an HTML legend. All of these use CSS variables of the theme. Plain SVG with the same classes can give the same look. The proof did not build the charts, so this is an estimate from the code of `Usage.tsx` and not a test.

## 3. API client

`web/src/api/api.gen.ts` stays as it is. `web/orval.config.ts` needs no change. The Commands call the generated functions (`getChat`, `answerInboxItem`, `approveAction`) inside `Effect.promise`. The types of the generated client are plain TypeScript types. Two small costs:

- A Model must be an Effect Schema. The generated types have no Schema, so the Model holds them as `Schema.Unknown as Schema.Schema<ChatMessage[]>`. This is a cast, and Foldkit does not check the data. A full move can add a Schema for the few types that the Model uses, or can move the generated client to a generator that emits Schema (for example `orval` with the Effect output). The proof did not test that.
- The Messages carry API data as `Schema.Unknown`, and the `update` function casts it again. This is the same cast.

## 4. Voice, outbox and service worker

- Voice: `web/src/lib/voice.ts` is a module with its own store and no React in its core. It stays. The Foldkit side has three parts.
  - A Subscription (`createVoiceStream`) reads the store: it sends the phase and the error as Messages, and it registers an input with `registerVoiceInput`. The input callbacks put the live text and the final text in the field. They keep the text around the voice text (`region`) in the Stream, so an edit of the Owner ends the region. They send `VoiceTextPlaced` to the Model.
  - Commands call the actions of the store: `ToggleVoice`, `StopVoice`, `ResendVoice`, `DiscardVoice`, `FinishVoiceForSend`, `ProbeVoice`.
  - The Send button first runs `ProbeVoice`. If a live text shows in the field, the Model waits for `FinishVoiceForSend`. Else it stops the voice and sends at once.
- Outbox and draft: `lib/outbox.ts` and `lib/draft.ts` stay. Commands call them: `EnqueueMessage`, `PostQueued`, `RemoveQueued`, `ListQueued`, `LoadDraft`, `SaveDraft`. The retry waits are a Command: `PostQueued` gives `SendAttemptFailed`, the update runs `WaitToRetry`, and `WaitToRetry` gives `RetryDue` after the wait. A counter (`retrySequence`) in the Model ignores a wait that "Retry now" already replaced. This replaces the loop with `setTimeout` of the React code, and the Story tests prove it.
- The draft changes by React code (for example the Chat button of the Inbox screen adds an Inbox item) are not read while the program runs. The program reads the draft one time at its start. A full move has no second writer, so the problem goes away.
- Service worker: `web/public/sw.js` has only a pass-through `fetch` handler, and nothing in the chat screen uses it. The proof did not change it. In a full client, one Command at the start registers it, and a Subscription on `navigator.serviceWorker` gives the Messages for an update.

## 5. Size and load time

Measured on this machine (macOS), one run for each commit, as the issue asks. A Chromium emulation is not a real phone: it throttles the CPU by a factor of 4 and the network to the Slow 4G values of Chrome DevTools (1.6 Mbit/s down, 750 kbit/s up, 150 ms latency), but the CPU and the memory of a real phone differ.

| | Base `7c8beea` | Branch | Change |
|---|---|---|---|
| JavaScript that `/chat` loads, gzip | 330.1 kB (one chunk) | 497.0 kB (`index` 352.2 kB, `foldkit-effect` 144.4 kB, runtime 0.4 kB) | +166.9 kB (+51 %) |
| JavaScript that `/chat` loads, raw | 1,082.7 kB | 1,639.4 kB | +556.7 kB (+51 %) |
| CSS, gzip | 15.1 kB | 15.3 kB | +0.2 kB |
| Median of 5 loads until the chat input is editable (`web/e2e/load-time.spec.ts`) | 6,537 ms | 9,492 ms | +2,955 ms (+45 %) |

Notes:

- Foldkit and Effect are 144.4 kB gzip in their own chunk (`foldkit-effect`, from `manualChunks` in `web/vite.config.ts`). The Foldkit part of the app (`web/src/chat/`) is in the main chunk.
- The Mobius server sends the files with no compression (`web/embed.go` has no gzip code). Thus the emulated network carries the raw bytes, and the load time follows the raw size (+51 % bytes, +45 % time). Compression on the server would remove most of the cost. The proof did not add it, because the issue forbids a change of the Go code.
- Estimate of a client with no React. Start with the base JavaScript (330.1 kB gzip) and remove what only React needs. The gzip sizes were measured in one build each: `react` and `react-dom` 75.8 kB, `@tanstack/react-router` at most 36.5 kB, `markdown-to-jsx` 32.5 kB, `lucide-react` (25 icons) 4.2 kB, `radix-ui` at most 85.5 kB (the measure imports all of it, so the real size is smaller), and `recharts` 122.2 kB. Add Foldkit, Effect and the Foldkit router (144.4 kB for the chat screen alone, more with `@foldkit/ui`), `@foldkit/markdown` (inside the main chunk) and the plain SVG charts (small). The result is in the range of 250 to 300 kB gzip. This is below the base (330 kB), if the charts do not use a library, and about equal to it if they use ECharts (add 213 kB). The estimate has a large error, because the parts overlap and the measures of `radix-ui` are too high.
- The size of the Foldkit chunk is fixed (Effect), and the size of the app code per screen is small. Thus the first screen costs the most, and the later screens cost little.

## 6. Tests

### Playwright

Command: `pnpm e2e --retries=2` in `web` (all 459 tests of 21 spec files). The run on the head of the branch: 459 passed, 0 failed, 0 flaky. The runs before it showed these tests that fail sometimes. None of them has a Foldkit cause that the proof found, and each passed in the next run:

- `chat.spec.ts` "the Tasks tab shows owner/name#N and the Start button ..." (error `route.fetch: Test ended`), and "Resume gives a needs-human task ..." (error `Response has been disposed`). Both belong to the Tasks tab (React code). A route callback of one test is still in flight when the test ends, and the error shows in the next test. They failed in 3 of the 10 full runs. The proof did not change the Tasks tab.
- `chat.spec.ts` "two taps on Send in one turn send one message ...". It failed in 2 runs before the Send button got `type="submit"` in all states (see Problems, item 4), and it passed in the 5 runs after the change.
- `screenshots.spec.ts` (the step `chat-voice-send-wait`): the field had the text two times. A `fill("")` of the step before it did not wait for the draft in the browser store, so the next page loaded the old draft. The step now waits for the stored draft, as the three other voice steps of the spec do. It failed in 4 runs before this wait.

Changed selectors: none. The specs use roles, labels and `data-*` attributes, and the Foldkit markup keeps them (`data-message`, `data-queued`, `data-action`, `data-question`, `data-slot`).

Changed tests (the issue asks for each change by name):

- `web/e2e/live.spec.ts`: one new test, "the chat screen receives a live message after the live connection closes". It answers the next requests of `/api/events` with a 502, which closes the source for good. It sends a message from a second page, then lets the requests pass, and the first page must show the message. The test is a parity test: it also passes on the base commit (run in a worktree of `7c8beea`), because the React `App.tsx` has the same reconnect rules. It does not fail on the base commit, because the proof must not change the behavior.
- `web/e2e/screenshots.spec.ts`: two waits, no selector. (1) `clearComposer` waits for the count of the remove buttons after each click (the spec failed one time on the base commit in this loop). (2) The voice step `chat-voice-send-wait` waits for the stored draft after `fill("")`. Foldkit saves the draft a little later than the React code did, so the next `page.goto` could win the race.
- `web/e2e/load-time.spec.ts`: new spec for the load time, with the project `load-time` in `web/playwright.config.ts`. It runs in CI, and it asserts nothing but that the input becomes editable.
- Tests with `test.fail`: none. Each failure that the proof met was a port gap, not a limit of Foldkit.

### Story tests

`web/src/chat/update.test.ts` has 18 tests with Vitest and the Story tools of Foldkit (`pnpm test`; `.mobius/check` runs it, in 0.4 s). They cover:

- the send queue: queue, send, the next item, discard, and the removal of a sent item after the next load, also when the server gives no `browserId`;
- the retry waits: the wait grows to the last value and stays, a wait that "Retry now" replaced is ignored;
- the merge of a live message into the list: sort, longer text wins, shorter text ignored, older than the first message dropped, queued item removed;
- the first steps of the answer panel.

The new test "removes a sent item on the next PageLoaded" fails on the earlier code (the item stayed), and the Playwright test `attachments.spec.ts` "the bubble of a sent message shows the card of the Inbox item" failed in the same way before the fix.

### Screenshots

The screenshots of the base commit and of the branch were made on the same machine, and they were compared pixel by pixel (a tolerance of 2 of 255 for each color channel). Result: 28 of 298 images differ.

| Images | Difference |
|---|---|
| `checkup-tools-desktop`, `checkup-tools-phone` (8,452 and 8,327 pixels) | The Checkup screen is React code that the proof did not change. The cause is not known. The difference can come from the time of the two runs. |
| `question-long-title-desktop` (3,785) | The area of the buttons at the bottom right of the question card. Not analyzed. |
| `chat-waits-todo-desktop` (2,299), `chat-brief-all-desktop` (1,922) | The list is scrolled 4 pixels differently, and the Brief button. Not analyzed further. |
| 5 images of 237 to 835 pixels (`chat-waits-desktop`, `chat-waits-typing-desktop`, `chat-stop-long-title-desktop`, `chat-records-desktop`, `chat-records-phone`) | A scroll offset of 4 pixels, and the record bubble. Not analyzed further. |
| 18 images of 55 pixels or less | A small icon of 7 by 10 pixels (a chevron in the tray header or in the folded event), and one small icon in `chat-waits-todo-phone`. Not analyzed further. |

The Owner must read the section "Screenshots" of the pull request. The names above show where to look. The comparison of CI is against `main`, and its images come from another machine, so these numbers do not predict it.

## 7. Problems

Each item has the workaround that the proof used.

1. `@foldkit/markdown` always enables `remark-directive`. A text such as `Keep 09:30 UTC` makes `parseMarkdown` throw `Unsupported markdown node "textDirective"`. Agent text has such colons often. Workaround: `parseChatMarkdown` in `web/src/chat/view.ts` catches the error and parses the text again with each colon between two word characters escaped. The Owner can ask for an option to turn directives off.
2. Flags are read one time, and the host must send the inbound Ports again for each new program. The proof did not test whether a Port message that the host sends before the program starts is lost. It avoids the question: a React `key` on the host component mounts a new host for a new chat, and its Port effects run after the embed effect.
3. `OnPaste` does not give the clipboard files, and `OnKeyDown` does not give `isComposing` or `keyCode`. The chat input needs both: a pasted picture, and the Enter of an IME. Workaround: one `Mount` on the form (`ComposerBehavior` in `web/src/chat/composer.ts`) adds the DOM listeners. The same `Mount` keeps a button from taking the focus (`mousedown` with `preventDefault`), because `OnMouseDown` cannot call `preventDefault`.
4. Foldkit renders in the next animation frame. A Message goes through a queue, and a Command starts in a microtask. A DOM event that Playwright sends right after another one can see the old view. Two tests saw it: the type of the Send button (it was `button` while the Model had an empty draft and a running agent), and the draft in the browser store (see "Tests"). Workaround: the Send button has `type="submit"` in all states, and the spec waits for the stored draft. React updates a controlled input in the same turn, so the old tests relied on it.
5. A controlled `textarea` and the voice text. The voice code puts text at the cursor and sets the scroll position of the field. The Model cannot hold the cursor. Workaround: the Subscription sets the DOM value and the cursor directly, and sends `VoiceTextPlaced` so the Model gets the same value. The region state (the text around the voice text) lives in the Stream, and an `input` event ends it.
6. Scroll rules need DOM data that the view cannot give: the position at the end, the position of the first unread message, and the place of the message after a prepend. Workaround: one `Mount` (`ChatList` in `web/src/chat/scroll.ts`) with a `MutationObserver`, a `ResizeObserver` and a `scroll` listener. The view gives it two data attributes (`data-ready` and `data-first-unread`). This is the largest piece of imperative code in the program. It does the work of 6 refs, 3 effects and 2 layout effects of the React code, and it is as hard to read. At least six of the Playwright tests of `chat.spec.ts` failed because of details of this Mount.
7. The Model must be an Effect Schema, so API types become `Schema.Unknown` casts (see section 3).
8. A Mount reads its arguments one time. The data for the Brief bubble and the list go through attributes, and the Mount reads them with an observer. This works, but it looks like the pattern that Foldkit wants to avoid. The program has five Mounts: `ComposerBehavior`, `ChatList`, `BriefLength`, `PanelOpened` and `PortalToBody`.
9. React inside Foldkit: `Runtime.embed` replaces the container element with its own root. A container that React owns breaks React. Workaround: a wrapper `div` that has only the container as child (see the comment in `web/src/Conversation.tsx`).
10. The type of a Mount Stream must list only the Messages that the Mount declares, so a `Stream.callback` needs a narrow type (`ComposerMessage`). This is a small cost, but each Mount repeats it.
11. A dialog inside the chat section hides the tray, because a Tailwind rule of the chat section (`group-has-[textarea:focus]/chat`) sees the focused field of the dialog. The React dialog is in a Radix portal. Workaround: the Mount `PortalToBody` moves the dialog to the body. This is the example of the Foldkit docs.
12. The cost of a faithful port. The Tailwind classes and the markup of each component came from the React code, and the first version of the port guessed them. 141 of 298 screenshots differed at that time, and about 50 specs failed. A port needs the exact classes, and a check by screenshot comparison after each component finds the errors early. The proof built a comparison tool for this (a few lines with the PNG reader of Playwright).

### Line counts

| | Lines |
|---|---|
| React code of the screen at the base commit: `Conversation.tsx` 977, `MessageInput.tsx` 507, `ActionCard.tsx` 247, `ActionTray.tsx` 73, `QuestionCard.tsx` 89, `InboxItemCard.tsx` 54, `AnswerPanel.tsx` 376, `Markdown.tsx` 40, `FileImage.tsx` 21 | 2,384 (and about 270 lines of `InboxRow` in `Inbox.tsx`, which the tray reuses) |
| Foldkit code: `web/src/chat/*.ts` without the test: `view` 1,463, `update` 1,227, `commands` 609, `composer` 490, `subscriptions` 341, `icons` 172, `model` 172, `scroll` 152, `message` 109, `program`, `ports`, `flags` and `ui` 95 | 4,830 |
| React host (`web/src/Conversation.tsx`) | 112 |
| Story tests | 595 |

The Foldkit code is about 1.8 times as long as the React code. Part of the difference is explicit code that React hides: each effect is a Command, and each change of state is a Message with a handler. Another part is a copy of the markup that a generator or shared helper can remove. `icons.ts` now has the data of the `lucide-react` icons, and `ui.ts` shares the class helpers (`buttonVariants` and `badgeVariants` moved to files that React and Foldkit both import).

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

- It works. The host in `web/src/Conversation.tsx` is 112 lines. The Flags, the inbound Ports and the outbound Ports carried everything that the chat screen needed.
- Each screen that is embedded adds a Foldkit runtime start (an Effect runtime, a Subscription system and one Port set). The size of Foldkit and Effect (144 kB gzip) is paid at the first screen, and later screens add only their code.
- Two programs on a page need care: a Port holds only data that a Schema can encode. A function, a DOM reference or a React element cannot cross it. The chat host keeps `head` and `note` in React for this reason. The wrapper `div` and the `key` on the host are needed (see Problems 2 and 9).
- The shared state is the hard part: the Inbox screen and the chat screen write the same draft store, the Workstream list and the unread counts live in React hooks, and `App.tsx` owns the `EventSource`. Each of them must cross the Port, or must move to `lib/` as a store with a subscription, before the second screen moves.
- The size grows during the move: React stays until the last screen moves. The bundle is the sum of both, so the Foldkit move is +167 kB gzip until the end.
- `main` stays releasable: each screen is a separate pull request, and the Playwright specs of that screen are the check. The screens that other Workstreams change (`Inbox.tsx`, `Pauses.tsx`, `Checkup.tsx`, and the files of voice) move last.

### Move in one step on a long branch

Facts from the proof:

- The proof screen took about two full sessions of agent time for 2,400 lines of React. The full client is 12,600 lines. A linear estimate is far above ten sessions, and the first screen is the one that has the new patterns (Mounts, Subscriptions, Ports), so the later screens may be faster.
- A long branch has no size cost in the middle. The bundle is smaller at the end, and the Foldkit router replaces `@tanstack/react-router` at once.
- The branch is behind `main` all the time, and other Workstreams change `Inbox.tsx`, `MessageInput.tsx`, `voice.ts`, `outbox.ts`, `Pauses.tsx` and `Checkup.tsx`. Each merge from `main` needs a manual port of those changes into Foldkit code. The proof already saw this risk: Workstream #1420 changes the files that the chat input uses.
- The specs of the whole client can check the branch only at the end, because the Playwright projects depend on each other (a failed project stops the next ones, as this proof saw: one failed spec left 258 tests that did not run).

Recommendation for the plan: move screen by screen with Embedding, in this order: chat (this proof), Inbox, Tasks, Checkup and the settings screens, Usage (with the SVG charts), and last the shell and the router. Fix the facts in section 7 first: ask for the directive option of `@foldkit/markdown`, and decide the compression of the Mobius server, because the size of the bundle is the main cost that the Owner can see.
