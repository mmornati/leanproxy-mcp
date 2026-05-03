package main

import (
	"fmt"
	"os"
	"runtime"
)

const (
	ExitSuccess                = 0
	ExitGeneral               = 1
	ExitMisuse                = 2
	ExitConfigurationError    = 3
	ExitTokenResolutionFailure = 4
	ExitUpstream          = 125
)

type PosixError struct {
	Code    int
	Message string
	Cause   error
}

func (e *PosixError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *PosixError) Unwrap() error {
	return e.Cause
}

func Exit(code int) {
	os.Exit(code)
}

func ExitWithMessage(code int, msg string) {
	fmt.Fprintf(os.Stderr, "tokengate: error: %s\n", msg)
	os.Exit(code)
}

func ExitWithError(code int, err error) {
	if err == nil {
		os.Exit(code)
	}
	fmt.Fprintf(os.Stderr, "tokengate: error: %v\n", err)
	os.Exit(code)
}

func ExitMisusef(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "tokengate: error: "+format+"\n", args...)
	os.Exit(ExitMisuse)
}

func ExitConfigError(err error) {
	ExitWithError(ExitConfigurationError, err)
}

func ExitTokenError(err error) {
	ExitWithError(ExitTokenResolutionFailure, err)
}

func ExitUpstreamError(err error) {
	ExitWithError(ExitUpstream, err)
}

func GetExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	if pe, ok := err.(*PosixError); ok {
		return pe.Code
	}
	return ExitGeneral
}

func StackTrace() string {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}