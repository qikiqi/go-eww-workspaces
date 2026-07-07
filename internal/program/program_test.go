package program

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFetcher implements WorkspaceFetcher for testing.
type fakeFetcher struct {
	workspaces []Workspace
	err        error
}

func (f *fakeFetcher) FetchWorkspaces(_ context.Context) ([]Workspace, error) {
	return f.workspaces, f.err
}

func TestBuildWidget(t *testing.T) {
	t.Parallel()

	const (
		cmd      = "swaymsg"
		myOut    = "HDMI-A-1"
		otherOut = "DP-1"
	)

	tests := []struct {
		name         string
		workspaces   []Workspace
		output       string
		cmdName      string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:       "nil workspaces - all unoccupied",
			workspaces: nil,
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{
				`class "unoccupied" "1"`,
				`class "unoccupied" "5"`,
				`class "unoccupied" "10"`,
			},
		},
		{
			name:       "focused workspace",
			workspaces: []Workspace{{Num: 1, Output: myOut, Focused: true}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "focused" "1"`},
			wantAbsent:   []string{`class "unoccupied" "1"`},
		},
		{
			name:       "urgent workspace",
			workspaces: []Workspace{{Num: 3, Output: myOut, Urgent: true}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "urgent" "3"`},
			wantAbsent:   []string{`class "unoccupied" "3"`},
		},
		{
			name:       "occupied workspace - neither focused nor urgent",
			workspaces: []Workspace{{Num: 5, Output: myOut}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "occupied" "5"`},
		},
		{
			name:       "urgent beats focused when both set",
			workspaces: []Workspace{{Num: 2, Output: myOut, Focused: true, Urgent: true}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "urgent" "2"`},
			wantAbsent:   []string{`class "focused" "2"`},
		},
		{
			name:       "workspace on different output is ignored",
			workspaces: []Workspace{{Num: 4, Output: otherOut, Focused: true}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "unoccupied" "4"`},
			wantAbsent:   []string{`class "focused" "4"`},
		},
		{
			name:       "workspace num below range is ignored",
			workspaces: []Workspace{{Num: 0, Output: myOut, Focused: true}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "unoccupied" "1"`},
		},
		{
			name:       "workspace num above range is ignored",
			workspaces: []Workspace{{Num: 11, Output: myOut, Urgent: true}},
			output:     myOut,
			cmdName:    cmd,
			wantContains: []string{`class "unoccupied" "10"`},
		},
		{
			name:    "onclick attribute uses cmdName for all buttons",
			output:  myOut,
			cmdName: cmd,
			wantContains: []string{
				fmt.Sprintf(`onclick "%s 'workspace 1'"`, cmd),
				fmt.Sprintf(`onclick "%s 'workspace 10'"`, cmd),
			},
		},
		{
			name:    "output wrapped in eww box element",
			output:  myOut,
			cmdName: cmd,
			wantContains: []string{
				`(box :class "workspaces"`,
				`:orientation "h"`,
				`:halign "start"`,
				`:spacing "6"`,
			},
		},
		{
			name:    "absolute path cmdName appears verbatim in onclick",
			output:  myOut,
			cmdName: "/usr/bin/swaymsg",
			wantContains: []string{
				`onclick "/usr/bin/swaymsg 'workspace 1'"`,
				`onclick "/usr/bin/swaymsg 'workspace 10'"`,
			},
		},
		{
			name: "mixed states across workspaces on same and different outputs",
			workspaces: []Workspace{
				{Num: 1, Output: myOut, Focused: true},
				{Num: 2, Output: myOut, Urgent: true},
				{Num: 3, Output: myOut},
				{Num: 4, Output: otherOut, Focused: true}, // different output, ignored
			},
			output:  myOut,
			cmdName: cmd,
			wantContains: []string{
				`class "focused" "1"`,
				`class "urgent" "2"`,
				`class "occupied" "3"`,
				`class "unoccupied" "4"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildWidget(tt.workspaces, tt.output, tt.cmdName)

			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("buildWidget() missing %q\ngot: %s", want, got)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("buildWidget() unexpectedly contains %q\ngot: %s", absent, got)
				}
			}
			if count := strings.Count(got, "(button "); count != endWS-startWS+1 {
				t.Errorf("buildWidget() produced %d buttons, want %d", count, endWS-startWS+1)
			}
		})
	}
}

func TestBuildWidget_FocusedCount(t *testing.T) {
	t.Parallel()

	const output = "HDMI-A-1"

	tests := []struct {
		name      string
		workspaces []Workspace
		wantCount  int
	}{
		{
			name:      "no workspaces - zero focused buttons",
			wantCount: 0,
		},
		{
			name: "one focused workspace - exactly one focused button",
			workspaces: []Workspace{
				{Num: 1, Output: output, Focused: true},
				{Num: 2, Output: output},
				{Num: 3, Output: output},
			},
			wantCount: 1,
		},
		{
			name: "focused workspace on different output - zero focused buttons",
			workspaces: []Workspace{
				{Num: 1, Output: "DP-1", Focused: true},
			},
			wantCount: 0,
		},
		{
			name: "urgent workspace does not count as focused",
			workspaces: []Workspace{
				{Num: 1, Output: output, Urgent: true, Focused: true},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildWidget(tt.workspaces, output, "swaymsg")
			if count := strings.Count(got, `class "focused"`); count != tt.wantCount {
				t.Errorf("buildWidget() focused button count = %d, want %d\ngot: %s", count, tt.wantCount, got)
			}
		})
	}
}

func TestRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fetcher      WorkspaceFetcher
		cmdName      string
		output       string
		wantErr      bool
		wantContains string
	}{
		{
			name:    "fetcher error propagates",
			fetcher: &fakeFetcher{err: fmt.Errorf("connection refused")},
			wantErr: true,
		},
		{
			name:         "empty workspaces writes widget to writer",
			fetcher:      &fakeFetcher{},
			cmdName:      "swaymsg",
			output:       "HDMI-A-1",
			wantContains: `class "workspaces"`,
		},
		{
			name: "focused workspace appears in written output",
			fetcher: &fakeFetcher{workspaces: []Workspace{
				{Num: 1, Output: "HDMI-A-1", Focused: true},
			}},
			cmdName:      "swaymsg",
			output:       "HDMI-A-1",
			wantContains: `class "focused" "1"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			err := render(context.Background(), &buf, tt.fetcher, tt.cmdName, tt.output)

			if (err != nil) != tt.wantErr {
				t.Errorf("render() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.wantContains != "" && !strings.Contains(buf.String(), tt.wantContains) {
				t.Errorf("render() output missing %q\ngot: %s", tt.wantContains, buf.String())
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
		if !strings.Contains(err.Error(), "nonexistent") {
			t.Errorf("readMonitorOutput() error should mention monitor name, got: %v", err)
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
}
