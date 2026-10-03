package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/auth"
)

// registerAuth wires Plane's /auth/* endpoints.
//
// Sign-in/sign-up/sign-out/magic-sign-in are native HTML form POSTs answered
// with a 302 (see internal/handlers/auth), so no JSON middleware is applied.
func registerAuth(e *core.ServeEvent, app core.App) {
	h := auth.New(app)

	e.Router.GET("/auth/get-csrf-token/{$}", h.CSRFToken)
	e.Router.POST("/auth/email-check/{$}", h.EmailCheck)
	e.Router.POST("/auth/sign-in/{$}", h.SignIn)
	e.Router.POST("/auth/sign-up/{$}", h.SignUp)
	e.Router.POST("/auth/sign-out/{$}", h.SignOut)
	e.Router.POST("/auth/magic-generate/{$}", h.MagicGenerate)
	e.Router.POST("/auth/magic-sign-in/{$}", h.MagicSignIn)
	e.Router.POST("/auth/forgot-password/{$}", h.ForgotPassword)
	e.Router.POST("/auth/set-password/{$}", h.SetPassword)
}
