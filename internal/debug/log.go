//go:build debug

package debug

import (
	"fmt"
	"os"
	"sync"
)

var (
	logFile *os.File
	once    sync.Once
)

const logPath = "/tmp/clanktop-debug.log"

func init() {
	once.Do(func() {
		var err error
		logFile, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logFile = nil
		}
	})
}

func Log(format string, args ...any) {
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, format+"\n", args...)
}
