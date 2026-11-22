package mcp

import "strings"

// logReplacer strips CR and LF from a request-derived value (an error's text,
// the method, a client-chosen id) before it is logged, so the value cannot
// start a forged log line (CWE-117, CodeQL go/log-injection).
var logReplacer = strings.NewReplacer("\r", "", "\n", "")
