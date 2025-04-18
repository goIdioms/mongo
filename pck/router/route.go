package router

import (
	"github.com/goIdioms/go-jwt-auth-api/pck/auth/controllers"

	"github.com/goIdioms/go-jwt-auth-api/pck/middleware"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app fiber.Router, authController *controllers.AuthController) {
	app.Post("/sign-up", authController.SignUpUser)
	app.Post("/sign-in", authController.SignInUser)
	app.Get("/logout", middleware.DeserializeUser, authController.LogOutUser)
	app.Post("/refresh", middleware.DeserializeUser, authController.RefreshToken)

	app.Get("/users/me", middleware.DeserializeUser, authController.GetMeHandler)
	app.Get("/users/", middleware.DeserializeUser, middleware.AllowedRoles([]string{"admin", "moderator"}), authController.GetUsersHandler)
}
