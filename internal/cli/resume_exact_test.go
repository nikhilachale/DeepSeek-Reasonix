package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/session"
)

func TestResumeExactFlagValidation(t *testing.T) {
	isolateCLIConfigHome(t)
	t.Chdir(t.TempDir())
	for _, prefix := range [][]string{nil, {"chat"}, {"code"}} {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"--resume-exact"}, "flag needs an argument"},
			{[]string{"--resume-exact="}, "requires a canonical session ID"},
			{[]string{"--resume-exact=../escape"}, "requires a canonical session ID"},
			{[]string{"--resume-exact=session-id:other"}, "requires a canonical session ID"},
			{[]string{"--resume-exact=target", "--resume=other"}, "cannot be combined"},
			{[]string{"--resume-exact=target", "--continue"}, "cannot be combined"},
			{[]string{"--resume-exact=target", "--copy"}, "cannot be combined"},
		} {
			args := append(append([]string{}, prefix...), tc.args...)
			var code int
			stdout, stderr := captureCLIOutput(t, func() { code = Run(args, "test-version") })
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("args=%q code=%d stdout=%q stderr=%q; want %q", args, code, stdout, stderr, tc.want)
			}
		}
	}
}

func exactResumeTestTarget(t *testing.T, id string) (cliResumeTarget, int) {
	t.Helper()
	fs := pflag.NewFlagSet("reasonix", pflag.ContinueOnError)
	exact := fs.String("resume-exact", "", "")
	fs.String("resume", "", "")
	fs.Bool("continue", false, "")
	fs.Bool("copy", false, "")
	if err := fs.Parse([]string{"--resume-exact", id}); err != nil {
		t.Fatal(err)
	}
	return interactiveStartupResumeTarget(fs, "", *exact, false, false)
}

func TestResumeExactIgnoresFileAndQueryMatches(t *testing.T) {
	isolateCLIConfigHome(t)
	dir := resolveCLISessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := session.RootForLegacyDir(dir)
	createCanonicalTestSession(t, root, "target", "the intended conversation")
	createCanonicalTestSession(t, root, "other", "target and missing are mentioned here")
	saveQueryTestSession(t, dir, "legacy-only.jsonl", "missing legacy preview")
	for _, id := range []string{"target", "missing"} {
		if err := os.WriteFile(id, []byte("not a transcript"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	target, code := exactResumeTestTarget(t, "target")
	if code != 0 || !target.exact || target.ref.SessionID != "target" || target.path != "" {
		t.Fatalf("exact target = %+v, code=%d", target, code)
	}
	for _, id := range []string{"missing", "legacy-only", "the intended"} {
		stderr := captureStderr(t, func() {
			got, code := exactResumeTestTarget(t, id)
			if code != 1 || !got.empty() {
				t.Fatalf("%q selected %+v, code=%d", id, got, code)
			}
		})
		if !strings.Contains(stderr, "cannot resume exact canonical session") {
			t.Fatalf("missing exact-session error: %q", stderr)
		}
	}
	var codeFromCLI int
	stderr := captureStderr(t, func() { codeFromCLI = Run([]string{"--resume-exact", "missing"}, "test-version") })
	if codeFromCLI != 1 || !strings.Contains(stderr, "cannot resume exact canonical session") {
		t.Fatalf("CLI missing ID code=%d stderr=%q", codeFromCLI, stderr)
	}
}

func TestResumeExactBypassesPickerLimit(t *testing.T) {
	isolateCLIConfigHome(t)
	dir := resolveCLISessionDir()
	root := session.RootForLegacyDir(dir)
	createCanonicalTestSession(t, root, "oldest", "retain this older conversation")
	for i := range canonicalResumeScanCap {
		createCanonicalTestSession(t, root, fmt.Sprintf("newer-%03d", i), "newer conversation")
	}
	entries := canonicalResumeEntries(t.Context(), dir)
	if len(entries) != canonicalResumeScanCap {
		t.Fatalf("picker has %d entries, want %d", len(entries), canonicalResumeScanCap)
	}
	for _, entry := range entries {
		if entry.target.ref.SessionID == "oldest" {
			t.Fatal("fixture's oldest session unexpectedly fits in the picker")
		}
	}
	target, code := exactResumeTestTarget(t, "oldest")
	if code != 0 || target.ref.SessionID != "oldest" || !target.exact {
		t.Fatalf("old exact target=%+v code=%d", target, code)
	}
}

func TestResumeExactPreservesBoundIdentity(t *testing.T) {
	isolateCLIConfigHome(t)
	dir := resolveCLISessionDir()
	createCanonicalTestSession(t, session.RootForLegacyDir(dir), "target", "retained task")
	target, code := exactResumeTestTarget(t, "target")
	if code != 0 {
		t.Fatal("could not resolve fixture")
	}
	newController := func() *control.Controller {
		ctrl := newOwnedTestController(t, control.Options{
			Executor:   agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard),
			SessionDir: dir, SessionService: cliSessionService(dir), ExclusiveSession: true,
		})
		t.Cleanup(ctrl.Close)
		return ctrl
	}
	ctrl := newController()
	if err := commitStartupResume(nil, nil, ctrl, nil, target, flagTakeoverApproval(false)); err != nil {
		t.Fatal(err)
	}
	if ref, ok := ctrl.SessionRef(); !ok || ref != target.ref {
		t.Fatalf("bound identity=%+v, want %+v", ref, target.ref)
	}
	ctrl.Close()
	if err := cliSessionService(dir).CloseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(session.RootForLegacyDir(dir), "target")); err != nil {
		t.Fatal(err)
	}
	if err := commitStartupResume(nil, nil, newController(), nil, target, flagTakeoverApproval(false)); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("missing canonical session after resolution: %v", err)
	}
}

func TestResumeExactHelpAndCompletion(t *testing.T) {
	isolateCLIConfigHome(t)
	stdout, stderr := captureCLIOutput(t, func() {
		if code := Run([]string{"--help"}, "test-version"); code != 0 {
			t.Fatalf("help code=%d", code)
		}
	})
	if !strings.Contains(stdout+stderr, "--resume-exact") {
		t.Fatal("root help lacks exact resume capability")
	}
	root := cliCompletionRootSpec()
	for _, words := range [][]string{{"reasonix", "--resume-ex"}, {"reasonix", "chat", "--resume-ex"}} {
		got := cliCompletionCandidatesWithValues(root, len(words)-1, words, func(cliCompletionValueKind) []string { return nil })
		if len(got) != 1 || got[0] != "--resume-exact" {
			t.Fatalf("completion=%v", got)
		}
	}
}
