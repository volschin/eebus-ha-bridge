package eebus

import (
	"fmt"
	"log"
	"regexp"
)

var (
	logSKIPattern    = regexp.MustCompile(`(?i)\b[0-9a-f]{40}\b`)
	logTokenPattern  = regexp.MustCompile(`(?i)token=[^\s,)]+`)
	logBearerPattern = regexp.MustCompile(`(?i)bearer\s+[^\s,)]+`)
)

func redactLogText(message string) string {
	message = logSKIPattern.ReplaceAllString(message, "[redacted-ski]")
	message = logTokenPattern.ReplaceAllString(message, "token=[redacted]")
	return logBearerPattern.ReplaceAllString(message, "Bearer [redacted]")
}

// shipLogger adapts the ship-go/eebus-go logging.LoggingInterface to the
// bridge's stdlib logger. Trace output (raw per-message JSON) is gated behind
// a separate flag because it is extremely verbose; Debug carries the useful
// SHIP handshake error/abort reasons.
type shipLogger struct {
	trace bool
}

func (l *shipLogger) Trace(args ...interface{}) {
	if l.trace {
		log.Print("[SHIP TRACE] " + redactLogText(fmt.Sprint(args...)))
	}
}

func (l *shipLogger) Tracef(format string, args ...interface{}) {
	if l.trace {
		log.Print("[SHIP TRACE] " + redactLogText(fmt.Sprintf(format, args...)))
	}
}

func (l *shipLogger) Debug(args ...interface{}) {
	log.Print("[SHIP DEBUG] " + redactLogText(fmt.Sprint(args...)))
}

func (l *shipLogger) Debugf(format string, args ...interface{}) {
	log.Print("[SHIP DEBUG] " + redactLogText(fmt.Sprintf(format, args...)))
}

func (l *shipLogger) Info(args ...interface{}) {
	log.Print("[SHIP INFO] " + redactLogText(fmt.Sprint(args...)))
}

func (l *shipLogger) Infof(format string, args ...interface{}) {
	log.Print("[SHIP INFO] " + redactLogText(fmt.Sprintf(format, args...)))
}

func (l *shipLogger) Error(args ...interface{}) {
	log.Print("[SHIP ERROR] " + redactLogText(fmt.Sprint(args...)))
}

func (l *shipLogger) Errorf(format string, args ...interface{}) {
	log.Print("[SHIP ERROR] " + redactLogText(fmt.Sprintf(format, args...)))
}
