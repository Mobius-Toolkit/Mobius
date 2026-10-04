import { ChevronRightIcon } from 'lucide-react'
import {
  use,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import {
  getChat,
  seeChat,
  sendChat,
  stopChat,
  type ChatMessage,
  type LiveEvents,
  type Workstream,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Textarea } from '@/components/ui/textarea'
import { onEvent } from '@/lib/events'
import { LoginContext } from '@/lib/login'
import { clock } from '@/lib/time'
import { sameChat } from '@/lib/unread'
import { cn } from '@/lib/utils'
import { Markdown } from './Markdown'

function upsert(list: ChatMessage[], message: ChatMessage) {
  const known = list.find((other) => other.id === message.id)
  // The agent only adds text to a message, so the longer text is the newer text.
  if (known && known.text.length >= message.text.length) {
    return list
  }
  return [...list.filter((other) => other.id !== message.id), message].toSorted(
    (a, b) => a.id - b.id,
  )
}

function Message({ message }: { message: ChatMessage }) {
  const event = message.author === 'Event'
  const [summary, ...rest] = message.text.split('\n')
  const body = rest.join('\n').trim()
  return (
    <div
      data-message={message.id}
      className={cn(
        'grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 rounded-xl border px-3 py-2',
        message.author === 'Owner'
          ? 'justify-self-end border-transparent bg-secondary'
          : 'justify-self-start bg-card',
        event && 'border-dashed bg-transparent text-muted-foreground',
      )}
    >
      <div className="flex gap-2 text-xs text-muted-foreground">
        <span>{message.author === 'tell_owner' ? 'Lead' : message.author}</span>
        <span>{clock(message.time)}</span>
      </div>
      {event && body ? (
        <Collapsible>
          <CollapsibleTrigger className="group flex min-w-0 items-start gap-1 text-left break-words">
            <ChevronRightIcon className="mt-0.5 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
            <span className="min-w-0">{summary}</span>
          </CollapsibleTrigger>
          <CollapsibleContent className="pt-2">
            <Markdown text={body} />
          </CollapsibleContent>
        </Collapsible>
      ) : (
        <Markdown text={message.text} />
      )}
    </div>
  )
}

// The chat of the agent: a Lead chat, or the Triager chat with the empty repository and the Workstream 0. unread is
// undefined until the first read of the unread counts.
export function Conversation({
  organization,
  repository,
  workstream,
  agent,
  source,
  unread,
  head,
  tail,
  brief,
  note,
  footer,
}: {
  organization: string
  repository: string
  workstream: number
  agent: string
  source?: EventSource
  unread?: number
  head: ReactNode
  tail?: ReactNode
  brief?: Workstream
  note?: ReactNode
  footer?: ReactNode
}) {
  const showLogin = use(LoginContext)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [loaded, setLoaded] = useState(false)
  const [harness, setHarness] = useState('')
  const [writing, setWriting] = useState(false)
  const [failure, setFailure] = useState('')
  const [error, setError] = useState<string>()
  const [text, setText] = useState('')
  const [sendError, setSendError] = useState('')
  const [sending, setSending] = useState(false)
  const [briefOpen, setBriefOpen] = useState<boolean>()
  const listRef = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)
  // The number of messages at the last scroll, or undefined before the first scroll.
  const scrolledCount = useRef<number>(undefined)

  const load = useCallback(() => {
    getChat({ organization, repository, workstream })
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 200) {
          const chat = res.data.data
          setMessages((list) => chat.messages.reduce(upsert, list))
          setHarness(chat.harness)
          setWriting(chat.writing)
          setLoaded(true)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [organization, repository, workstream, showLogin])

  // A message that comes while the connection is down is lost, so each connection reads the chat.
  useEffect(() => {
    if (!source) {
      return
    }
    const key = { organization, repository, workstream }
    load()
    source.addEventListener('open', load)
    const removeMessage = onEvent<LiveEvents, 'message'>(
      source,
      'message',
      (message) => {
        if (sameChat(message, key)) {
          setMessages((list) => upsert(list, message))
        }
      },
    )
    const removeState = onEvent<LiveEvents, 'chat'>(source, 'chat', (state) => {
      if (sameChat(state, key)) {
        setWriting(state.writing)
        setFailure(state.error)
      }
    })
    return () => {
      source.removeEventListener('open', load)
      removeMessage()
      removeState()
    }
  }, [source, load, organization, repository, workstream])

  const lastAgentMessage = messages.findLast(
    (message) => message.author !== 'Owner',
  )?.id
  useEffect(() => {
    if (unread && lastAgentMessage !== undefined) {
      // A failed call keeps the count, and the next message of the agent calls again.
      seeChat({
        organization,
        repository,
        workstream,
        message: lastAgentMessage,
      }).catch(() => {})
    }
  }, [unread, lastAgentMessage, organization, repository, workstream])

  // The first scroll shows the first unread message, or the end. Then a new message scrolls to the end, and a longer
  // last message scrolls only while the Owner is at the end.
  useLayoutEffect(() => {
    const list = listRef.current
    if (!list || !loaded || unread === undefined) {
      return
    }
    if (scrolledCount.current === undefined) {
      scrolledCount.current = messages.length
      const unreadMessages = messages.filter(
        (message) => message.author !== 'Owner' && message.author !== 'Event',
      )
      const first =
        unread > 0 &&
        unreadMessages[Math.max(0, unreadMessages.length - unread)]
      const element =
        first && list.querySelector(`[data-message="${first.id}"]`)
      if (element) {
        list.scrollTop +=
          element.getBoundingClientRect().top - list.getBoundingClientRect().top
        return
      }
      list.scrollTop = list.scrollHeight
      return
    }
    if (messages.length > scrolledCount.current) {
      pinned.current = true
    }
    scrolledCount.current = messages.length
    if (pinned.current) {
      list.scrollTop = list.scrollHeight
    }
  })

  const send = () => {
    if (sending || !text.trim()) {
      return
    }
    const sent = text
    setSending(true)
    setText('')
    sendChat({ organization, repository, workstream, text: sent })
      .then((res) => {
        if (res.status === 204) {
          setSendError('')
          return
        }
        setText((current) => sent + current)
        if (res.status === 401) {
          showLogin()
        } else {
          setSendError(res.data.error)
        }
      })
      .catch((err: unknown) => {
        setText((current) => sent + current)
        setSendError(String(err))
      })
      .finally(() => setSending(false))
  }

  const stop = () => {
    stopChat({ organization, repository, workstream })
      .then((res) => {
        if (res.status !== 204) {
          setSendError(res.data.error)
        }
      })
      .catch((err: unknown) => setSendError(String(err)))
  }

  return (
    <section className="flex min-h-0 min-w-0 grow flex-col">
      <header className="flex min-h-14 items-center gap-2 border-b px-4 py-2">
        {head}
        <span className="grow" />
        {harness && (
          <span className="truncate text-sm text-muted-foreground">
            {agent}: {harness}
          </span>
        )}
        {tail}
      </header>
      {brief && (
        <Collapsible
          open={briefOpen ?? (loaded && messages.length === 0)}
          onOpenChange={setBriefOpen}
          className="border-b px-4 py-2"
        >
          <CollapsibleTrigger className="group flex w-full items-center gap-2 text-left font-medium">
            <span className="grow truncate">{brief.title}</span>
            <ChevronRightIcon className="size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
          </CollapsibleTrigger>
          <CollapsibleContent className="max-h-[40svh] overflow-y-auto pt-2">
            <Markdown text={brief.brief} />
          </CollapsibleContent>
        </Collapsible>
      )}
      {note}
      <div
        ref={listRef}
        onScroll={(event) => {
          const list = event.currentTarget
          pinned.current =
            list.scrollHeight - list.scrollTop - list.clientHeight < 40
        }}
        className="grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-3 overflow-y-auto bg-muted/40 p-4"
      >
        {error && <Badge variant="destructive">{error}</Badge>}
        {loaded && messages.length === 0 && (
          <p className="text-center text-sm text-muted-foreground">
            No messages. Write to start a chat session.
          </p>
        )}
        {messages.map((message) => (
          <Message key={message.id} message={message} />
        ))}
        {writing && (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className="size-2 animate-pulse rounded-full bg-green-600" />
            The {agent} writes a reply.
          </p>
        )}
        {failure && (
          <Badge
            variant="destructive"
            className="h-auto w-full justify-start whitespace-normal"
          >
            The chat session failed: {failure}
          </Badge>
        )}
      </div>
      {footer}
      <form
        className="flex items-end gap-2 border-t p-3"
        onSubmit={(event) => {
          event.preventDefault()
          send()
        }}
      >
        <div className="grid grow gap-1">
          <Textarea
            rows={1}
            aria-label={`Message to the ${agent}`}
            placeholder={`Write to the ${agent}`}
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              // On a touch screen, Enter adds a line and only the Send button sends. Safari gives the Enter that
              // ends an IME composition with isComposing false and keyCode 229.
              if (
                event.key === 'Enter' &&
                !event.shiftKey &&
                !event.nativeEvent.isComposing &&
                event.keyCode !== 229 &&
                !window.matchMedia('(pointer: coarse)').matches
              ) {
                event.preventDefault()
                send()
              }
            }}
            className="max-h-40 min-h-9 resize-none"
          />
          {sendError && <p className="text-sm text-destructive">{sendError}</p>}
        </div>
        {/* A button that takes the focus closes the keyboard of a phone, and the button moves before the click. */}
        {writing && (
          <Button
            type="button"
            variant="destructive"
            onMouseDown={(event) => event.preventDefault()}
            onClick={stop}
          >
            Stop
          </Button>
        )}
        <Button
          type="submit"
          disabled={sending}
          onMouseDown={(event) => event.preventDefault()}
        >
          Send
        </Button>
      </form>
    </section>
  )
}
