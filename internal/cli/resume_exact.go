package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"
	"reasonix/internal/session"
)

func interactiveStartupResumeTarget(fs *pflag.FlagSet, query, exact string, cont, copySession bool) (cliResumeTarget, int) {
	if !fs.Changed("resume-exact") {
		return interactiveResumeTarget(query, cont, copySession)
	}
	if exact == "" || exact != strings.TrimSpace(exact) || session.ValidateSessionID(exact) != nil {
		fmt.Fprintln(os.Stderr, "--resume-exact requires a canonical session ID")
		return cliResumeTarget{}, 2
	}
	for _, other := range []string{"resume", "continue", "copy"} {
		if fs.Changed(other) {
			fmt.Fprintln(os.Stderr, "--resume-exact cannot be combined with --"+other)
			return cliResumeTarget{}, 2
		}
	}
	// Direct service lookup deliberately bypasses cwd files, query matching,
	// picker limits and the legacy transcript compatibility path.
	ref, err := cliCanonicalRouteRef(resolveCLISessionDir(), cliCanonicalRoute(exact))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resume exact canonical session:", err)
		return cliResumeTarget{}, 1
	}
	return cliResumeTarget{ref: ref, exact: true}, 0
}
