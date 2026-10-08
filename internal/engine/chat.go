package engine

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

var (
	//go:embed prompts/lead.md
	leadPrompt string
	//go:embed prompts/triager.md
	triagerPrompt string
)

const (
	// savePrompt is the last prompt of a Lead session.
	savePrompt = "Save in the Workstream memory what the next session needs."
	// historySize is the number of chat messages in the history of a first prompt.
	historySize = 20
	// maxCrashes is the number of new sessions that a chat starts after crashes. After that, the item of the crash
	// goes to the Inbox.
	maxCrashes = 3
	// memoryLines is the number of lines of MEMORY.md that a first prompt of the Lead shows.
	memoryLines = 200
	// leadFailedKind is the kind of the Inbox item of an item that the Lead did not take.
	leadFailedKind = "Lead failed"
)

// The authors of the chat messages, as the chat_messages table names them.
const (
	ownerAuthor      = "Owner"
	leadAuthor       = "Lead"
	tellOwnerAuthor  = "tell_owner"
	researcherAuthor = "Researcher"
	triagerAuthor    = "Triager"
	mobiusAuthor     = "Mobius"
	eventAuthor      = "Event"
)

// ChatKey names a chat: the Lead chat of a Workstream, or the Triager chat of an organization. The Triager chat has
// the empty repository and the Workstream 0.
type ChatKey struct {
	Organization string
	Repository   string
	Workstream   int64
}

func leadChat(repository string, workstream int64) ChatKey {
	organization, _, _ := strings.Cut(repository, "/")
	return ChatKey{organization, repository, workstream}
}

// Unread is the number of unread messages of a chat.
type Unread struct {
	Key   ChatKey
	Count int64
}

// ChatState is the state of a chat.
type ChatState struct {
	Key ChatKey
	// Writing is true while the agent of the chat has a turn that runs or an item that waits.
	Writing bool
	// Error is the error that ended the last session of the chat, or "".
	Error string
}

// ChatView is a chat with its messages.
type ChatView struct {
	Messages []store.ChatMessage
	Writing  bool
	// Harness is the Harness of the Lead, or of the Triager for the Triager chat.
	Harness config.Harness
}

// item is one turn of a chat: a chat message or a Lead event.
type item struct {
	message *store.ChatMessage
	event   *store.LeadEvent
}

// chat is a running Lead chat or Triager chat. One goroutine runs its sessions, one after the other.
//
// The fields after wake are guarded by e.chatsMu.
type chat struct {
	key ChatKey
	// ctx ends at the stop of the Workstream, and halt ends it.
	ctx  context.Context
	halt context.CancelFunc
	// done closes when the chat ends.
	done chan struct{}
	// tracked tells that the drain counts the first session of the chat from a tryTrack.
	tracked bool
	// wake gets a value after each new item and each request to close.
	wake chan struct{}

	// first is the item of the first turn of the next session.
	first item
	queue []item
	// wait is the context of the start of the next session, and a stop ends it with stopWait. stopWait is nil after
	// the start.
	wait     context.Context
	stopWait context.CancelFunc
	agent    *Agent
	// current is the item of the turn that runs, or nil. A crash in this turn sends the item again in a new session.
	current *item
	// held tells that the Lead called hold_event in the turn that runs.
	held bool
	// stoppable tells that the turn that runs answers a message of the Owner, so a stop cancels the turn.
	stoppable bool
	writing   bool
}

func (c *chat) notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// addChatMessage adds a chat message and sends it to the listeners. A message of the Researcher stays hidden. A
// message that is not of the Owner, of a Researcher or of an event also changes the unread count of the chat. When
// images is not empty, it is the directory of saveImages. The message gets the images before the listeners see it, and
// Mobius does not add it when the move fails.
func (e *Engine) addChatMessage(ctx context.Context, key ChatKey, author, text, images string) (store.ChatMessage, error) {
	message, err := e.queries.AddChatMessage(ctx, store.AddChatMessageParams{
		Organization: key.Organization,
		Repository:   key.Repository,
		Workstream:   key.Workstream,
		Author:       author,
		Time:         now(),
		Text:         text,
	})
	if err != nil {
		return message, err
	}
	if images != "" {
		if err := e.moveImages(images, message.ID); err != nil {
			return message, errors.Join(err, e.queries.DeleteChatMessage(ctx, message.ID))
		}
	}
	if author == researcherAuthor {
		return message, nil
	}
	e.publish(Change{Message: &message})
	if author == ownerAuthor || author == eventAuthor {
		return message, nil
	}
	return message, e.publishUnread(ctx, key)
}

func (e *Engine) publishUnread(ctx context.Context, key ChatKey) error {
	count, err := e.queries.CountUnread(ctx, store.CountUnreadParams{Organization: key.Organization, Repository: key.Repository, Workstream: key.Workstream})
	if err != nil {
		return err
	}
	e.publish(Change{Unread: &Unread{key, count}})
	return nil
}

// SendChat adds the message text of the Owner with its images to the chat, and gives it to the agent of the chat. It
// starts the agent when none runs. The text can be empty when the message has an image.
func (e *Engine) SendChat(ctx context.Context, key ChatKey, text string, images []Image) error {
	if !slices.Contains(e.github.Organizations(), key.Organization) {
		return refuse("Mobius has no repository in the organization \"%s\".", key.Organization)
	}
	if text == "" && len(images) == 0 {
		return refuse("A message must have text or an image.")
	}
	return e.postChat(ctx, key, ownerAuthor, text, images)
}

// postChat adds the message text of author with its images to the chat, and gives it to the agent of the chat. It
// starts the agent when none runs. A message that has images is not added when Mobius cannot keep them.
func (e *Engine) postChat(ctx context.Context, key ChatKey, author, text string, images []Image) error {
	if e.sealed() {
		return refuse("Mobius restarts for an upgrade. Send the message after the restart.")
	}
	var saved string
	if len(images) > 0 {
		var err error
		if saved, err = e.saveImages(images); err != nil {
			return err
		}
	}
	e.chatOrder.Lock()
	defer e.chatOrder.Unlock()
	message, err := e.addChatMessage(ctx, key, author, text, saved)
	if err != nil {
		if saved != "" {
			_ = os.RemoveAll(saved)
		}
		return err
	}
	e.give(key, false, item{message: &message})
	return nil
}

// give gives the items to the chat of key, and starts the chat when none runs. tracked tells that the drain counts the
// first session of a new chat from a tryTrack of the caller. The caller holds e.chatOrder.
func (e *Engine) give(key ChatKey, tracked bool, items ...item) {
	e.chatsMu.Lock()
	c, ok := e.chats[key]
	var agent *Agent
	if ok {
		c.queue = append(c.queue, items...)
		c.writing = true
		agent = c.agent
		c.notify()
		if tracked {
			e.untrack()
		}
	} else {
		ctx, halt := context.WithCancel(context.Background())
		c = &chat{key: key, ctx: ctx, halt: halt, wake: make(chan struct{}, 1), done: make(chan struct{}), first: items[0], queue: items[1:], writing: true, tracked: tracked}
		c.wait, c.stopWait = context.WithCancel(ctx)
		e.chats[key] = c
		go e.runChat(c)
	}
	e.chatsMu.Unlock()
	e.publish(Change{Chat: &ChatState{Key: key, Writing: true}})
	e.publishSession(agent)
}

// publishSession sends the session of agent, so the clients read its working state again. It does nothing for nil.
func (e *Engine) publishSession(agent *Agent) {
	if agent == nil {
		return
	}
	session, err := e.queries.GetSession(context.Background(), agent.id)
	if err != nil {
		log.Printf("read the chat session %d: %v", agent.id, err)
		return
	}
	e.publish(Change{Node: new(e.node(session))})
}

// addLeadEvent adds an event of kind about the issue for the Lead of the Workstream, with its entry in the chat, and
// gives the ready events to the Lead.
func (e *Engine) addLeadEvent(ctx context.Context, repository string, workstream int64, issue sql.NullInt64, kind, text string) error {
	e.chatOrder.Lock()
	defer e.chatOrder.Unlock()
	message, err := e.addChatMessage(ctx, leadChat(repository, workstream), eventAuthor, text, "")
	if err != nil {
		return err
	}
	err = e.queries.AddLeadEvent(ctx, store.AddLeadEventParams{
		Repository:  repository,
		Workstream:  workstream,
		Issue:       issue,
		Kind:        kind,
		Payload:     text,
		Time:        now(),
		ChatMessage: sql.NullInt64{Int64: message.ID, Valid: true},
	})
	if err != nil {
		return err
	}
	return e.sendEvents(ctx, repository, workstream)
}

// wakeEvents gives the ready events of the Workstream to its Lead.
func (e *Engine) wakeEvents(ctx context.Context, repository string, workstream int64) error {
	e.chatOrder.Lock()
	defer e.chatOrder.Unlock()
	return e.sendEvents(ctx, repository, workstream)
}

// wakeAllEvents gives the ready events of each Workstream to its Lead.
func (e *Engine) wakeAllEvents(ctx context.Context) error {
	waiting, err := e.queries.ListWaitingLeadWorkstreams(ctx)
	if err != nil {
		return err
	}
	for _, workstream := range waiting {
		if err := e.wakeEvents(ctx, workstream.Repository, workstream.Workstream); err != nil {
			return err
		}
	}
	return nil
}

// sendEvents gives each ready event of the Workstream to its Lead. A Lead that already has an event skips the copy
// at its turn. The drain holds each new Lead, so the events of a Workstream with no Lead stay undelivered. The caller
// holds e.chatOrder.
func (e *Engine) sendEvents(ctx context.Context, repository string, workstream int64) error {
	events, err := e.queries.ListReadyLeadEvents(ctx, store.ListReadyLeadEventsParams{Repository: repository, Workstream: workstream})
	if err != nil || len(events) == 0 {
		return err
	}
	key := leadChat(repository, workstream)
	e.chatsMu.Lock()
	_, running := e.chats[key]
	e.chatsMu.Unlock()
	if !running && !e.tryTrack() {
		return nil
	}
	items := make([]item, 0, len(events))
	for _, event := range events {
		items = append(items, item{event: &event})
	}
	e.give(key, !running, items...)
	return nil
}

// StopChat stops the turn of the chat that answers a message of the Owner. A turn for an event goes on. A chat
// whose session waits for its slot drops its first message, and it ends when no later item waits.
func (e *Engine) StopChat(ctx context.Context, key ChatKey) error {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	c, ok := e.chats[key]
	switch {
	case !ok:
		return nil
	case c.stopWait != nil && c.first.event != nil:
		return nil
	case c.stopWait != nil && len(c.queue) > 0:
		c.first, c.queue = c.queue[0], c.queue[1:]
		return nil
	case c.stopWait != nil:
		delete(e.chats, key)
		c.stopWait()
		return nil
	case c.stoppable:
		return c.agent.stop(ctx)
	}
	return nil
}

// stopLead ends the Lead chat of the Workstream and its Researchers at once.
func (e *Engine) stopLead(repository string, workstream int64) {
	e.stopResearchers(repository, workstream)
	key := leadChat(repository, workstream)
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	if c, ok := e.chats[key]; ok {
		delete(e.chats, key)
		c.halt()
	}
}

// stopAgents ends each chat and each Triager at once, and waits for their ends.
func (e *Engine) stopAgents() {
	var done []chan struct{}
	e.chatsMu.Lock()
	for key, c := range e.chats {
		delete(e.chats, key)
		c.halt()
		done = append(done, c.done)
	}
	e.chatsMu.Unlock()
	e.triagesMu.Lock()
	for _, triage := range e.triages {
		triage.stop()
		done = append(done, triage.done)
	}
	e.triagesMu.Unlock()
	for _, ch := range done {
		<-ch
	}
}

// hasLead tells if the Workstream has a Lead chat.
func (e *Engine) hasLead(repository string, workstream int64) bool {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	_, ok := e.chats[leadChat(repository, workstream)]
	return ok
}

// closeChats asks each chat to close when its work ends. A Lead saves its memory before the close.
func (e *Engine) closeChats() {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	for _, c := range e.chats {
		c.notify()
	}
}

// ChatView gives the messages of the chat of key.
func (e *Engine) ChatView(ctx context.Context, key ChatKey) (ChatView, error) {
	messages, err := e.queries.ListChatMessages(ctx, store.ListChatMessagesParams{Organization: key.Organization, Repository: key.Repository, Workstream: key.Workstream})
	if err != nil {
		return ChatView{}, err
	}
	view := ChatView{Messages: messages, Harness: e.config.Roles.Lead.Harness}
	if key.Workstream == 0 {
		view.Harness = e.config.Roles.Triager.Harness
	}
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	if c, ok := e.chats[key]; ok {
		view.Writing = c.writing
	}
	return view, nil
}

// SeeChat records that the Owner saw the messages of the chat up to the message id.
func (e *Engine) SeeChat(ctx context.Context, key ChatKey, message int64) error {
	err := e.queries.SetChatSeen(ctx, store.SetChatSeenParams{Organization: key.Organization, Repository: key.Repository, Workstream: key.Workstream, Message: message})
	if err != nil {
		return err
	}
	return e.publishUnread(ctx, key)
}

// UnreadChats gives each chat with unread messages.
func (e *Engine) UnreadChats(ctx context.Context) ([]Unread, error) {
	rows, err := e.queries.ListUnread(ctx)
	if err != nil {
		return nil, err
	}
	unread := make([]Unread, 0, len(rows))
	for _, row := range rows {
		unread = append(unread, Unread{ChatKey{row.Organization, row.Repository, row.Workstream}, row.Count})
	}
	return unread, nil
}

// runChat runs the sessions of the chat. After a crash, a new session gets the item of the failed turn as its first
// item, at most maxCrashes times. A crash on a full context does not count.
func (e *Engine) runChat(c *chat) {
	defer close(c.done)
	background := context.Background()
	crashes := 0
	spec, err := e.chatSpec(c.key)
	spec.Tracked = c.tracked
	if err != nil {
		if c.tracked {
			e.untrack()
		}
		e.finishChat(c, err.Error())
		return
	}
	for {
		e.chatsMu.Lock()
		wait := c.wait
		e.chatsMu.Unlock()
		a, err := e.Start(wait, spec)
		spec.Tracked = false
		e.chatsMu.Lock()
		stopWait := c.stopWait
		c.stopWait = nil
		c.agent = a
		first := c.first
		e.chatsMu.Unlock()
		stopped := wait.Err() != nil
		stopWait()
		if stopped {
			if err == nil {
				if err := a.End(background, "stopped"); err != nil {
					log.Printf("end the chat session %d: %v", a.ID(), err)
				}
			}
			e.finishChat(c, "")
			return
		}
		if err != nil {
			e.finishChat(c, err.Error())
			return
		}
		err = e.chat(c, a, first)
		if err == nil || c.ctx.Err() != nil {
			reason := "idle"
			if c.ctx.Err() != nil {
				reason = "stopped"
				e.finishChat(c, "")
			}
			if err := a.End(background, reason); err != nil {
				log.Printf("end the chat session %d: %v", a.ID(), err)
			}
			return
		}
		var failure error
		if errors.Is(err, errHung) {
			failure = errors.Join(err, a.endHung(background))
			crashes = maxCrashes + 1
		} else {
			failure = a.Fail(background, err)
			if !contextError(err) {
				crashes++
			}
		}
		e.chatsMu.Lock()
		current := c.current
		c.current = nil
		if current != nil && crashes <= maxCrashes {
			c.first = *current
			c.wait, c.stopWait = context.WithCancel(c.ctx)
		}
		e.chatsMu.Unlock()
		if current != nil && crashes <= maxCrashes {
			continue
		}
		if current != nil {
			if err := e.leadFailed(background, *current); err != nil {
				log.Printf("send the failed item of the chat session %d to the Inbox: %v", a.ID(), err)
			}
		}
		e.finishChat(c, failure.Error())
		return
	}
}

// chatSpec gives the session of the chat of key. It makes the directory of a Lead.
func (e *Engine) chatSpec(key ChatKey) (Spec, error) {
	if key.Workstream == 0 {
		return Spec{Role: TriagerRole, Organization: key.Organization}, nil
	}
	dir := leadDir(e.config.DataDir, key.Repository, key.Workstream)
	spec := Spec{Role: LeadRole, Organization: key.Organization, Repository: key.Repository, Workstream: key.Workstream, Dir: dir}
	return spec, os.MkdirAll(dir, 0o750)
}

// leadDir gives the directory of the Lead of the Workstream. The directory keeps the memory of the Lead.
func leadDir(dataDir, repository string, workstream int64) string {
	return filepath.Join(dataDir, "leads", repository, strconv.FormatInt(workstream, 10))
}

// contextError tells if err of a turn is a full context of the agent. Then a new session starts with a new first
// prompt, and the crash does not count.
func contextError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "prompt is too long") || strings.Contains(text, "context window") || strings.Contains(text, "context length")
}

// finishChat removes the chat, and sends its end with the error of its last session.
func (e *Engine) finishChat(c *chat, failure string) {
	e.chatsMu.Lock()
	if e.chats[c.key] == c {
		delete(e.chats, c.key)
	}
	e.chatsMu.Unlock()
	e.publish(Change{Chat: &ChatState{Key: c.key, Error: failure}})
}

// leadFailed sends the item that the Lead did not take to the Inbox. For an event, all ready events of the
// Workstream go to the Inbox, and they count as delivered.
func (e *Engine) leadFailed(ctx context.Context, failed item) error {
	if failed.message != nil {
		message := failed.message
		err := e.addInboxItem(ctx, store.AddInboxItemParams{
			Kind:         leadFailedKind,
			Organization: message.Organization,
			Repository:   message.Repository,
			Workstream:   message.Workstream,
			Issue:        message.Workstream,
			Text:         message.Text,
		})
		return err
	}
	repository, workstream := failed.event.Repository, failed.event.Workstream
	events, err := e.queries.ListReadyLeadEvents(ctx, store.ListReadyLeadEventsParams{Repository: repository, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, event := range events {
		organization, _, _ := strings.Cut(repository, "/")
		err := e.addInboxItem(ctx, store.AddInboxItemParams{
			Kind:         leadFailedKind,
			Organization: organization,
			Repository:   repository,
			Workstream:   workstream,
			Issue:        workstream,
			Text:         event.Payload,
		})
		if err != nil {
			return err
		}
		if err := e.queries.DeliverLeadEvent(ctx, store.DeliverLeadEventParams{DeliveredAt: sql.NullString{String: now(), Valid: true}, ID: event.ID}); err != nil {
			return err
		}
	}
	return nil
}

// chat runs the turns of the session a: first the item first, and then each queued item. When the chat is idle for
// lead_idle_timeout or the drain asks it to close, the Lead saves its memory and the session ends.
func (e *Engine) chat(c *chat, a *Agent, first item) error {
	ctx := c.ctx
	prompt, images, err := e.firstPrompt(ctx, c.key, first)
	if err != nil {
		return err
	}
	if err := e.itemTurn(c, a, first, prompt, images); err != nil {
		return err
	}
	for {
		next, ok := e.next(c)
		if ok {
			var prompt string
			var images []Image
			if next.message != nil {
				prompt, images, err = e.messagePrompt(*next.message)
				if err != nil {
					return err
				}
			} else {
				// The drain holds each event. The event stays undelivered until the drain ends.
				pending, err := e.pending(ctx, next.event)
				if err != nil {
					return err
				}
				if e.draining() || !pending {
					continue
				}
				prompt = "# Event\n\n" + next.event.Payload
			}
			if err := e.itemTurn(c, a, next, prompt, images); err != nil {
				return err
			}
			continue
		}
		closing, err := e.idle(c)
		if err != nil {
			return err
		}
		if !closing {
			continue
		}
		if c.key.Workstream != 0 {
			if err := e.turn(c, a, savePrompt, nil, true); err != nil {
				return err
			}
		}
		e.chatsMu.Lock()
		closed := len(c.queue) == 0
		if closed && e.chats[c.key] == c {
			delete(e.chats, c.key)
		}
		e.chatsMu.Unlock()
		if closed {
			return nil
		}
	}
}

// next takes the next queued item. With no item, the chat is not writing.
func (e *Engine) next(c *chat) (item, bool) {
	e.chatsMu.Lock()
	if len(c.queue) == 0 {
		stopped := c.writing
		c.writing = false
		agent := c.agent
		e.chatsMu.Unlock()
		if stopped {
			e.publish(Change{Chat: &ChatState{Key: c.key}})
			e.publishSession(agent)
		}
		return item{}, false
	}
	defer e.chatsMu.Unlock()
	next := c.queue[0]
	c.queue = c.queue[1:]
	return next, true
}

// idle waits for a new item, and gives true when the chat must close: after lead_idle_timeout, or at once while
// the drain is on.
func (e *Engine) idle(c *chat) (bool, error) {
	timeout := time.NewTimer(e.config.LeadIdleTimeout)
	defer timeout.Stop()
	for {
		e.chatsMu.Lock()
		queued := len(c.queue) > 0
		e.chatsMu.Unlock()
		switch {
		case queued:
			return false, nil
		case e.draining():
			return true, nil
		}
		select {
		case <-c.ctx.Done():
			return false, c.ctx.Err()
		case <-timeout.C:
			return true, nil
		case <-c.wake:
		}
	}
}

// pending tells if the event is ready: an earlier turn did not deliver or hold it.
func (e *Engine) pending(ctx context.Context, event *store.LeadEvent) (bool, error) {
	ready, err := e.queries.ListReadyLeadEvents(ctx, store.ListReadyLeadEventsParams{Repository: event.Repository, Workstream: event.Workstream})
	return slices.ContainsFunc(ready, func(other store.LeadEvent) bool { return other.ID == event.ID }), err
}

// itemTurn runs the turn of the item, and then ends the turn. The reply text of a turn for an event goes only to the
// Transcript, and a stop does not cancel that turn: the Owner cannot see it. The Lead uses tell_owner to write to the
// Owner. After a tell_owner call, the reply text of a turn for a message goes only to the Transcript.
func (e *Engine) itemTurn(c *chat, a *Agent, current item, prompt string, images []Image) error {
	e.chatsMu.Lock()
	c.current = &current
	e.chatsMu.Unlock()
	author := ""
	if current.message != nil {
		author = leadAuthor
		if c.key.Workstream == 0 {
			author = triagerAuthor
		}
	}
	a.setAuthor(author)
	if err := e.turn(c, a, prompt, images, current.message != nil); err != nil {
		return err
	}
	return e.endTurn(c, current)
}

// turn sends prompt and images to the agent and holds until the turn ends. A stop cancels a stoppable turn.
func (e *Engine) turn(c *chat, a *Agent, prompt string, images []Image, stoppable bool) error {
	e.chatsMu.Lock()
	c.stoppable = stoppable
	e.chatsMu.Unlock()
	defer func() {
		e.chatsMu.Lock()
		c.stoppable = false
		a.takeStop()
		e.chatsMu.Unlock()
	}()
	return a.Prompt(c.ctx, prompt, images)
}

// endTurn delivers the event of the turn, or holds it when the Lead called hold_event. The end of a turn for a
// message of the Owner frees each held event. The freed events and the ready events of their task issues go in
// front of the queue and replace their queued copies. Each other queued item keeps its place.
func (e *Engine) endTurn(c *chat, current item) error {
	ctx := c.ctx
	e.chatsMu.Lock()
	held := c.held
	c.current, c.held = nil, false
	e.chatsMu.Unlock()
	switch {
	case current.event != nil && held:
		return e.queries.HoldLeadEvent(ctx, current.event.ID)
	case current.event != nil:
		return e.queries.DeliverLeadEvent(ctx, store.DeliverLeadEventParams{DeliveredAt: sql.NullString{String: now(), Valid: true}, ID: current.event.ID})
	case current.message.Author != ownerAuthor:
		return nil
	}
	freed, err := e.queries.FreeLeadEvents(ctx, store.FreeLeadEventsParams{Repository: c.key.Repository, Workstream: c.key.Workstream})
	if err != nil || len(freed) == 0 {
		return err
	}
	ready, err := e.queries.ListReadyLeadEvents(ctx, store.ListReadyLeadEventsParams{Repository: c.key.Repository, Workstream: c.key.Workstream})
	if err != nil {
		return err
	}
	var affected []item
	for _, event := range ready {
		if slices.ContainsFunc(freed, func(other store.LeadEvent) bool {
			return other.ID == event.ID || other.Issue.Valid && other.Issue == event.Issue
		}) {
			affected = append(affected, item{event: &event})
		}
	}
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	c.queue = slices.DeleteFunc(c.queue, func(queued item) bool {
		return queued.event != nil && slices.ContainsFunc(affected, func(other item) bool { return other.event.ID == queued.event.ID })
	})
	c.queue = append(affected, c.queue...)
	return nil
}

// holdEvent holds the event of the turn of the Lead session of c until the end of the next turn for a message of the Owner.
func (e *Engine) holdEvent(_ context.Context, c caller, _ github.Repository, _ noInput) (string, error) {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	running, ok := e.chats[ChatKey{c.organization, c.repository, c.workstream}]
	if !ok || running.agent == nil || running.agent.ID() != c.session || running.current == nil || running.current.event == nil {
		return "", refuse("hold_event works only in a turn for an event.")
	}
	running.held = true
	return "Mobius holds the event. It sends the event again after your next reply to the Owner.", nil
}

// tellOwner adds text to the Lead chat as a message of the Lead to the Owner, and adds an Inbox item. The reply text
// after the call, in the same turn, goes only to the Transcript.
func (e *Engine) tellOwner(ctx context.Context, c caller, repository github.Repository, input tellInput) (string, error) {
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	issue, err := repository.Issue(ctx, c.workstream)
	if err != nil {
		return "", err
	}
	if issue == nil {
		return "", refuse("The Workstream issue does not exist.")
	}
	if _, err := e.addChatMessage(ctx, ChatKey{c.organization, c.repository, c.workstream}, tellOwnerAuthor, input.Text, ""); err != nil {
		return "", err
	}
	c.agent.setAuthor("")
	err = e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         leadKind,
		Organization: c.organization,
		Repository:   c.repository,
		Workstream:   c.workstream,
		Issue:        c.workstream,
		Text:         input.Text,
		Link:         issue.GetHTMLURL(),
	})
	return "Sent to the Owner.", err
}

// firstPrompt gives the first prompt of a session with the item first: the Role prompt, the context and the chat
// history before the item. It also gives the images of the item.
func (e *Engine) firstPrompt(ctx context.Context, key ChatKey, first item) (string, []Image, error) {
	var images []Image
	if first.message != nil {
		var err error
		if images, err = e.messageImages(first.message.ID); err != nil {
			return "", nil, err
		}
	}
	if key.Workstream == 0 {
		history, err := e.history(ctx, key, first.message.ID)
		if err != nil {
			return "", nil, err
		}
		workstreams, err := openWorkstreams(ctx, e.organizationRepositories(key.Organization))
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("%s\n%s\n%s# %s message\n\n%s", triagerPrompt, workstreams, history, first.message.Author, first.message.Text), images, nil
	}
	repository, err := e.repository(key.Repository)
	if err != nil {
		return "", nil, err
	}
	lead, err := e.leadContext(ctx, repository, key.Workstream)
	if err != nil {
		return "", nil, err
	}
	sections, err := e.repositorySections(ctx, repository, "lead")
	if err != nil {
		return "", nil, err
	}
	before, heading, text := int64(math.MaxInt64), "Event", ""
	if first.message != nil {
		before, heading, text = first.message.ID, first.message.Author+" message", first.message.Text
	} else {
		text = first.event.Payload
		if first.event.ChatMessage.Valid {
			before = first.event.ChatMessage.Int64
		}
	}
	history, err := e.history(ctx, key, before)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("%s\n%s%s%s# %s\n\n%s", leadPrompt, sections, lead, history, heading, text), images, nil
}

// leadContext gives the Brief, the memory and the task list of the Workstream.
func (e *Engine) leadContext(ctx context.Context, repository github.Repository, workstream int64) (string, error) {
	issue, err := repository.Issue(ctx, workstream)
	if err != nil {
		return "", err
	}
	if issue == nil {
		return "", errors.New("the Workstream issue does not exist")
	}
	memory, err := readMemory(leadDir(e.config.DataDir, repository.FullName, workstream))
	if err != nil {
		return "", err
	}
	lines, err := e.taskLines(ctx, repository, workstream)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Brief\n\n%s\n\n# MEMORY.md\n\n%s\n\n# Task list\n\n%s\n\n", issue.GetBody(), memory, taskText(lines)), nil
}

// readMemory gives MEMORY.md of the Lead directory dir, or "" when the file does not exist. A longer file than
// memoryLines lines gives its first lines and a request to make it shorter.
func readMemory(dir string) (string, error) {
	text, err := fs.ReadFile(os.DirFS(dir), "MEMORY.md")
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if len(lines) <= memoryLines {
		return string(text), nil
	}
	return strings.Join(lines[:memoryLines], "\n") + "\nMEMORY.md is too long. Make it shorter.\n", nil
}

// history gives the last historySize messages of the chat before the message id before.
func (e *Engine) history(ctx context.Context, key ChatKey, before int64) (string, error) {
	messages, err := e.queries.ListChatMessagesBefore(ctx, store.ListChatMessagesBeforeParams{
		Organization: key.Organization,
		Repository:   key.Repository,
		Workstream:   key.Workstream,
		ID:           before,
		Limit:        historySize,
	})
	if err != nil {
		return "", err
	}
	var history strings.Builder
	history.WriteString("# Chat history\n\n")
	for _, message := range slices.Backward(messages) {
		t, err := time.Parse(time.RFC3339Nano, message.Time)
		if err != nil {
			return "", err
		}
		text := message.Text
		if message.Author == ownerAuthor {
			count, err := e.ImageCount(message.ID)
			if err != nil {
				return "", err
			}
			if count > 0 && text != "" {
				text += "\n"
			}
			if count > 0 {
				text += "[" + imagesText(count) + "]"
			}
		}
		fmt.Fprintf(&history, "%s (%s):\n%s\n\n", message.Author, t.UTC().Format(timeFormat), text)
	}
	return history.String(), nil
}

// messagePrompt gives the prompt of a message and its images. A message of the Owner is its text.
func (e *Engine) messagePrompt(message store.ChatMessage) (string, []Image, error) {
	if message.Author == ownerAuthor {
		images, err := e.messageImages(message.ID)
		return message.Text, images, err
	}
	return fmt.Sprintf("# %s message\n\n%s", message.Author, message.Text), nil, nil
}
