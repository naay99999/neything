package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

type spinner struct {
	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// startSpinner begins an animated status line on stderr. It returns nil
// (a no-op receiver) when output isn't an interactive terminal or JSON
// output was requested, so callers can unconditionally defer/call Stop().
func startSpinner(message string) *spinner {
	if !colorEnabled || flagJSON {
		return nil
	}
	s := &spinner{stop: make(chan struct{})}
	s.wg.Add(1)
	go s.run(message)
	return s
}

func (s *spinner) run(message string) {
	defer s.wg.Done()
	ticker := time.NewTicker(90 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			fmt.Fprint(os.Stderr, "\r\033[K")
			return
		case <-ticker.C:
			frame := string(spinnerFrames[i%len(spinnerFrames)])
			fmt.Fprintf(os.Stderr, "\r%s %s", Cyan(frame), Dim(message))
			i++
		}
	}
}

func (s *spinner) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.stop)
	})
	s.wg.Wait()
}

// typewrite prints s with a brief per-rune delay on an interactive terminal,
// falling back to a plain Println for JSON output or non-TTY stdout.
func typewrite(s string) {
	if !colorEnabled || flagJSON {
		fmt.Println(s)
		return
	}
	for _, r := range s {
		fmt.Print(string(r))
		time.Sleep(2 * time.Millisecond)
	}
	fmt.Println()
}
