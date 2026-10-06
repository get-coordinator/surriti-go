package surriti

import (
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarning
	LogError
	LogCritical
)

var (
	logMu           sync.Mutex
	packageLogger   = log.New(io.Discard, "surriti ", log.LstdFlags)
	packageLogLevel = LogInfo
)

func parseLogLevel(v string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "DEBUG":
		return LogDebug
	case "WARNING", "WARN":
		return LogWarning
	case "ERROR":
		return LogError
	case "CRITICAL", "FATAL":
		return LogCritical
	default:
		return LogInfo
	}
}

// SetupLogging is the opt-in package logging hook corresponding to Python's
// setup_logging(). Repeated calls update the level without stacking handlers.
func SetupLogging(level string, writer io.Writer) *log.Logger {
	logMu.Lock()
	defer logMu.Unlock()
	if level == "" || strings.EqualFold(level, "INFO") {
		if env := os.Getenv("SURRITI_LOG_LEVEL"); env != "" {
			level = env
		}
	}
	if writer == nil {
		writer = os.Stderr
	}
	packageLogLevel = parseLogLevel(level)
	packageLogger.SetOutput(writer)
	return packageLogger
}

func packageLogf(level LogLevel, format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	if level < packageLogLevel {
		return
	}
	packageLogger.Printf(format, args...)
}
