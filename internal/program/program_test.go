package program

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sway "github.com/joshuarubin/go-sway"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeFetcher implements WorkspaceFetcher for testing.
type fakeFetcher struct {
	workspaces []Workspace
	err        error
}

func (f *fakeFetcher) FetchWorkspaces(_ context.Context) ([]Workspace, error) {
	return f.workspaces, f.err
}

// stateOf returns the state string of the entry for workspace num in list.
// Returns "" if num is out of range.
func stateOf(list []wsView, num int) string {
	if num < startWS || num > endWS {
		return ""
	}
	return list[num-startWS].State
}

func TestBuildPayload(t *testing.T) {
	t.Parallel()

	const (
		myOut    = "HDMI-A-1"
		otherOut = "DP-1"
	)

	tests := []struct {
		name        string
		workspaces  []Workspace
		output      string
		wantFocused int
		wantStates  map[int]string // ws num -> expected state; omitted nums default to "unoccupied"
	}{
		{
			name:        "nil workspaces - all unoccupied",
			workspaces:  nil,
			output:      myOut,
			wantFocused: 0,
			wantStates:  map[int]string{1: "unoccupied", 5: "unoccupied", 10: "unoccupied"},
		},
		{
			name:        "focused workspace",
			workspaces:  []Workspace{{Num: 1, Output: myOut, Focused: true}},
			output:      myOut,
			wantFocused: 1,
			wantStates:  map[int]string{1: "focused"},
		},
		{
			name:        "urgent workspace",
			workspaces:  []Workspace{{Num: 3, Output: myOut, Urgent: true}},
			output:      myOut,
			wantFocused: 0,
			wantStates:  map[int]string{3: "urgent"},
		},
		{
			name:        "occupied workspace - neither focused nor urgent",
			workspaces:  []Workspace{{Num: 5, Output: myOut}},
			output:      myOut,
			wantFocused: 0,
			wantStates:  map[int]string{5: "occupied"},
		},
		{
			name:        "urgent beats focused when both set",
			workspaces:  []Workspace{{Num: 2, Output: myOut, Focused: true, Urgent: true}},
			output:      myOut,
			wantFocused: 0, // focused not counted when urgent wins
			wantStates:  map[int]string{2: "urgent"},
		},
		{
			name:        "workspace on different output is ignored",
			workspaces:  []Workspace{{Num: 4, Output: otherOut, Focused: true}},
			output:      myOut,
			wantFocused: 0,
			wantStates:  map[int]string{4: "unoccupied"},
		},
		{
			name:        "workspace num below range is ignored",
			workspaces:  []Workspace{{Num: 0, Output: myOut, Focused: true}},
			output:      myOut,
			wantFocused: 0,
			wantStates:  map[int]string{1: "unoccupied"},
		},
		{
			name:        "workspace num above range is ignored",
			workspaces:  []Workspace{{Num: 11, Output: myOut, Urgent: true}},
			output:      myOut,
			wantFocused: 0,
			wantStates:  map[int]string{10: "unoccupied"},
		},
		{
			name: "mixed states across workspaces on same and different outputs",
			workspaces: []Workspace{
				{Num: 1, Output: myOut, Focused: true},
				{Num: 2, Output: myOut, Urgent: true},
				{Num: 3, Output: myOut},
				{Num: 4, Output: otherOut, Focused: true}, // different output, ignored
			},
			output:      myOut,
			wantFocused: 1,
			wantStates: map[int]string{
				1: "focused",
				2: "urgent",
				3: "occupied",
				4: "unoccupied",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildPayload(tt.workspaces, tt.output)

			if got.Focused != tt.wantFocused {
				t.Errorf("Focused = %d, want %d", got.Focused, tt.wantFocused)
			}
			if want := endWS - startWS + 1; len(got.List) != want {
				t.Errorf("len(List) = %d, want %d", len(got.List), want)
			}
			for i := startWS; i <= endWS; i++ {
				want := tt.wantStates[i]
				if want == "" {
					want = "unoccupied"
				}
				if got := stateOf(got.List, i); got != want {
					t.Errorf("workspace %d state = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestBuildPayload_FocusedInvariant(t *testing.T) {
	t.Parallel()

	const output = "HDMI-A-1"

	tests := []struct {
		name        string
		workspaces  []Workspace
		wantFocused int
	}{
		{
			name:        "no workspaces - focused is 0",
			wantFocused: 0,
		},
		{
			name: "single focused workspace - focused matches its num",
			workspaces: []Workspace{
				{Num: 1, Output: output, Focused: true},
				{Num: 2, Output: output},
				{Num: 3, Output: output},
			},
			wantFocused: 1,
		},
		{
			name: "focused workspace on different output - focused is 0",
			workspaces: []Workspace{
				{Num: 1, Output: "DP-1", Focused: true},
			},
			wantFocused: 0,
		},
		{
			name: "urgent overrides focused - focused is 0",
			workspaces: []Workspace{
				{Num: 1, Output: output, Urgent: true, Focused: true},
			},
			wantFocused: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildPayload(tt.workspaces, output)
			if got.Focused != tt.wantFocused {
				t.Errorf("Focused = %d, want %d", got.Focused, tt.wantFocused)
			}
		})
	}
}

func TestRenderPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fetcher     WorkspaceFetcher
		output      string
		wantErr     bool
		wantFocused int
		wantStates  map[int]string
	}{
		{
			name:    "fetcher error propagates",
			fetcher: &fakeFetcher{err: fmt.Errorf("connection refused")},
			wantErr: true,
		},
		{
			name:        "empty workspaces emits full unoccupied list",
			fetcher:     &fakeFetcher{},
			output:      "HDMI-A-1",
			wantFocused: 0,
			wantStates:  map[int]string{1: "unoccupied", 10: "unoccupied"},
		},
		{
			name: "focused workspace appears in payload",
			fetcher: &fakeFetcher{workspaces: []Workspace{
				{Num: 1, Output: "HDMI-A-1", Focused: true},
			}},
			output:      "HDMI-A-1",
			wantFocused: 1,
			wantStates:  map[int]string{1: "focused"},
		},
		{
			name: "urgent workspace appears in payload",
			fetcher: &fakeFetcher{workspaces: []Workspace{
				{Num: 3, Output: "HDMI-A-1", Urgent: true},
			}},
			output:     "HDMI-A-1",
			wantStates: map[int]string{3: "urgent"},
		},
		{
			name: "occupied workspace appears in payload",
			fetcher: &fakeFetcher{workspaces: []Workspace{
				{Num: 5, Output: "HDMI-A-1"},
			}},
			output:     "HDMI-A-1",
			wantStates: map[int]string{5: "occupied"},
		},
		{
			name: "workspaces on other output are excluded",
			fetcher: &fakeFetcher{workspaces: []Workspace{
				{Num: 1, Output: "HDMI-A-1", Focused: true},
				{Num: 2, Output: "DP-1", Focused: true},
			}},
			output:      "HDMI-A-1",
			wantFocused: 1,
			wantStates:  map[int]string{1: "focused", 2: "unoccupied"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := renderPayload(context.Background(), tt.fetcher, tt.output)

			if (err != nil) != tt.wantErr {
				t.Fatalf("renderPayload() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if len(got) == 0 || got[len(got)-1] != '\n' {
				t.Errorf("renderPayload() output missing trailing newline: %q", got)
			}

			var p payload
			if err := json.Unmarshal(got, &p); err != nil {
				t.Fatalf("renderPayload() output is not valid JSON: %v (%q)", err, got)
			}
			if p.Focused != tt.wantFocused {
				t.Errorf("Focused = %d, want %d", p.Focused, tt.wantFocused)
			}
			for n, want := range tt.wantStates {
				if s := stateOf(p.List, n); s != want {
					t.Errorf("workspace %d state = %q, want %q", n, s, want)
				}
			}
		})
	}
}

// mutableFetcher lets a test flip the returned workspaces between emits.
// Access is guarded so the debouncer goroutine can call FetchWorkspaces
// concurrently with test mutations.
type mutableFetcher struct {
	mu         sync.Mutex
	workspaces []Workspace
}

func (m *mutableFetcher) set(w []Workspace) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workspaces = w
}

func (m *mutableFetcher) FetchWorkspaces(_ context.Context) ([]Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workspaces, nil
}

// syncBuffer is a bytes.Buffer safe for concurrent reads/writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *syncBuffer) String() string {
	return string(b.Bytes())
}

func TestEventHandler_EmitDedupesRepeatedPayload(t *testing.T) {
	t.Parallel()

	const output = "HDMI-A-1"

	fetcher := &mutableFetcher{}
	fetcher.set([]Workspace{{Num: 1, Output: output, Focused: true}})
	var buf bytes.Buffer
	h := &eventHandler{
		fetcher: fetcher,
		writer:  &buf,
		output:  output,
	}

	ctx := context.Background()

	// First emit writes.
	h.emit(ctx)
	if got := buf.Len(); got == 0 {
		t.Fatalf("expected first emit to write, buffer is empty")
	}
	after1 := buf.Len()

	// Second emit with identical state must not write.
	h.emit(ctx)
	if buf.Len() != after1 {
		t.Errorf("expected repeated emit to be suppressed; buffer grew from %d to %d", after1, buf.Len())
	}

	// State change → new emission must write.
	fetcher.set([]Workspace{{Num: 2, Output: output, Focused: true}})
	h.emit(ctx)
	if buf.Len() <= after1 {
		t.Errorf("expected state change to write; buffer stayed at %d", buf.Len())
	}
	after3 := buf.Len()

	// Same again — no write.
	h.emit(ctx)
	if buf.Len() != after3 {
		t.Errorf("expected repeated emit after state change to be suppressed; buffer grew from %d to %d", after3, buf.Len())
	}

	// Each emitted line should be one full JSON object with a trailing newline.
	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 emitted lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var p payload
		if err := json.Unmarshal(line, &p); err != nil {
			t.Errorf("line %d is not valid JSON: %v (%q)", i, err, line)
		}
	}
}

// waitFor polls check every 5ms up to timeout; returns true if check ever
// returns true. Preferred over fixed sleeps for asserting on debouncer output.
func waitFor(timeout time.Duration, check func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return check()
}

func countLines(b []byte) int {
	trimmed := bytes.TrimRight(b, "\n")
	if len(trimmed) == 0 {
		return 0
	}
	return bytes.Count(trimmed, []byte("\n")) + 1
}

func TestEventHandler_DebouncerCoalescesBursts(t *testing.T) {
	t.Parallel()

	const output = "HDMI-A-1"
	fetcher := &mutableFetcher{}
	fetcher.set([]Workspace{{Num: 1, Output: output, Focused: true}})
	buf := &syncBuffer{}
	h := newEventHandler(fetcher, buf, output, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runDebouncer(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Fire a burst; expect exactly one line after the trailing edge.
	for range 5 {
		h.kick()
	}
	if !waitFor(200*time.Millisecond, func() bool { return countLines(buf.Bytes()) == 1 }) {
		t.Fatalf("expected 1 emitted line from burst, got %d: %q", countLines(buf.Bytes()), buf.String())
	}

	// State change → new burst → second emit.
	fetcher.set([]Workspace{{Num: 2, Output: output, Focused: true}})
	for range 3 {
		h.kick()
	}
	if !waitFor(200*time.Millisecond, func() bool { return countLines(buf.Bytes()) == 2 }) {
		t.Fatalf("expected 2 lines after state change, got %d: %q", countLines(buf.Bytes()), buf.String())
	}

	// Each emitted line must be a valid JSON payload.
	for i, line := range bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		var p payload
		if err := json.Unmarshal(line, &p); err != nil {
			t.Errorf("line %d not valid JSON: %v (%q)", i, err, line)
		}
	}
}

func TestEventHandler_DebouncerExitsOnCancel(t *testing.T) {
	t.Parallel()

	fetcher := &mutableFetcher{}
	buf := &syncBuffer{}
	h := newEventHandler(fetcher, buf, "HDMI-A-1", 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runDebouncer(ctx)
		close(done)
	}()

	// Cancel with no pending kicks — outer select's ctx.Done() case.
	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("debouncer did not exit after ctx cancel")
	}
}

func TestEventHandler_DebouncerExitsDuringTimerWait(t *testing.T) {
	t.Parallel()

	fetcher := &mutableFetcher{}
	fetcher.set([]Workspace{{Num: 1, Output: "HDMI-A-1", Focused: true}})
	buf := &syncBuffer{}
	// Long debounce so we can cancel while the inner timer is still waiting.
	h := newEventHandler(fetcher, buf, "HDMI-A-1", time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.runDebouncer(ctx)
		close(done)
	}()

	h.kick()
	// Give the debouncer time to enter the inner loop and start the timer.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("debouncer did not exit during timer wait after ctx cancel")
	}

	// Cancelled before timer fired → no emit.
	if got := buf.Len(); got != 0 {
		t.Errorf("expected no emit after cancel-during-timer, buf len = %d: %q", got, buf.String())
	}
}

func TestShouldRerenderWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		change sway.WindowEventChange
		want   bool
	}{
		{sway.WindowNew, true},
		{sway.WindowClose, true},
		{sway.WindowMove, true},
		{sway.WindowFocus, false},
		{sway.WindowTitle, false},
		{sway.WindowMark, false},
		{sway.WindowFullscreen, false},
		{sway.WindowFloating, false},
		{sway.WindowUrgent, false},
		{sway.WindowEventChange("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.change), func(t *testing.T) {
			t.Parallel()
			if got := shouldRerenderWindow(tt.change); got != tt.want {
				t.Errorf("shouldRerenderWindow(%q) = %v, want %v", tt.change, got, tt.want)
			}
		})
	}
}

func TestWaitForFile(t *testing.T) {
	t.Parallel()

	t.Run("returns data when file exists and is non-empty", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "data.json")
		want := []byte(`{"key":"value"}`)
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		got, err := waitForFile(ctx, path, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("waitForFile() unexpected error: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("waitForFile() = %q, want %q", got, want)
		}
	})

	t.Run("returns error when context is pre-canceled", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "missing.json")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := waitForFile(ctx, path, 10*time.Millisecond)
		if err == nil {
			t.Error("waitForFile() expected error for pre-canceled context, got nil")
		}
	})

	t.Run("skips empty file and returns error on timeout", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "empty.json")
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := waitForFile(ctx, path, 10*time.Millisecond)
		if err == nil {
			t.Error("waitForFile() expected timeout error for empty file, got nil")
		}
	})
}

func TestReadMonitorOutput(t *testing.T) {
	t.Parallel()

	t.Run("returns output for matching monitor", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "monitors.json")
		data := `[{"monitor":"eDP-1","output":"HDMI-A-1"},{"monitor":"DP-1","output":"DP-2"}]`
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		got, err := readMonitorOutput(ctx, path, "eDP-1")
		if err != nil {
			t.Fatalf("readMonitorOutput() unexpected error: %v", err)
		}
		if got != "HDMI-A-1" {
			t.Errorf("readMonitorOutput() = %q, want %q", got, "HDMI-A-1")
		}
	})

	t.Run("error when monitor not present in file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "monitors.json")
		if err := os.WriteFile(path, []byte(`[{"monitor":"eDP-1","output":"HDMI-A-1"}]`), 0o644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		_, err := readMonitorOutput(ctx, path, "nonexistent")
		if err == nil {
			t.Fatal("readMonitorOutput() expected error for unknown monitor, got nil")
		}
	})

	t.Run("error when context canceled before file appears", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "missing.json")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := readMonitorOutput(ctx, path, "eDP-1")
		if err == nil {
			t.Error("readMonitorOutput() expected error for canceled context, got nil")
		}
	})

	t.Run("retries until file contains valid JSON", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "monitors.json")

		// Non-empty content that passes waitForFile but fails json.Unmarshal.
		if err := os.WriteFile(path, []byte(`not valid json`), 0o644); err != nil {
			t.Fatal(err)
		}

		// Overwrite with valid JSON well before the 200 ms retry fires.
		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = os.WriteFile(path, []byte(`[{"monitor":"eDP-1","output":"HDMI-A-1"}]`), 0o644)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		got, err := readMonitorOutput(ctx, path, "eDP-1")
		if err != nil {
			t.Fatalf("readMonitorOutput() unexpected error: %v", err)
		}
		if got != "HDMI-A-1" {
			t.Errorf("readMonitorOutput() = %q, want %q", got, "HDMI-A-1")
		}
	})
}
