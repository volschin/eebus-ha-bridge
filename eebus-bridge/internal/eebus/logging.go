package eebus

import "log"

// shipLogger adapts the ship-go/eebus-go logging.LoggingInterface to the
// bridge's stdlib logger. Trace output (raw per-message JSON) is gated behind
// a separate flag because it is extremely verbose; Debug carries the useful
// SHIP handshake error/abort reasons.
type shipLogger struct {
	trace bool
}

func (l *shipLogger) Trace(args ...interface{}) {
	if l.trace {
		log.Print(append([]interface{}{"[SHIP TRACE] "}, args...)...)
	}
}

func (l *shipLogger) Tracef(format string, args ...interface{}) {
	if l.trace {
		log.Printf("[SHIP TRACE] "+format, args...)
	}
}

func (l *shipLogger) Debug(args ...interface{}) {
	log.Print(append([]interface{}{"[SHIP DEBUG] "}, args...)...)
}

func (l *shipLogger) Debugf(format string, args ...interface{}) {
	log.Printf("[SHIP DEBUG] "+format, args...)
}

func (l *shipLogger) Info(args ...interface{}) {
	log.Print(append([]interface{}{"[SHIP INFO] "}, args...)...)
}

func (l *shipLogger) Infof(format string, args ...interface{}) {
	log.Printf("[SHIP INFO] "+format, args...)
}

func (l *shipLogger) Error(args ...interface{}) {
	log.Print(append([]interface{}{"[SHIP ERROR] "}, args...)...)
}

func (l *shipLogger) Errorf(format string, args ...interface{}) {
	log.Printf("[SHIP ERROR] "+format, args...)
}
