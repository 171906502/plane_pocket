// Package config holds startup-time configuration self-checks.
//
// The checks here run before the HTTP server starts accepting connections so
// that a misconfiguration fails loudly at boot instead of producing the
// notorious "blank frontend, zero requests in the logs" failure mode
// documented as N-8 in docs/04-DEPLOYMENT.md §9.
package config

import (
	"errors"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/spf13/cobra"
)

// OriginsFlagName is the name of the CORS flag declared by PocketBase's `serve`
// subcommand (see cmd/serve.go in the vendored source: it is a
// `StringSliceVar` defaulting to `["*"]`).
const OriginsFlagName = "origins"

// wildcardOrigin is the sentinel PocketBase uses to allow every origin.
const wildcardOrigin = "*"

// The messages below intentionally say *how to fix* the problem, because the
// symptom they prevent (a blank page with no requests in the server logs) gives
// the operator almost no clue about the cause.

const originsEmptyMessage = "refusing to start: no CORS origins are configured in production.\n" +
	"The Plane frontends call this API cross-origin, so with no allowed origins the browser blocks " +
	"every request: the UI stays blank and the server logs show no requests at all.\n" +
	"Fix: pass --origins with the exact frontend origins, e.g.\n" +
	"  plane-pocketbase serve --origins=https://app.example.com,https://space.example.com"

const originsWildcardMessage = "refusing to start: CORS origins contain the wildcard \"*\".\n" +
	"A wildcard origin cannot be combined with credentialed (cookie) requests: browsers reject the " +
	"response, which leaves the frontend blank with no requests in the server logs.\n" +
	"Fix: pass --origins with the exact frontend origins instead of \"*\", e.g.\n" +
	"  plane-pocketbase serve --origins=https://app.example.com,https://space.example.com"

const devWarningMessage = "warning: CORS origins are empty or contain \"*\" while running in dev mode " +
	"(PLANE_PB_DEV=1); continuing.\n" +
	"This configuration is refused in production. Before deploying, pass --origins with the exact " +
	"frontend origins, e.g.\n" +
	"  plane-pocketbase serve --origins=https://app.example.com,https://space.example.com"

// ErrOriginsEmpty is returned by CheckServeOrigins when production mode has no
// configured CORS origins.
var ErrOriginsEmpty = errors.New(originsEmptyMessage)

// ErrOriginsWildcard is returned by CheckServeOrigins when production mode
// allows the "*" wildcard origin.
var ErrOriginsWildcard = errors.New(originsWildcardMessage)

// IsDev reports whether the process is running in Plane dev mode. This mirrors
// the PLANE_PB_DEV switch consumed in main.go.
func IsDev() bool {
	return os.Getenv("PLANE_PB_DEV") == "1"
}

// ReadServeOrigins reads the value of the `--origins` flag bound to
// PocketBase's `serve` subcommand.
//
// `--origins` is declared inside cmd.NewServeCommand, which pocketbase.Start()
// registers on app.RootCmd immediately before executing it. By the time the
// OnServe hook fires, cobra has parsed the serve command's flags, so the value
// can be read back through the command tree. It returns ok=false when the flag
// cannot be located (e.g. when called from a non-serve path such as `migrate`).
func ReadServeOrigins(rootCmd *cobra.Command) (origins []string, ok bool) {
	if rootCmd == nil {
		return nil, false
	}

	serveCmd, _, err := rootCmd.Find([]string{"serve"})
	if err != nil || serveCmd == nil || serveCmd.Name() != "serve" {
		return nil, false
	}

	if serveCmd.Flags().Lookup(OriginsFlagName) == nil {
		return nil, false
	}

	values, err := serveCmd.Flags().GetStringSlice(OriginsFlagName)
	if err != nil {
		return nil, false
	}

	return normalizeOrigins(values), true
}

// normalizeOrigins trims whitespace and drops empty entries so that
// `--origins=""` (which pflag expands to `[""]`) is treated as "no origins".
func normalizeOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// CheckServeOrigins validates the CORS origins for a `serve` invocation.
//
// It returns a non-empty warning when the configuration is tolerated but
// suspicious (dev mode) and a non-nil error when startup must be refused
// (production). Callers should treat any non-nil error as fatal.
func CheckServeOrigins(dev bool, origins []string) (warning string, err error) {
	normalized := normalizeOrigins(origins)

	hasWildcard := false
	for _, o := range normalized {
		if o == wildcardOrigin {
			hasWildcard = true
			break
		}
	}

	if dev {
		// Dev is permissive: empty or wildcard origins only warn, so local
		// frontends on arbitrary ports keep working out of the box.
		if len(normalized) == 0 || hasWildcard {
			return devWarningMessage, nil
		}
		return "", nil
	}

	// Production must be explicit and wildcard-free.
	if len(normalized) == 0 {
		return "", ErrOriginsEmpty
	}
	if hasWildcard {
		return "", ErrOriginsWildcard
	}

	return "", nil
}

// RegisterServeOriginsCheck binds the startup self-check to the app's OnServe
// hook. Because the hook fires before the HTTP listener starts serving, a
// failing check aborts startup (the error propagates out of app.Start()) and
// the server never boots into a silently broken state. The `migrate` and other
// non-serve subcommands never trigger OnServe, so they are unaffected.
//
// rootCmd is the app's cobra command tree (pocketbase.PocketBase.RootCmd); it
// is passed separately because core.App does not expose it.
func RegisterServeOriginsCheck(app core.App, rootCmd *cobra.Command) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Id: "planeServeOriginsCheck",
		Func: func(e *core.ServeEvent) error {
			origins, ok := ReadServeOrigins(rootCmd)
			if !ok {
				// Not the serve path (or flag unavailable): nothing to check.
				return e.Next()
			}

			warning, err := CheckServeOrigins(IsDev(), origins)
			if err != nil {
				return err
			}
			if warning != "" {
				app.Logger().Warn(warning)
			}

			return e.Next()
		},
	})
}
