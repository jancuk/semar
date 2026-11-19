package cli

import (
	"fmt"
	"io"

	"github.com/jancuk/semar/internal/config"
)

// noteIgnoredSettings tells a person, once per command, about a setting of semar.yaml that Semar no
// longer uses. The file still loads.
func noteIgnoredSettings(stderr io.Writer, cfg config.Config) {
	if cfg.TrustIgnored {
		fmt.Fprintln(stderr, "semar: "+config.TrustNotice)
	}
}
