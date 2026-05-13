package store

import (
	"encoding/json"
	"sync"
	"time"
)

type RequestRecord struct {
	ID         string        `json:"id"`
	User       string        `json:"user"`
	Client     string        `json:"client"`
	Model      string        `json:"model"`
	Path       string        `json:"path"`
	Status     int           `json:"status"`
	Error      string        `json:"error,omitempty"`
	StartedAt  time.Time     `json:"startedAt"`
	FinishedAt time.Time     `json:"finishedAt,omitempty"`
	Duration   time.Duration `json:"duration"`
}

type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
	Time time.Time   `json:"time"`
}

type Store struct {
	mu       sync.RWMutex
	recent   []RequestRecord
	active   map[string]activeRecord
	subs     map[chan Event]struct{}
	capacity int
}

type activeRecord struct {
	record RequestRecord
	cancel func()
}

func New(capacity int) *Store {
	return &Store{
		active:   make(map[string]activeRecord),
		subs:     make(map[chan Event]struct{}),
		capacity: capacity,
	}
}

func (s *Store) Start(record RequestRecord, cancel func()) {
	s.mu.Lock()
	s.active[record.ID] = activeRecord{record: record, cancel: cancel}
	s.mu.Unlock()
	s.publish("request_started", record)
}

func (s *Store) Finish(id string, status int, errText string) {
	s.mu.Lock()
	entry, ok := s.active[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.active, id)
	record := entry.record
	record.Status = status
	record.Error = errText
	record.FinishedAt = time.Now()
	record.Duration = time.Since(record.StartedAt)
	s.recent = append([]RequestRecord{record}, s.recent...)
	if len(s.recent) > s.capacity {
		s.recent = s.recent[:s.capacity]
	}
	s.mu.Unlock()
	s.publish("request_finished", record)
}

func (s *Store) Snapshot() (active []RequestRecord, recent []RequestRecord) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	active = make([]RequestRecord, 0, len(s.active))
	for _, entry := range s.active {
		active = append(active, entry.record)
	}
	recent = make([]RequestRecord, 0, len(s.recent))
	recent = append(recent, s.recent...)
	return active, recent
}

func (s *Store) Stop(id string) bool {
	s.mu.RLock()
	entry, ok := s.active[id]
	s.mu.RUnlock()
	if !ok || entry.cancel == nil {
		return false
	}
	entry.cancel()
	s.publish("request_stopped", entry.record)
	return true
}

func (s *Store) DeleteRecent(id string) bool {
	s.mu.Lock()
	for i, record := range s.recent {
		if record.ID == id {
			s.recent = append(s.recent[:i], s.recent[i+1:]...)
			s.mu.Unlock()
			s.publish("usage_deleted", map[string]string{"id": id})
			return true
		}
	}
	s.mu.Unlock()
	return false
}

func (s *Store) ClearRecent() {
	s.mu.Lock()
	s.recent = nil
	s.mu.Unlock()
	s.publish("usage_cleared", nil)
}

func (s *Store) ClearOlderThan(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.recent[:0]
	removed := 0
	for _, record := range s.recent {
		t := record.FinishedAt
		if t.IsZero() {
			t = record.StartedAt
		}
		if t.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, record)
	}
	s.recent = kept
	return removed
}

func (s *Store) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

func (s *Store) Publish(kind string, data interface{}) {
	s.publish(kind, data)
}

func (s *Store) publish(kind string, data interface{}) {
	event := Event{Type: kind, Data: data, Time: time.Now()}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func (e Event) JSON() []byte {
	b, _ := json.Marshal(e)
	return b
}
