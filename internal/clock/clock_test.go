package clock

import (
	"testing"
	"time"
)

func TestRealClock(t *testing.T) {
	c := RealClock{}
	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("RealClock.Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestMockClock(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewMock(start)

	if got := m.Now(); !got.Equal(start) {
		t.Errorf("Mock.Now() = %v, want %v", got, start)
	}

	m.Advance(5 * time.Second)
	expected := start.Add(5 * time.Second)
	if got := m.Now(); !got.Equal(expected) {
		t.Errorf("after Advance(5s), Mock.Now() = %v, want %v", got, expected)
	}

	newTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	m.Set(newTime)
	if got := m.Now(); !got.Equal(newTime) {
		t.Errorf("after Set, Mock.Now() = %v, want %v", got, newTime)
	}
}

func TestMockImplementsClock(t *testing.T) {
	var _ Clock = RealClock{}
	var _ Clock = &Mock{}
}
