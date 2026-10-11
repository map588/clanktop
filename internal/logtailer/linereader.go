package logtailer

import (
	"bufio"
	"io"
	"os"
)

// LineReader reads the complete lines that a writer appends to a file.
type LineReader struct {
	f       *os.File
	r       *bufio.Reader
	partial []byte
}

func OpenLineReader(path string) (*LineReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &LineReader{f: f, r: bufio.NewReaderSize(f, 1<<20)}, nil
}

func (lr *LineReader) Close() error {
	return lr.f.Close()
}

// ReadLines calls fn for each complete line appended since the last call.
// A line without its newline stays in the reader until the rest arrives,
// because the writer can be in the middle of the line.
func (lr *LineReader) ReadLines(fn func(line []byte)) error {
	for {
		chunk, err := lr.r.ReadBytes('\n')
		lr.partial = append(lr.partial, chunk...)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		line := lr.partial
		lr.partial = nil
		fn(line)
	}
}
