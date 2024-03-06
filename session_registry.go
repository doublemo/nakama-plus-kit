package nakamapluskit

import (
	"sync"

	"go.uber.org/atomic"
)

type SessionRegistry struct {
	sessions     *MapOf[string, Session]
	sessionRoles map[string]map[string]bool
	sessionCount *atomic.Int32
	sync.RWMutex
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions:     &MapOf[string, Session]{},
		sessionRoles: make(map[string]map[string]bool),
		sessionCount: atomic.NewInt32(0),
	}
}

func (r *SessionRegistry) Count() int {
	return int(r.sessionCount.Load())
}

func (r *SessionRegistry) Get(sessionID string) Session {
	session, ok := r.sessions.Load(sessionID)
	if !ok {
		return nil
	}
	return session
}

func (r *SessionRegistry) Add(session Session) {
	r.sessions.Store(session.ID(), session)
	r.Lock()
	m, ok := r.sessionRoles[session.Role()]
	if !ok {
		r.sessionRoles[session.Role()] = map[string]bool{session.ID(): true}
	} else {
		if m[session.ID()] {
			r.Unlock()
			return
		}
		m[session.ID()] = true
	}
	r.Unlock()
	r.sessionCount.Inc()
}

func (r *SessionRegistry) Remove(sessionID string) {
	session, ok := r.sessions.Load(sessionID)
	if !ok {
		return
	}

	r.sessions.Delete(sessionID)
	r.sessionCount.Dec()
	r.Lock()
	m, ok := r.sessionRoles[session.Role()]
	if !ok {
		r.Unlock()
		return
	}
	delete(m, sessionID)
	if size := len(r.sessionRoles[session.Role()]); size < 1 {
		delete(r.sessionRoles, session.Role())
	}
	r.Unlock()
}

func (r *SessionRegistry) Range(fn func(Session) bool) {
	r.sessions.Range(func(id string, session Session) bool {
		return fn(session)
	})
}

func (r *SessionRegistry) RangeRole(role string, fn func(Session) bool) {
	r.RLock()
	m, ok := r.sessionRoles[role]
	if !ok {
		r.RUnlock()
		return
	}

	for sessionID := range m {
		r.RUnlock()
		session, ok := r.sessions.Load(sessionID)
		if ok {
			fn(session)
		}
		r.RLock()
	}
	r.RUnlock()
}
