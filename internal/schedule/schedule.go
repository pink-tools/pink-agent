package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("schedule not found")

type Schedule struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	ThreadID  int    `json:"threadId"`
	TriggerAt int64  `json:"triggerAt"`
	Prompt    string `json:"prompt"`
	CreatedAt int64  `json:"createdAt"`
}

type Schedules struct {
	Items []Schedule `json:"items"`
}

func (s *Schedules) clone() *Schedules {
	items := make([]Schedule, len(s.Items))
	copy(items, s.Items)
	return &Schedules{Items: items}
}

type Storage struct{ path string }

func NewStorage(path string) *Storage { return &Storage{path: path} }

func (s *Storage) Load() (*Schedules, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &Schedules{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return &Schedules{}, nil
	}
	var ss Schedules
	if err := json.Unmarshal(data, &ss); err != nil {
		return nil, err
	}
	return &ss, nil
}

func (s *Storage) Save(ss *Schedules) error {
	data, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type TriggerFunc func(Schedule)

type cmd struct {
	fn     func(*Schedules) (*Schedules, error)
	result chan error
}

type Manager struct {
	storage *Storage
	state   atomic.Pointer[Schedules]
	cmds    chan cmd
	done    chan struct{}

	trigger atomic.Pointer[TriggerFunc]
	wakeup  chan struct{}
	stop    chan struct{}
}

func NewManager(storage *Storage) (*Manager, error) {
	ss, err := storage.Load()
	if err != nil {
		return nil, err
	}
	m := &Manager{
		storage: storage,
		cmds:    make(chan cmd, 16),
		done:    make(chan struct{}),
		wakeup:  make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	m.state.Store(ss)
	go m.commandLoop()
	return m, nil
}

func (m *Manager) commandLoop() {
	defer close(m.done)
	for c := range m.cmds {
		cur := m.state.Load()
		next, err := c.fn(cur)
		if err != nil {
			c.result <- err
			continue
		}
		if next != nil {
			m.state.Store(next)
		}
		c.result <- nil
		m.notify()
	}
}

func (m *Manager) notify() {
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
}

func (m *Manager) mutate(fn func(*Schedules) (*Schedules, error)) error {
	res := make(chan error, 1)
	m.cmds <- cmd{fn: fn, result: res}
	return <-res
}

// Run starts the trigger loop. Past-due schedules fire immediately.
func (m *Manager) Run(trigger TriggerFunc) {
	m.trigger.Store(&trigger)
	go m.triggerLoop()
}

func (m *Manager) triggerLoop() {
	for {
		next, wait := m.nextSchedule()
		if next == nil {
			select {
			case <-m.wakeup:
				continue
			case <-m.stop:
				return
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
			m.fire(*next)
		case <-m.wakeup:
			timer.Stop()
		case <-m.stop:
			timer.Stop()
			return
		}
	}
}

func (m *Manager) nextSchedule() (*Schedule, time.Duration) {
	ss := m.state.Load()
	if len(ss.Items) == 0 {
		return nil, 0
	}
	var earliest *Schedule
	for i := range ss.Items {
		s := &ss.Items[i]
		if earliest == nil || s.TriggerAt < earliest.TriggerAt {
			earliest = s
		}
	}
	wait := time.Until(time.Unix(earliest.TriggerAt, 0))
	if wait < 0 {
		wait = 0
	}
	return earliest, wait
}

func (m *Manager) fire(s Schedule) {
	if err := m.removeByID(s.ID); err != nil {
		return
	}
	fn := m.trigger.Load()
	if fn != nil {
		(*fn)(s)
	}
}

func (m *Manager) removeByID(id string) error {
	return m.mutate(func(cur *Schedules) (*Schedules, error) {
		next := cur.clone()
		idx := -1
		for i, s := range next.Items {
			if s.ID == id {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, ErrNotFound
		}
		next.Items = append(next.Items[:idx], next.Items[idx+1:]...)
		return next, m.storage.Save(next)
	})
}

func (m *Manager) Add(projectID string, threadID int, when, prompt string) (Schedule, error) {
	triggerAt, err := ParseWhen(when, time.Now())
	if err != nil {
		return Schedule{}, err
	}
	s := Schedule{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		ThreadID:  threadID,
		TriggerAt: triggerAt.Unix(),
		Prompt:    prompt,
		CreatedAt: time.Now().Unix(),
	}
	err = m.mutate(func(cur *Schedules) (*Schedules, error) {
		next := cur.clone()
		next.Items = append(next.Items, s)
		return next, m.storage.Save(next)
	})
	return s, err
}

func (m *Manager) Cancel(id string) error {
	return m.removeByID(id)
}

func (m *Manager) CancelByProject(projectID string) (int, error) {
	var count int
	err := m.mutate(func(cur *Schedules) (*Schedules, error) {
		next := cur.clone()
		kept := make([]Schedule, 0, len(next.Items))
		for _, s := range next.Items {
			if s.ProjectID == projectID {
				count++
				continue
			}
			kept = append(kept, s)
		}
		next.Items = kept
		return next, m.storage.Save(next)
	})
	return count, err
}

func (m *Manager) List() []Schedule {
	items := m.state.Load().Items
	out := make([]Schedule, len(items))
	copy(out, items)
	return out
}

func (m *Manager) ListByProject(projectID string) []Schedule {
	var out []Schedule
	for _, s := range m.state.Load().Items {
		if s.ProjectID == projectID {
			out = append(out, s)
		}
	}
	return out
}

func (m *Manager) Close() {
	close(m.stop)
	close(m.cmds)
	<-m.done
}

var dayPattern = regexp.MustCompile(`(\d+)d`)

// ParseWhen parses "1h", "30m", "2h30m", "1d", "1d2h" (relative to now)
// or RFC3339 absolute time like "2026-04-29T15:00:00Z".
func ParseWhen(when string, now time.Time) (time.Time, error) {
	if when == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339, when); err == nil {
		return t, nil
	}
	expanded := dayPattern.ReplaceAllStringFunc(when, func(match string) string {
		n, _ := strconv.Atoi(match[:len(match)-1])
		return fmt.Sprintf("%dh", n*24)
	})
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q: use 1h, 30m, 2h30m, 1d, or RFC3339 like 2026-04-29T15:00:00Z", when)
	}
	if d <= 0 {
		return time.Time{}, fmt.Errorf("duration must be positive")
	}
	return now.Add(d), nil
}
