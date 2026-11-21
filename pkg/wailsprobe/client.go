package wailsprobe

import _ "embed"

//go:embed client.js
var clientJS string

// ClientJS returns the opt-in frontend ES module. It does not import or modify
// the Wails runtime; the application supplies Call.ByName or bound callbacks.
func ClientJS() string { return clientJS }
