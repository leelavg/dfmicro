package support

import (
	"fmt"
	"regexp"
)

var reVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// ValidateVersionFormat is a soft X.Y.Z check and does not verify version availability.
func ValidateVersionFormat(version string) error {
	if !reVersion.MatchString(version) {
		return fmt.Errorf("version %q must be in X.Y.Z format", version)
	}
	return nil
}
