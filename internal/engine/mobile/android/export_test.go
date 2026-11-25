package android

// These make the helpers that the tests check reachable from the external test package.
var (
	ShellQuote   = shellQuote
	ShellCommand = shellCommand
	FailureLine  = failureLine
	CommandError = commandError
)
