package tui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Nulifyer/sqlgo/internal/atomicfile"
	"github.com/Nulifyer/sqlgo/internal/config"
)

const maxRecoveryBytes = 64 << 20

type recoveryDraft struct {
	Title, Path, Text, Baseline, Catalog string
	Format                               documentFormat
	Fingerprint                          documentFingerprint
	DiskKnown                            bool
	lines                                [][]rune
}
type recoveryEnvelope struct {
	Version, PID int
	Drafts       []recoveryDraft
}
type recoveryStore struct {
	mu         sync.Mutex
	path       string
	generation uint64
	closed     bool
}

func newRecoveryStore(dir string) (*recoveryStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	return &recoveryStore{path: filepath.Join(dir, "recovery-"+hex.EncodeToString(id[:])+".json")}, nil
}
func (s *recoveryStore) write(gen uint64, drafts []recoveryDraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("recovery store is closed")
	}
	if gen < s.generation {
		return nil
	}
	if len(drafts) == 0 {
		err := os.Remove(s.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		s.generation = gen
		return nil
	}
	data, err := json.Marshal(recoveryEnvelope{Version: 1, PID: os.Getpid(), Drafts: drafts})
	if err != nil {
		return err
	}
	if len(data) > maxRecoveryBytes {
		return fmt.Errorf("recovery snapshot exceeds 64 MiB; save large queries to files")
	}
	if err := atomicfile.WriteFile(s.path, data); err != nil {
		return err
	}
	s.generation = gen
	return nil
}
func (s *recoveryStore) close(discard bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if discard {
		_ = os.Remove(s.path)
	}
}
func readRecoveryDrafts(dir string, alive func(int) bool) ([]recoveryDraft, []string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "recovery-*.json"))
	if err != nil {
		return nil, nil, err
	}
	var drafts []recoveryDraft
	var sources []string
	var lastErr error
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxRecoveryBytes+1))
		_ = f.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if len(data) > maxRecoveryBytes {
			lastErr = fmt.Errorf("recovery file too large: %s", path)
			continue
		}
		var saved recoveryEnvelope
		if err := json.Unmarshal(data, &saved); err != nil {
			lastErr = err
			continue
		}
		if saved.Version != 1 {
			lastErr = fmt.Errorf("unsupported recovery version in %s", path)
			continue
		}
		if saved.PID > 0 && alive(saved.PID) {
			continue
		}
		for i := range saved.Drafts {
			saved.Drafts[i].Text = normalizeEditorLineEndings(saved.Drafts[i].Text)
			saved.Drafts[i].lines = splitBufferText(saved.Drafts[i].Text)
		}
		drafts = append(drafts, saved.Drafts...)
		sources = append(sources, path)
	}
	return drafts, sources, lastErr
}
func (a *app) startRecovery() {
	a.runBackground(func() func(*app) {
		dir, err := config.DataDir()
		var store *recoveryStore
		var drafts []recoveryDraft
		var sources []string
		if err == nil {
			dir = filepath.Join(dir, "recovery")
			store, err = newRecoveryStore(dir)
		}
		if err == nil {
			drafts, sources, err = readRecoveryDrafts(dir, recoveryProcessAlive)
		}
		return func(a *app) {
			a.recovery = store
			if err != nil {
				a.mainLayerPtr().status = "recovery: " + err.Error()
			}
			if len(drafts) > 0 {
				a.pushLayer(&recoveryLayer{drafts: drafts, sources: sources})
			}
		}
	})
}
func (a *app) recoverySnapshot() []recoveryDraft {
	var drafts []recoveryDraft
	for _, s := range a.mainLayerPtr().sessions {
		if !s.IsDirty() {
			continue
		}
		drafts = append(drafts, recoveryDraft{Title: s.title, Path: s.sourcePath, Text: s.editor.buf.Text(), Baseline: s.savedText, Catalog: s.activeCatalog, Format: s.format, Fingerprint: s.diskFingerprint, DiskKnown: s.diskKnown})
	}
	return drafts
}
func (a *app) scheduleRecovery() {
	if a.recovery == nil || a.recoveryBusy {
		return
	}
	a.recoveryBusy = true
	a.recoveryGeneration++
	gen := a.recoveryGeneration
	store := a.recovery
	drafts := a.recoverySnapshot()
	a.runBackground(func() func(*app) {
		err := store.write(gen, drafts)
		return func(a *app) {
			a.recoveryBusy = false
			if err != nil {
				a.mainLayerPtr().status = "recovery: " + err.Error()
			}
		}
	})
}
func (a *app) finishRecovery() {
	if a.recovery == nil {
		return
	}
	if !a.discardRecovery {
		a.recoveryGeneration++
		_ = a.recovery.write(a.recoveryGeneration, a.recoverySnapshot())
	}
	a.recovery.close(a.discardRecovery)
}
func (a *app) persistRestoredDrafts(sources []string) {
	if a.recovery == nil {
		return
	} // Leave original snapshots intact until a new one exists.
	a.recoveryGeneration++
	gen := a.recoveryGeneration
	store := a.recovery
	drafts := a.recoverySnapshot()
	a.runBackground(func() func(*app) {
		err := store.write(gen, drafts)
		if err == nil {
			for _, path := range sources {
				if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = removeErr
				}
			}
		}
		return func(a *app) {
			if err != nil {
				a.mainLayerPtr().status = "recovery: " + err.Error()
			}
		}
	})
}
