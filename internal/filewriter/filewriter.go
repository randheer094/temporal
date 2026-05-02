// Package filewriter provides a serialized, non-blocking writer for a single
// file. Writes are queued on a buffered channel and drained by one consumer
// goroutine, preserving submission order.
package filewriter

import (
	"fmt"
	"log"
	"os"
)

const defaultQueueSize = 1024

type Writer struct {
	path     string
	maxBytes int64
	queue    chan string
	done     chan struct{}
}

func New(path string) (*Writer, error) {
	return NewWithMaxSize(path, 0)
}

// NewWithMaxSize creates a writer that rotates the file to "<path>.1" when
// it exceeds maxBytes. A maxBytes of 0 disables rotation.
func NewWithMaxSize(path string, maxBytes int64) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("could not open file %s: %w", path, err)
	}
	w := &Writer{
		path:     path,
		maxBytes: maxBytes,
		queue:    make(chan string, defaultQueueSize),
		done:     make(chan struct{}),
	}
	go w.run(f)
	return w, nil
}

func (w *Writer) run(f *os.File) {
	defer close(w.done)
	defer func() { f.Close() }()

	size, _ := currentSize(f)
	for entry := range w.queue {
		n, err := f.WriteString(entry)
		if err != nil {
			log.Printf("write to %s failed: %v", w.path, err)
			continue
		}
		size += int64(n)
		if w.maxBytes > 0 && size >= w.maxBytes {
			rotated, err := w.rotate(f)
			if err != nil {
				log.Printf("rotate %s failed: %v", w.path, err)
				continue
			}
			f = rotated
			size = 0
		}
	}
}

func currentSize(f *os.File) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (w *Writer) rotate(f *os.File) (*os.File, error) {
	if err := f.Close(); err != nil {
		return nil, err
	}
	backup := w.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(w.path, backup); err != nil {
		return nil, err
	}
	return os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
}

func (w *Writer) Write(s string) {
	w.queue <- s
}

func (w *Writer) Close() {
	close(w.queue)
	<-w.done
}
