package engine

import (
	"strings"
	"time"
)

// waitChatBackground holds the idle chat open while a background task of the session runs. It sends
// chatBackgroundPrompt, and again each backgroundCheckInterval, until the agent calls background_state with done. The
// reply text of the turn with done goes to the chat. started is the time of the first prompt of the session, and
// waitChatBackground gives true when the chat must close: the drain started, or backgroundTimeout passed. A queued
// item ends the wait at once, also during the sleep between two prompts, and gives false.
func (e *Engine) waitChatBackground(c *chat, a *Agent, started *time.Time) (bool, error) {
	if started.IsZero() {
		*started = time.Now()
	}
	for {
		if time.Since(*started) >= backgroundTimeout {
			return true, a.addNote(c.ctx, backgroundTimeoutNote())
		}
		a.setAuthor("")
		if err := e.turn(c, a, chatBackgroundPrompt, nil, false); err != nil {
			return false, err
		}
		if !a.backgroundRunning() {
			*started = time.Time{}
			return false, e.tellBackgroundResult(c, a)
		}
		closing, queued, err := e.sleepBackground(c)
		if err != nil || closing || queued {
			return closing, err
		}
	}
}

// sleepBackground holds for backgroundCheckInterval. It ends early when an item is queued or the drain starts.
func (e *Engine) sleepBackground(c *chat) (closing, queued bool, err error) {
	timer := time.NewTimer(backgroundCheckInterval)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return false, false, c.ctx.Err()
		case <-timer.C:
			return false, false, nil
		case <-c.wake:
		}
		e.chatsMu.Lock()
		queued := len(c.queue) > 0
		e.chatsMu.Unlock()
		if queued || e.draining() {
			return !queued, queued, nil
		}
	}
}

// tellBackgroundResult adds the reply text of the last turn of a to the chat, unless it is empty.
func (e *Engine) tellBackgroundResult(c *chat, a *Agent) error {
	a.mu.Lock()
	text := strings.TrimSpace(a.reply.String())
	a.mu.Unlock()
	if text == "" {
		return nil
	}
	author := leadAuthor
	if c.key.Workstream == 0 {
		author = coordinatorAuthor
	}
	_, err := e.addChatMessage(c.ctx, c.key, author, text, "", "", nil)
	return err
}

// backgroundRunning tells if a background task of the session still runs, by the last state of the agent.
func (a *Agent) backgroundRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.background
}
