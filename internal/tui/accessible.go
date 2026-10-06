package tui

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// runAccessible asks fields one after another as plain lines on out, reading the answers from in, as
// huh's accessible form (TERM=dumb) does, but stops at the end of in or of ctx. huh v1.0.0's discards
// every field's error, takes the end of input for each remaining field's default answer and ignores
// ctx: Ctrl+D would submit the form, and a stop signal would leave it waiting for a line. The fields
// are the form's, which gave them its theme and keys. runAccessible returns huh.ErrUserAborted when
// in ends before every field has its answer, as Esc does in the terminal's form; ctx's error when
// ctx ends; or the first error reading in or from a field.
func runAccessible(ctx context.Context, fields []huh.Field, in io.Reader, out io.Writer) error {
	input := &formInput{ctx: ctx, in: in}
	for _, f := range fields {
		f.Init()
		f.Focus()
		err := f.WithAccessible(true).RunAccessible(out, input) //nolint:staticcheck // as huh's form calls it
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(input.err, io.EOF):
			return huh.ErrUserAborted
		case input.err != nil:
			return fmt.Errorf("reading the answer: %w", input.err)
		case err != nil:
			return err
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	return nil
}

// formInput is an accessible form's input: in, read until it ends or ctx does. huh's fields read
// their answers with a scanner that takes any error for the end of input and answers with the
// default, so err keeps the first one for runAccessible to see.
type formInput struct {
	ctx context.Context
	in  io.Reader
	err error // the first error in or ctx ended the input with
}

// Read reads from in. While ctx can end, it reads on a goroutine of its own, so as to return as soon
// as ctx ends; that goroutine reads one more time from in, its answer dropped, as no one asks for
// another once the input has ended.
func (r *formInput) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	var n int
	if r.ctx.Done() == nil {
		n, r.err = r.in.Read(p)
		return n, r.err
	}
	type read struct {
		data []byte
		err  error
	}
	done := make(chan read, 1) // the goroutine leaves without waiting for Read
	go func() {
		buf := make([]byte, len(p))
		n, err := r.in.Read(buf)
		done <- read{buf[:n], err}
	}()
	select {
	case <-r.ctx.Done():
		r.err = r.ctx.Err()
		return 0, r.err
	case got := <-done:
		r.err = got.err
		return copy(p, got.data), got.err
	}
}
