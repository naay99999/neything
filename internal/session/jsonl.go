package session

import (
	"bufio"
	"bytes"
	"io"
	"os"
)

const maxLine = 16 << 20

// Lines calls fn for each complete line of r; lines over maxLine (16 MiB)
// and a final line without '\n' are skipped. The slice passed to fn is only
// valid during the call.
func Lines(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull {
			if err == io.EOF {
				return nil // a trailing partial line is dropped
			}
			return err
		}
		if !skipping {
			buf = append(buf, chunk...)
			if len(buf) > maxLine {
				skipping = true
				buf = buf[:0]
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if skipping {
			skipping = false
			continue
		}
		fn(bytes.TrimSuffix(buf, []byte("\n")))
		buf = buf[:0]
	}
}

// TailReader returns a reader over the last n bytes of f, starting after the
// first '\n' in that window (so it never yields a partial first line). When
// the file is no larger than n the whole file is returned.
func TailReader(f *os.File, n int64) (io.Reader, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size <= n {
		return io.NewSectionReader(f, 0, size), nil
	}
	br := bufio.NewReader(io.NewSectionReader(f, size-n, n))
	if _, err := br.ReadBytes('\n'); err != nil {
		if err == io.EOF {
			return bytes.NewReader(nil), nil
		}
		return nil, err
	}
	return br, nil
}
