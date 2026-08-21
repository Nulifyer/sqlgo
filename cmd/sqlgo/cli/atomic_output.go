package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// atomicOutput writes beside the destination and replaces it only after the
// operation succeeds. A failed query therefore cannot truncate a valid file.
type atomicOutput struct {
	file        *os.File
	temporary   string
	destination string
	done        bool
}

func createAtomicOutput(destination string) (*atomicOutput, error) {
	dir := filepath.Dir(destination)
	base := filepath.Base(destination)
	file, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(destination); statErr == nil {
		if chmodErr := file.Chmod(info.Mode().Perm()); chmodErr != nil {
			_ = file.Close()
			_ = removeIfPresent(file.Name())
			return nil, chmodErr
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		_ = file.Close()
		_ = removeIfPresent(file.Name())
		return nil, statErr
	}
	return &atomicOutput{
		file:        file,
		temporary:   file.Name(),
		destination: destination,
	}, nil
}

func (o *atomicOutput) Writer() io.Writer { return o.file }

func (o *atomicOutput) Finish(commit bool) error {
	if o == nil || o.done {
		return nil
	}
	o.done = true

	if !commit {
		return errors.Join(o.file.Close(), removeIfPresent(o.temporary))
	}
	if err := o.file.Sync(); err != nil {
		_ = o.file.Close()
		_ = removeIfPresent(o.temporary)
		return fmt.Errorf("sync temporary output: %w", err)
	}
	if err := o.file.Close(); err != nil {
		_ = removeIfPresent(o.temporary)
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := replaceOutputFile(o.temporary, o.destination); err != nil {
		_ = removeIfPresent(o.temporary)
		return fmt.Errorf("replace %s: %w", o.destination, err)
	}
	return nil
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
