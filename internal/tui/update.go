package tui

import (
	"context"
	"time"

	"github.com/Nulifyer/sqlgo/internal/update"
)

func (a *app) beginUpdateCheck() context.CancelFunc {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if update.ChecksEnabled() && update.ValidVersion(a.version) {
		current := a.version
		a.startUpdateCheck(ctx, func(ctx context.Context) string {
			path, err := update.CachePath()
			if err != nil {
				return ""
			}
			return update.New().CachedLatest(ctx, current, path, time.Now())
		})
	}
	return cancel
}

// Background checks post state changes through the existing UI event channel.
func (a *app) startUpdateCheck(ctx context.Context, check func(context.Context) string) {
	go func() {
		latest := check(ctx)
		if latest == "" || ctx.Err() != nil {
			return
		}
		select {
		case a.asyncCh <- func(a *app) {
			a.updateVersion = latest
			if hl, ok := a.topLayer().(*helpLayer); ok {
				hl.setVersion(a)
			}
		}:
		case <-a.doneCh:
		case <-ctx.Done():
		}
	}()
}

func appendUpdateNotice(status, latest string, width int) string {
	if latest == "" {
		return status
	}
	notice := "    Update available: " + latest + " | run sqlgo update"
	if displayWidth(status+notice) > width {
		return status
	}
	return status + notice
}

func newAppHelpLayer(a *app) *helpLayer {
	hl := newHelpLayer()
	hl.setVersion(a)
	return hl
}

func (hl *helpLayer) setVersion(a *app) {
	version := a.version
	if version == "" {
		version = "source/development build"
	}
	lines := []helpLine{{desc: "Version and updates"}, {key: "Installed", desc: version}}
	if a.updateVersion != "" {
		lines = append(lines, helpLine{key: "Update available", desc: a.updateVersion})
	}
	if update.ValidVersion(a.version) {
		lines = append(lines,
			helpLine{key: "Update", desc: "Exit sqlgo, then run: sqlgo update"},
			helpLine{key: "Check manually", desc: "sqlgo update --check"},
			helpLine{key: "Disable checks", desc: "Set SQLGO_UPDATE_CHECK=0"},
		)
	} else {
		lines = append(lines, helpLine{key: "Update", desc: "Re-run your source installation command."})
	}
	hl.all = append(append(lines, helpLine{}), helpContent()...)
	hl.refilter()
}
