package cmd

// Link-time overrides (-X). Empty keeps the in-code red-team defaults.
const defaultProcessName = "rsyslogd"

var (
	compiledProcessName string
	compiledQuiet       string
)
