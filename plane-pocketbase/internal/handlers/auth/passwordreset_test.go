package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// TestDeliverResetTokenSharesMagicFile pins the core contract of T-5: a
// password-reset token and a magic sign-in code are delivered through the same
// function into the same file, and each line carries a `kind=` marker so the
// operator can tell them apart.
func TestDeliverResetTokenSharesMagicFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.log")
	t.Setenv("PLANE_MAGIC_LOG", path)
	t.Setenv("PLANE_MAIL_LOG", "")

	deliverMagicCode("alice@example.com", "135790")
	deliverResetToken("alice@example.com", "reset-token-abc123")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("delivery file not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines in one file, got %d: %q", len(lines), string(raw))
	}
	t.Logf("delivery file:\n%s", strings.TrimSpace(string(raw)))
	if !strings.Contains(lines[0], "kind=magic-sign-in") || !strings.Contains(lines[0], "135790") {
		t.Fatalf("magic line missing kind marker/code: %q", lines[0])
	}
	if !strings.Contains(lines[1], "kind=password-reset") || !strings.Contains(lines[1], "reset-token-abc123") {
		t.Fatalf("reset line missing kind marker/token: %q", lines[1])
	}
}

// TestMailLogEnvOverride pins the back-compat naming rule: PLANE_MAGIC_LOG
// remains the primary variable, but PLANE_MAIL_LOG overrides it when both are
// set. Delivery must follow the winning path.
func TestMailLogEnvOverride(t *testing.T) {
	dir := t.TempDir()
	magicPath := filepath.Join(dir, "magic.log")
	mailPath := filepath.Join(dir, "mail.log")

	t.Run("mail log wins when both set", func(t *testing.T) {
		t.Setenv("PLANE_MAGIC_LOG", magicPath)
		t.Setenv("PLANE_MAIL_LOG", mailPath)

		deliverResetToken("bob@example.com", "tok-both")

		if raw, err := os.ReadFile(mailPath); err != nil || !strings.Contains(string(raw), "tok-both") {
			t.Fatalf("PLANE_MAIL_LOG must win; mail file: %q err=%v", string(raw), err)
		}
		if _, err := os.Stat(magicPath); !os.IsNotExist(err) {
			t.Fatalf("magic file must not be written when PLANE_MAIL_LOG is set (err=%v)", err)
		}
	})

	t.Run("magic log used when mail log unset", func(t *testing.T) {
		t.Setenv("PLANE_MAGIC_LOG", magicPath)
		t.Setenv("PLANE_MAIL_LOG", "")

		deliverResetToken("bob@example.com", "tok-magic")

		if raw, err := os.ReadFile(magicPath); err != nil || !strings.Contains(string(raw), "tok-magic") {
			t.Fatalf("PLANE_MAGIC_LOG must be honoured; magic file: %q err=%v", string(raw), err)
		}
	})

	t.Run("mail log used when magic log unset", func(t *testing.T) {
		mailOnly := filepath.Join(dir, "mail-only.log")
		t.Setenv("PLANE_MAGIC_LOG", "")
		t.Setenv("PLANE_MAIL_LOG", mailOnly)

		deliverResetToken("bob@example.com", "tok-mail-only")

		if raw, err := os.ReadFile(mailOnly); err != nil || !strings.Contains(string(raw), "tok-mail-only") {
			t.Fatalf("PLANE_MAIL_LOG must be honoured on its own; file: %q err=%v", string(raw), err)
		}
	})
}

// TestDeliverResetTokenFallsBackToProcessLog pins the unset behaviour: with no
// file configured the token goes to the process log so a local run still works.
func TestDeliverResetTokenFallsBackToProcessLog(t *testing.T) {
	t.Setenv("PLANE_MAGIC_LOG", "")
	t.Setenv("PLANE_MAIL_LOG", "")

	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()

	deliverResetToken("carol@example.com", "reset-246810")

	if !strings.Contains(buf.String(), "reset-246810") {
		t.Fatalf("fallback log missing the token: %q", buf.String())
	}
}

// TestDeliverResetTokenUnwritablePathStillLogs guards the failure mode: a bad
// path must not swallow the token, or the operator is locked out with no trace.
func TestDeliverResetTokenUnwritablePathStillLogs(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file is expected: OpenFile fails deterministically.
	t.Setenv("PLANE_MAGIC_LOG", filepath.Join(dir, "as-dir"))
	t.Setenv("PLANE_MAIL_LOG", "")
	if err := os.Mkdir(filepath.Join(dir, "as-dir"), 0o700); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()

	deliverResetToken("dave@example.com", "reset-975310")

	if !strings.Contains(buf.String(), "reset-975310") {
		t.Fatalf("token lost when the delivery path is unusable: %q", buf.String())
	}
}

// TestForgotPasswordDeliversTokenToMailLog is the end-to-end guard: the real
// handler must route the generated token through the shared delivery file with
// the password-reset marker, while keeping its HTTP response shape unchanged.
func TestForgotPasswordDeliversTokenToMailLog(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	coll, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(coll)
	user.Set("uuid", serializers.NewUUID())
	user.Set("username", "reset-user")
	user.Set("email", "reset@example.com")
	user.Set("password", "1234567890")
	user.Set("display_name", "Reset")
	user.Set("last_active", "2024-01-01 00:00:00.000Z")
	user.Set("is_active", true)
	if err := app.Save(user); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	path := filepath.Join(t.TempDir(), "mail.log")
	t.Setenv("PLANE_MAGIC_LOG", path)
	t.Setenv("PLANE_MAIL_LOG", "")

	body, _ := json.Marshal(map[string]string{"email": "reset@example.com"})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app}
	e.Request = req
	e.Response = rec

	if err := New(app).ForgotPassword(e); err != nil {
		t.Fatalf("forgot-password: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot-password status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Check your email to reset your password") {
		t.Fatalf("response shape changed: %q", rec.Body.String())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reset token not delivered to the shared file: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(raw)), "\t")
	if len(fields) < 4 {
		t.Fatalf("malformed delivery line: %q", string(raw))
	}
	if fields[1] != "kind=password-reset" {
		t.Fatalf("kind marker = %q, want kind=password-reset (line %q)", fields[1], string(raw))
	}
	if fields[2] != "reset@example.com" {
		t.Fatalf("email field = %q, want reset@example.com", fields[2])
	}
	token := fields[3]
	if len(token) != 40 {
		t.Fatalf("token = %q, want a 40-char hex string", token)
	}
	if _, ok := app.Store().GetOk("password_reset_" + token); !ok {
		t.Fatalf("delivered token %q was not the one stored for reset", token)
	}
}
