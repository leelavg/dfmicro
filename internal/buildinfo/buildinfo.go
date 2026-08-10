package buildinfo

import "fmt"

var Version = "dev"
var Commit = "none"

func String() string {
	return fmt.Sprintf("%s (%s)", Version, Commit)
}
