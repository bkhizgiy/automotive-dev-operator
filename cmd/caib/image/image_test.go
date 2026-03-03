package image

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestShowCommandRejectsMissingBuildName(t *testing.T) {
	called := false
	cmd := newShowCmd(Options{
		RunShow: func(_ *cobra.Command, _ []string) {
			called = true
		},
	})

	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected an error when build name argument is missing")
	}
	if called {
		t.Fatalf("expected RunShow not to be called when args are invalid")
	}
	if !strings.Contains(err.Error(), "accepts 1 arg(s), received 0") {
		t.Fatalf("unexpected error for missing build name: %v", err)
	}
}

func TestShowCommandInvokesHandlerWithBuildName(t *testing.T) {
	var gotArgs []string
	cmd := newShowCmd(Options{
		RunShow: func(_ *cobra.Command, args []string) {
			gotArgs = append([]string{}, args...)
		},
	})

	cmd.SetArgs([]string{"my-build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected command to execute successfully: %v", err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "my-build" {
		t.Fatalf("expected RunShow to receive [my-build], got %v", gotArgs)
	}
}
