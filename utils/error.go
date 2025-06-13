package utils

import "log"

// A simple util to fail on error and log
// the error message.
func FailOnError(err error, msg string) {
	if err != nil {
		log.Panicf("%s: %s", msg, err)
	}
}
