package engine

import (
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// keepTime is the time that Mobius keeps the Harness session of an Implementer round open for the next round of the
// task. The prompt cache of the Harness stays valid in that time.
const keepTime = 60 * time.Minute

// beforeKeep runs after the row of the done round ends and before the engine stores the kept session. A test uses it
// to stop the task in the window between the end of the round and the keep.
var beforeKeep = func(int64, *runner.Session) {}

// keptSession is the open Harness process of an Implementer round that a later round of the same task continues. The
// row of the earlier round is ended, so a kept session holds no Worker slot.
type keptSession struct {
	session *runner.Session
	key     string
	// binding is the Role binding of the Harness process. A round with another binding starts a new process.
	binding config.RoleBinding
	// keptAt is the time of the keep, from e.timeNow.
	keptAt    time.Time
	costTotal float64
	costBase  float64
	// dshRecords is the number of assistant/message records of the dsh session log that the earlier rows counted. The
	// log belongs to the ACP session, so a continued row reads only the later records.
	dshRecords int
	// brief, issue, comments and report are the texts that the session got, so a later short prompt sends only a change.
	brief    string
	issue    string
	comments string
	// report is the report text that the session knows, from its prompt or from its own submit_report call.
	report string
}

// keepSession keeps k for the next round of the task, and closes the earlier kept session of the task.
func (e *Engine) keepSession(task int64, k *keptSession) {
	e.keptMu.Lock()
	old := e.kept[task]
	e.kept[task] = k
	e.keptMu.Unlock()
	if old != nil {
		e.closeKeptSession(old)
	}
}

// closeKeptSession closes a kept session and its MCP key.
func (e *Engine) closeKeptSession(k *keptSession) {
	k.session.Close()
	e.agents.MCP.Close(k.key)
}

// closeKept closes and removes the kept session of the task, when it has one.
func (e *Engine) closeKept(task int64) {
	e.keptMu.Lock()
	k := e.kept[task]
	delete(e.kept, task)
	e.keptMu.Unlock()
	if k != nil {
		e.closeKeptSession(k)
	}
}

// takeKeptSession gives the kept session of the task for a round with binding, and removes it. It closes the kept
// session when the round cannot use it.
func (e *Engine) takeKeptSession(task int64, binding config.RoleBinding) *keptSession {
	e.keptMu.Lock()
	k := e.kept[task]
	delete(e.kept, task)
	e.keptMu.Unlock()
	if k == nil {
		return nil
	}
	if e.timeNow().Sub(k.keptAt) >= keepTime || !k.session.Alive() || k.binding != binding {
		e.closeKeptSession(k)
		return nil
	}
	return k
}

// closeKeptSessions closes each kept session. The Engine calls it at its stop, and the drain before the restart.
func (e *Engine) closeKeptSessions() {
	e.keptMu.Lock()
	kept := e.kept
	e.kept = map[int64]*keptSession{}
	e.keptMu.Unlock()
	for _, k := range kept {
		e.closeKeptSession(k)
	}
}

// expireKeptSessions closes each kept session whose keepTime ended or whose Harness process ended. The poll calls it.
func (e *Engine) expireKeptSessions() {
	now := e.timeNow()
	e.keptMu.Lock()
	var expired []*keptSession
	for task, k := range e.kept {
		if now.Sub(k.keptAt) >= keepTime || !k.session.Alive() {
			expired = append(expired, k)
			delete(e.kept, task)
		}
	}
	e.keptMu.Unlock()
	for _, k := range expired {
		e.closeKeptSession(k)
	}
}
