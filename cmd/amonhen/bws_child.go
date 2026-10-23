package main

import (
	"os"
	"runtime"
	"strings"
)

// bwsChildCommand restores runtime variables removed by bws --no-inherit-env.
// The allowlist deliberately excludes authentication and application variables.
func bwsChildCommand(args []string) []string {
	if runtime.GOOS != "windows" {
		return args
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	var script strings.Builder
	for _, key := range []string{"PATH", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "SystemRoot", "ComSpec", "ProgramData", "MISE_DATA_DIR", "MISE_STATE_DIR", "MISE_CACHE_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			script.WriteString("$env:" + key + "=" + quote(value) + "; ")
		}
	}
	script.WriteString("$global:LASTEXITCODE=$null; & ")
	for _, arg := range args {
		script.WriteString(quote(arg) + " ")
	}
	script.WriteString("; $commandSucceeded=$?; if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }; if (-not $commandSucceeded) { exit 1 }")
	return []string{script.String()}
}
