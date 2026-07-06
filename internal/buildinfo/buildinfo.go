package buildinfo

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
)

func String() string {
	return fmt.Sprintf("%s (%s)", Version, Commit)
}
