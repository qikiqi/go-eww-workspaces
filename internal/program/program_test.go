package program

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func TestRender(t *testing.T) {
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

			var buf bytes.Buffer
			err := render(context.Background(), &buf, tt.fetcher, tt.output)

			if (err != nil) != tt.wantErr {
				t.Fatalf("render() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			// Encoder writes exactly one JSON object followed by '\n'.
			if b := buf.Bytes(); len(b) == 0 || b[len(b)-1] != '\n' {
				t.Errorf("render() output missing trailing newline: %q", b)
			}

			var got payload
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("render() output is not valid JSON: %v (%q)", err, buf.String())
			}
			if got.Focused != tt.wantFocused {
				t.Errorf("Focused = %d, want %d", got.Focused, tt.wantFocused)
			}
			for n, want := range tt.wantStates {
				if s := stateOf(got.List, n); s != want {
					t.Errorf("workspace %d state = %q, want %q", n, s, want)
				}
			}
		})
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
