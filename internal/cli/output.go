package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func protectOutput(path string, inputs []string) error {
	dest, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if input == "" {
			continue
		}
		inAbs, err := filepath.Abs(input)
		if err != nil {
			return err
		}
		if abs == inAbs {
			return fmt.Errorf("output is an input file")
		}
		st, err := os.Stat(input)
		if err != nil {
			return err
		}
		if dest != nil && os.SameFile(dest, st) {
			return fmt.Errorf("output aliases an input file")
		}
	}
	return nil
}
func output(ctx context.Context, path string, overwrite bool, inputs []string, stdout io.Writer, produce func(io.Writer) error) error {
	if path == "" {
		return produce(contextWriter{ctx, stdout})
	}
	if err := protectOutput(path, inputs); err != nil {
		return err
	}
	if !overwrite {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("output already exists")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".innodb-reader-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = produce(contextWriter{ctx, f}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = protectOutput(path, inputs); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if overwrite {
		return os.Rename(name, path)
	}
	return os.Link(name, path)
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return n, err
}
