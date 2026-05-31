// Package clilog provides centralized output control for CLI informational messages.
// When quiet mode is enabled, informational output is suppressed while errors
// (stderr) and structured data output (json/yaml/table) remain visible.
package clilog

import "fmt"

var quiet bool

// SetQuiet enables or disables quiet mode globally.
func SetQuiet(q bool) { quiet = q }

// IsQuiet returns whether quiet mode is currently enabled.
func IsQuiet() bool { return quiet }

// Infof prints a formatted informational message to stdout unless quiet mode is enabled.
func Infof(format string, a ...any) {
	if !quiet {
		fmt.Printf(format, a...)
	}
}

// Infoln prints an informational message line to stdout unless quiet mode is enabled.
func Infoln(a ...any) {
	if !quiet {
		fmt.Println(a...)
	}
}
