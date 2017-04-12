package goprof

import "log"

// LogFxn is function which is used for writing log messages
// It doesn't have many levels, since all the messages has quite the same level
type LogFxn func(format string, args ...interface{})

var logf = log.Printf

// SetLogFunction changes function used for logging.
// Logging is very basic and doesn't have many levels, since all the messages has quite the same level
func SetLogFunction(fxn LogFxn) {
	logf = fxn
}
