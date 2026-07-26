package clock

import "time"

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

// RealClock uses the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Mock is a controllable clock for tests.
type Mock struct {
	current time.Time
}

func NewMock(t time.Time) *Mock {
	return &Mock{current: t}
}

func (m *Mock) Now() time.Time {
	return m.current
}

func (m *Mock) Advance(d time.Duration) {
	m.current = m.current.Add(d)
}

func (m *Mock) Set(t time.Time) {
	m.current = t
}
