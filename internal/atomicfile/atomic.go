package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Output writes beside the destination and replaces it only after the
// operation succeeds. Callers must abort unfinished outputs with Finish(false).
type Output struct {
	file        *os.File
	temporary   string
	destination string
	done        bool
}

func Create(destination string) (*Output, error) {
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
	return &Output{
		file:        file,
		temporary:   file.Name(),
		destination: destination,
	}, nil
}

func (o *Output) Writer() io.Writer { return o.file }

// WriteFile replaces destination only after all bytes have been written and
// synced. Existing permissions are preserved; new files are private.
func WriteFile(destination string, data []byte) error {
	out, err := Create(destination)
	if err != nil {
		return err
	}
	defer out.Finish(false)
	if _, err := out.Writer().Write(data); err != nil {
		return err
	}
	return out.Finish(true)
}

func (o *Output) Finish(commit bool) error {
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
