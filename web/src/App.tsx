import { Outlet, useLocation, useMatch, useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  listGitHubApps,
  listOrganizations,
  type Activity as ActivityRow,
  type GitHubApp,
  type LiveEvents,
} from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { onEvent } from "@/lib/events";
import { useInbox } from "@/lib/inbox";
import { LoginContext } from "@/lib/login";
import { ShellContext } from "@/lib/shell";
import { useUnread } from "@/lib/unread";
import { useWorkstreams } from "@/lib/workstreams";
import { Frame } from "./Frame";
import { GitHub } from "./GitHub";
import { Login } from "./Login";

function savedOrganization() {
  try {
    return localStorage.getItem("organization") ?? "";
  } catch {
    return "";
  }
}

function saveOrganization(organization: string) {
  try {
    localStorage.setItem("organization", organization);
  } catch {
    // The page works with no saved organization.
  }
}

function App() {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const chat = useMatch({
    from: "/workstreams/$owner/$name/$number",
    shouldThrow: false,
  });
  const chatOwner = useRef(chat?.params.owner);
  const [loginShown, setLoginShown] = useState(false);
  const [apps, setApps] = useState<GitHubApp[]>();
  const [organizations, setOrganizations] = useState<string[]>([]);
  const [organization, setOrganization] = useState("");
  const [error, setError] = useState<string>();
  const [source, setSource] = useState<EventSource>();
  const [activities, setActivities] = useState<ActivityRow[]>([]);
  const live = !loginShown && apps !== undefined && apps.length > 0;
  const showLogin = useCallback(() => setLoginShown(true), []);
  const workstreams = useWorkstreams(showLogin, source);
  const unread = useUnread(source);
  const inbox = useInbox(source);

  useEffect(() => {
    chatOwner.current = chat?.params.owner;
  }, [chat?.params.owner]);

  useEffect(() => {
    if (!live) {
      return;
    }
    const connect = () => {
      const events = new EventSource("/api/events");
      events.addEventListener("open", () => setSource(events), { once: true });
      // The server sends the latest activities when the connection opens, before the pages can listen. When the
      // browser connects again, the server sends only the activities after the last event id.
      onEvent<LiveEvents, "activity">(events, "activity", (activity) =>
        setActivities((list) =>
          list.some((other) => other.id === activity.id) ? list : [...list, activity],
        ),
      );
      return events;
    };
    let events = connect();
    // A phone that stops the page in the background can leave a live connection open with no error. Thus the page
    // connects again when it is visible again or when the browser is online again.
    const wake = () => {
      if (!document.hidden) {
        events.close();
        events = connect();
      }
    };
    window.addEventListener("online", wake);
    document.addEventListener("visibilitychange", wake);
    return () => {
      window.removeEventListener("online", wake);
      document.removeEventListener("visibilitychange", wake);
      events.close();
    };
  }, [live]);

  const load = useCallback(() => {
    listGitHubApps()
      .then((res) => {
        if (res.status === 401) {
          setLoginShown(true);
        } else if (res.status === 200) {
          setApps(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
    listOrganizations()
      .then((res) => {
        if (res.status === 200) {
          const list = res.data.data;
          const owner = chatOwner.current;
          if (owner !== undefined && list.includes(owner)) {
            saveOrganization(owner);
          }
          const saved = savedOrganization();
          setOrganizations(list);
          setOrganization((current) =>
            list.includes(current) ? current : list.includes(saved) ? saved : (list[0] ?? ""),
          );
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, []);

  useEffect(() => {
    if (!loginShown) {
      load();
    }
  }, [loginShown, load]);

  useEffect(() => {
    if (source) {
      return onEvent<LiveEvents, "repositories">(source, "repositories", load);
    }
  }, [source, load]);

  const selectOrganization = (name: string) => {
    saveOrganization(name);
    setOrganization(name);
    if (chat !== undefined && chat.params.owner !== name) {
      void navigate({ to: "/workstreams" });
    }
  };

  if (loginShown) {
    return <Login onLogin={() => setLoginShown(false)} />;
  }
  if (error) {
    return (
      <main className="p-6">
        <Badge variant="destructive">{error}</Badge>
      </main>
    );
  }
  if (!apps) {
    return null;
  }
  if (apps.length === 0) {
    return (
      <LoginContext value={showLogin}>
        <main className="flex min-h-svh items-center justify-center p-6">
          <GitHub apps={apps} />
        </main>
      </LoginContext>
    );
  }
  return (
    <LoginContext value={showLogin}>
      <Frame
        path={pathname}
        organizations={organizations}
        organization={organization}
        onSelect={selectOrganization}
        source={source}
        workstreams={workstreams}
        unread={unread ?? []}
        inbox={inbox}
        fill={chat !== undefined || pathname === "/workstreams/new"}
      >
        <ShellContext
          value={{
            apps,
            organizations,
            organization,
            source,
            workstreams,
            unread,
            inbox,
            activities,
          }}
        >
          <Outlet />
        </ShellContext>
      </Frame>
    </LoginContext>
  );
}

export default App;
