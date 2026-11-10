// Command wkperf compares WebKit Web Inspector Timelines exports.
package main

import (
	"os"

	"github.com/omniviewdev/wkperf/cmd"
)

func main() {
	if err := cmd.Execute(os.Args[1:]); err != nil {
		code := 1
		if ee, ok := err.(*cmd.ExitError); ok {
			code = ee.Code
		}
		os.Exit(code)
	}
}
