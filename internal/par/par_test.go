package par

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// guard fails a test that would otherwise hang when ForEach never cancels or never returns.
// Correct code never waits on it.
const guard = 10 * time.Second

func TestForEachCallsEveryItemOnceWithItsIndex(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f", "g"}
	var mu sync.Mutex
	got := make([]string, len(items))
	calls := 0
	err := ForEach(context.Background(), 3, items, func(_ context.Context, i int, item string) error {
		mu.Lock()
		defer mu.Unlock()
		got[i] = item
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, items) || calls != len(items) {
		t.Errorf("ForEach called %d times with %v, want %d times with %v", calls, got, len(items), items)
	}
}

func TestForEachEmptyListCallsNothing(t *testing.T) {
	for _, items := range [][]int{nil, {}} {
		called := false
		err := ForEach(context.Background(), 4, items, func(context.Context, int, int) error {
			called = true
			return nil
		})
		if err != nil || called {
			t.Errorf("ForEach(%v) = %v, called %v; want nil, not called", items, err, called)
		}
	}
}

// TestForEachBound holds every call open until `want` of them are running at once, then checks
// that no more than that ever ran together. n <= 0 runs one call at a time.
func TestForEachBound(t *testing.T) {
	tests := []struct {
		name     string
		n, items int
		want     int // calls in flight at the peak
	}{
		{"n calls at a time", 3, 10, 3},
		{"one at a time", 1, 4, 1},
		{"n = 0 runs one at a time", 0, 4, 1},
		{"negative n runs one at a time", -2, 4, 1},
		{"more slots than items runs them all at once", 8, 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			inFlight, peak := 0, 0
			started := make(chan int, tt.items)
			release := make(chan struct{})
			done := make(chan error, 1)
			calls := make([]atomic.Int32, tt.items)
			go func() {
				done <- ForEach(context.Background(), tt.n, make([]struct{}, tt.items), func(_ context.Context, i int, _ struct{}) error {
					mu.Lock()
					inFlight++
					peak = max(peak, inFlight)
					mu.Unlock()
					calls[i].Add(1)
					started <- i
					<-release
					mu.Lock()
					inFlight--
					mu.Unlock()
					return nil
				})
			}()
			for range tt.want {
				select {
				case <-started:
				case <-time.After(guard):
					t.Fatalf("ForEach(n=%d) started fewer than %d calls at once", tt.n, tt.want)
				}
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatalf("ForEach = %v, want nil", err)
			}
			if peak != tt.want {
				t.Errorf("ForEach(n=%d) ran %d calls at once, want %d", tt.n, peak, tt.want)
			}
			for i := range calls {
				if got := calls[i].Load(); got != 1 {
					t.Errorf("ForEach(n=%d) called item %d %d times, want 1", tt.n, i, got)
				}
			}
		})
	}
}

func TestForEachReturnsTheError(t *testing.T) {
	boom := errors.New("boom")
	err := ForEach(context.Background(), 4, []int{0, 1, 2, 3}, func(_ context.Context, i int, _ int) error {
		if i == 2 {
			return boom
		}
		return nil
	})
	if err != boom {
		t.Errorf("ForEach = %v, want %v", err, boom)
	}
}

// The first error cancels the context of a call still running, and is what ForEach returns even
// when that call then fails too.
func TestForEachFirstErrorCancelsRunningCalls(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	running := make(chan struct{})
	var cause error
	err := ForEach(context.Background(), 2, []int{0, 1}, func(ctx context.Context, i int, _ int) error {
		if i == 0 {
			select {
			case <-running: // item 1 is blocked on its context
			case <-time.After(guard):
				t.Error("item 1 never started")
			}
			return first
		}
		close(running)
		select {
		case <-ctx.Done():
			cause = context.Cause(ctx)
		case <-time.After(guard):
			t.Error("the context of a running call was not cancelled")
		}
		return second
	})
	if err != first {
		t.Errorf("ForEach = %v, want the first error %v", err, first)
	}
	if cause != first {
		t.Errorf("the running call's context.Cause = %v, want %v", cause, first)
	}
}

func TestForEachErrorStopsNewCallsStarting(t *testing.T) {
	boom := errors.New("boom")
	var mu sync.Mutex
	var called []int
	err := ForEach(context.Background(), 1, []int{0, 1, 2, 3, 4}, func(_ context.Context, i int, _ int) error {
		mu.Lock()
		called = append(called, i)
		mu.Unlock()
		return boom
	})
	if err != boom {
		t.Errorf("ForEach = %v, want %v", err, boom)
	}
	if want := []int{0}; !reflect.DeepEqual(called, want) {
		t.Errorf("ForEach called items %v after the first failed, want only %v", called, want)
	}
}

func TestForEachCancelledParentStartsNothing(t *testing.T) {
	stop := errors.New("shutting down")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(stop)
	called := false
	err := ForEach(ctx, 2, []int{0, 1, 2}, func(context.Context, int, int) error {
		called = true
		return nil
	})
	if err != stop || called {
		t.Errorf("ForEach(cancelled ctx) = %v, called %v; want %v, not called", err, called, stop)
	}
}
