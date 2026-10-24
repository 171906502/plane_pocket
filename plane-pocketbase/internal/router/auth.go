package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/auth"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// registerAuth wires Plane's /auth/* endpoints.
//
// Sign-in/sign-up/sign-out/magic-sign-in are native HTML form POSTs answered
// with a 302 (see internal/handlers/auth), so no JSON middleware is applied.
//
// The credential-verifying endpoints — sign-in, magic-generate, magic-sign-in
// and forgot-password (Plane's request-password-reset) — share one per-IP rate
// limiter so an attacker cannot multiply their budget by rotating routes. The
// non-guessing routes (sign-up, sign-out, get-csrf-token, email-check,
// set-password) are deliberately left unlimited.
func registerAuth(e *core.ServeEvent, app core.App) {
	h := auth.New(app)

	limit := middleware.NewAuthRateLimiter().Middleware()

	e.Router.GET("/auth/get-csrf-token/{$}", h.CSRFToken)
	e.Router.POST("/auth/email-check/{$}", h.EmailCheck)
	e.Router.POST("/auth/sign-in/{$}", h.SignIn).BindFunc(limit)
	e.Router.POST("/auth/sign-up/{$}", h.SignUp)
	e.Router.POST("/auth/sign-out/{$}", h.SignOut)
	e.Router.POST("/auth/magic-generate/{$}", h.MagicGenerate).BindFunc(limit)
	e.Router.POST("/auth/magic-sign-in/{$}", h.MagicSignIn).BindFunc(limit)
	e.Router.POST("/auth/forgot-password/{$}", h.ForgotPassword).BindFunc(limit)
	e.Router.POST("/auth/set-password/{$}", h.SetPassword)
}
