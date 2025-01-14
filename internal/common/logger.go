package common

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

type LogLevel int

const (
	INFO LogLevel = iota
	WARNING
	ERROR
)

var (
	logger     *log.Logger
	loggerOnce sync.Once
)

// TODO(akul): Maybe we can use init for apiClient and redisClient as well.
func init() {
	loggerOnce.Do(func() {
		logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
	})
}

func GetLogger() *log.Logger {
	return logger
}

func Log(level LogLevel, format string, args ...interface{}) {
	prefix := ""
	switch level {
	case INFO:
		prefix = "INFO: "
	case WARNING:
		prefix = "WARNING: "
	case ERROR:
		prefix = "ERROR: "
	}
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	formattedMessage := fmt.Sprintf(format, args...)
	logger.SetPrefix(prefix)
	logger.Output(2, formattedMessage)
}
