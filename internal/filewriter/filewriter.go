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
	path  string
	queue chan string
	done  chan struct{}
}

func New(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("could not open file %s: %w", path, err)
	}
	w := &Writer{
		path:  path,
		queue: make(chan string, defaultQueueSize),
		done:  make(chan struct{}),
	}
	go w.run(f)
	return w, nil
}

func (w *Writer) run(f *os.File) {
	defer close(w.done)
	defer f.Close()
	for entry := range w.queue {
		if _, err := f.WriteString(entry); err != nil {
			log.Printf("write to %s failed: %v", w.path, err)
		}
	}
}

func (w *Writer) Write(s string) {
	w.queue <- s
}

func (w *Writer) Close() {
	close(w.queue)
	<-w.done
}
