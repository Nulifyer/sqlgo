package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestUpdateFooterYieldsToHintsAndStatus(t *testing.T) {
	m := newMainLayer()
	a := &app{layers: []Layer{m}}
	m.status = "query failed: fixture"
	for _, width := range []int{40, 80, 120, 240} {
		without := m.statusText(a, width)
		a.updateVersion = "v1.2.3"
		with := m.statusText(a, width)
		if !strings.HasPrefix(with, without) {
			t.Fatalf("notice displaced existing footer at %d: %q vs %q", width, with, without)
		}
		if displayWidth(with) > width {
			t.Fatalf("footer overflows %d: %q", width, with)
		}
		if width == 240 && !strings.Contains(with, "run sqlgo update") {
			t.Fatalf("wide footer missing notice: %q", with)
		}
		if width == 40 && strings.Contains(with, "Update available") {
			t.Fatal("notice displaced narrow footer")
		}
		a.updateVersion = ""
	}
}

func TestVersionHelpIncludesInstructionsAndCanSearchUpdates(t *testing.T) {
	a := &app{version: "v1.0.0", updateVersion: "v1.2.3"}
	hl := newAppHelpLayer(a)
	var content string
	for _, l := range hl.all {
		content += l.key + " " + l.desc + "\n"
	}
	for _, want := range []string{"v1.0.0", "v1.2.3", "Exit sqlgo", "sqlgo update", "SQLGO_UPDATE_CHECK=0"} {
		if !strings.Contains(content, want) {
			t.Fatalf("help missing %q", want)
		}
	}
	if len(filterHelpLines(hl.all, "update")) == 0 {
		t.Fatal("update instructions not searchable")
	}
	a.version = ""
	a.updateVersion = ""
	hl.setVersion(a)
	content = ""
	for _, l := range hl.all {
		content += l.key + " " + l.desc + "\n"
	}
	if !strings.Contains(content, "source installation") || strings.Contains(content, "sqlgo update") {
		t.Fatalf("source instructions = %s", content)
	}
}

func TestUpdateCheckPostsThroughUIQueue(t *testing.T) {
	m := newMainLayer()
	a := &app{version: "v1.0.0", layers: []Layer{m}, asyncCh: make(chan func(*app), 1), doneCh: make(chan struct{})}
	hl := newAppHelpLayer(a)
	a.layers = append(a.layers, hl)
	entered := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a.startUpdateCheck(ctx, func(context.Context) string { close(entered); <-release; return "v1.2.3" })
	<-entered
	if a.updateVersion != "" {
		t.Fatal("check changed UI state before callback")
	}
	close(release)
	select {
	case apply := <-a.asyncCh:
		if a.updateVersion != "" {
			t.Fatal("background goroutine mutated UI")
		}
		apply(a)
	case <-ctx.Done():
		t.Fatal("check did not post result")
	}
	if a.updateVersion != "v1.2.3" {
		t.Fatalf("notice = %q", a.updateVersion)
	}
	var found bool
	for _, l := range hl.all {
		if l.desc == "v1.2.3" {
			found = true
		}
	}
	if !found {
		t.Fatal("open help was not refreshed")
	}
}

func TestAutomaticChecksDisabledForSourceAndOptOut(t *testing.T) {
	for _, version := range []string{"", "v1.2.3"} {
		t.Setenv("SQLGO_UPDATE_CHECK", "0")
		a := &app{version: version, asyncCh: make(chan func(*app), 1), doneCh: make(chan struct{})}
		cancel := a.beginUpdateCheck()
		cancel()
		select {
		case <-a.asyncCh:
			t.Fatal("disabled check posted callback")
		default:
		}
	}
}

func TestUpdateCheckCancellationAndFailuresAreSilent(t *testing.T) {
	for _, result := range []string{"", "v1.2.3"} {
		t.Run("result="+result, func(t *testing.T) {
			a := &app{asyncCh: make(chan func(*app), 1), doneCh: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			finished := make(chan struct{})
			a.startUpdateCheck(ctx, func(context.Context) string { defer close(finished); return result })
			<-finished
			select {
			case <-a.asyncCh:
				t.Fatal("cancelled check posted UI notification")
			default:
			}
			if a.updateVersion != "" {
				t.Fatal("cancelled check mutated UI")
			}
		})
	}
}
