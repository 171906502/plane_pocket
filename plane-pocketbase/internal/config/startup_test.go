package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestCheckServeOrigins is the table-driven acceptance test. It asserts the
// exact message text (not merely that an error is non-nil), because the whole
// point of the check is to hand the operator an actionable message.
func TestCheckServeOrigins(t *testing.T) {
	cases := []struct {
		name        string
		dev         bool
		origins     []string
		wantWarning string
		wantErr     error
	}{
		{
			name:    "production + empty origins refuses to start",
			dev:     false,
			origins: nil,
			wantErr: ErrOriginsEmpty,
		},
		{
			name:    "production + wildcard refuses to start",
			dev:     false,
			origins: []string{"*"},
			wantErr: ErrOriginsWildcard,
		},
		{
			name:        "dev + empty origins warns and continues",
			dev:         true,
			origins:     nil,
			wantWarning: devWarningMessage,
		},
		{
			name:        "dev + wildcard warns and continues",
			dev:         true,
			origins:     []string{"*"},
			wantWarning: devWarningMessage,
		},
		{
			name:    "production + concrete origin list passes",
			dev:     false,
			origins: []string{"https://app.example.com", "https://space.example.com"},
		},
		{
			name:    "dev + concrete origin list passes silently",
			dev:     true,
			origins: []string{"https://app.example.com"},
		},
		{
			name:    "production + blank entry is treated as empty",
			dev:     false,
			origins: []string{"", "   "},
			wantErr: ErrOriginsEmpty,
		},
		{
			name:    "production + wildcard among concrete origins refuses",
			dev:     false,
			origins: []string{"https://app.example.com", "*"},
			wantErr: ErrOriginsWildcard,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warning, err := CheckServeOrigins(tc.dev, tc.origins)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("want error text %q, got nil", tc.wantErr.Error())
				}
				if got := err.Error(); got != tc.wantErr.Error() {
					t.Fatalf("error text mismatch:\n got: %q\nwant: %q", got, tc.wantErr.Error())
				}
			}

			if warning != tc.wantWarning {
				t.Fatalf("warning text mismatch:\n got: %q\nwant: %q", warning, tc.wantWarning)
			}
		})
	}
}

// TestOriginsMessagesAreActionable guards against someone replacing the
// messages with a generic "invalid config" string: every message must tell the
// operator to fix it via --origins.
func TestOriginsMessagesAreActionable(t *testing.T) {
	messages := map[string]string{
		"originsEmptyMessage":    originsEmptyMessage,
		"originsWildcardMessage": originsWildcardMessage,
		"devWarningMessage":      devWarningMessage,
	}

	for name, msg := range messages {
		if !strings.Contains(msg, "--origins") {
			t.Errorf("%s must tell the operator how to fix it using --origins; got %q", name, msg)
		}
	}
}

// TestReadServeOrigins proves the actual reading mechanism: the --origins value
// is read back from the `serve` subcommand of the cobra command tree (mirrors
// how pocketbase.Start() registers cmd.NewServeCommand on app.RootCmd).
func TestReadServeOrigins(t *testing.T) {
	newRoot := func(run func()) *cobra.Command {
		root := &cobra.Command{Use: "plane-pocketbase"}
		var allowed []string
		serve := &cobra.Command{
			Use:  "serve",
			RunE: func(*cobra.Command, []string) error { run(); return nil },
		}
		serve.PersistentFlags().StringSliceVar(&allowed, OriginsFlagName, []string{"*"}, "CORS allowed domain origins list")
		root.AddCommand(serve)
		return root
	}

	t.Run("explicit list is read back", func(t *testing.T) {
		root := newRoot(func() {})
		root.SetArgs([]string{"serve", "--origins=https://app.example.com,https://space.example.com"})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}

		got, ok := ReadServeOrigins(root)
		if !ok {
			t.Fatal("ReadServeOrigins returned ok=false, want true")
		}
		want := []string{"https://app.example.com", "https://space.example.com"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadServeOrigins = %v, want %v", got, want)
		}
	})

	t.Run("default wildcard is read back", func(t *testing.T) {
		root := newRoot(func() {})
		root.SetArgs([]string{"serve"})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}

		got, ok := ReadServeOrigins(root)
		if !ok {
			t.Fatal("ReadServeOrigins returned ok=false, want true")
		}
		if !reflect.DeepEqual(got, []string{"*"}) {
			t.Fatalf("ReadServeOrigins = %v, want [*]", got)
		}
	})

	t.Run("missing serve command yields ok=false", func(t *testing.T) {
		root := &cobra.Command{Use: "plane-pocketbase"}
		root.AddCommand(&cobra.Command{Use: "migrate", RunE: func(*cobra.Command, []string) error { return nil }})

		if _, ok := ReadServeOrigins(root); ok {
			t.Fatal("ReadServeOrigins returned ok=true for a command tree without serve")
		}
	})
}
