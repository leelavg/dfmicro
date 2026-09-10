package buildinfo

import "fmt"

var Version = "dev"
var Commit = "none"
var BuildTime = "unknown"

func String() string {
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, BuildTime)
}
