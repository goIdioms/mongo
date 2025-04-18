package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/goIdioms/go-jwt-auth-api/pck/auth/services"
	"github.com/goIdioms/go-jwt-auth-api/pck/cache"
	"github.com/goIdioms/go-jwt-auth-api/pck/database"
	"github.com/goIdioms/go-jwt-auth-api/pck/models"
	"github.com/goIdioms/go-jwt-auth-api/pck/utils"
	"github.com/goIdioms/go-jwt-auth-api/pck/validator"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type AuthController struct {
	AuthService services.AuthService
	cache       *cache.RedisCache
}

func NewAuthController(service services.AuthService, cache *cache.RedisCache) *AuthController {
	return &AuthController{AuthService: service, cache: cache}
}

// @Summary Регистрация пользователя
// @Description Создает нового пользователя
// @Tags auth
// @Accept json
// @Produce json
// @Param input body models.SignUpInput true "Данные для регистрации"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /sign-up [post]
func (ac *AuthController) SignUpUser(c *fiber.Ctx) error {
	payload := new(models.SignUpInput)

	if err := c.BodyParser(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"stattus": "fail",
			"message": err.Error(),
		})
	}

	errors := validator.ValidateStruct(payload)
	if errors != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"status": "fail",
			"errors": errors,
		})
	}

	user, err := ac.AuthService.SignUpUser(payload)
	if err != nil {
		return fmt.Errorf("error creating user: %v", err)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status": "success",
		"user":   models.FilteredUserResponse(user),
	})
}

// @Summary Вход пользователя
// @Description Аутентификация пользователя и выдача токенов
// @Tags auth
// @Accept json
// @Produce json
// @Param input body models.SignInInput true "Данные для входа"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /sign-in [post]
func (ac *AuthController) SignInUser(c *fiber.Ctx) error {
	payload := new(models.SignInInput)

	if err := c.BodyParser(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"stattus": "fail",
			"message": err.Error(),
		})
	}

	errors := validator.ValidateStruct(payload)
	if errors != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"status": "fail",
			"errors": errors,
		})
	}

	tokens, err := ac.AuthService.SignInUser(c, payload)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"status":  "fail",
			"message": fmt.Sprintf("generating JWT Token failed: %v", err),
		})
	}

	config, _ := database.LoadConfig(".")
	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    tokens.AccessToken,
		Path:     "/",
		MaxAge:   config.AccessJwtMaxAge * 60,
		Secure:   false,
		HTTPOnly: true,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "session_id",
		Value:    tokens.SessionID,
		Path:     "/",
		MaxAge:   config.AccessJwtMaxAge * 60,
		Secure:   false,
		HTTPOnly: true,
	})

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status":        "success",
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
	})
}

// @Summary Обновление токена
// @Description Обновляет access и refresh токены
// @Tags auth
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /refresh [post]
func (ac *AuthController) RefreshToken(c *fiber.Ctx) error {
	user := c.Locals("user").(*models.User)
	config, _ := database.LoadConfig(".")

	currentSessionID := c.Cookies("session_id")
	if currentSessionID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"status":  "fail",
			"message": "session_id cookie is missing or empty",
		})
	}

	cachedRefreshToken, err := ac.cache.GetRefreshToken(c.Context(), currentSessionID)
	if err != nil || cachedRefreshToken == nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"status":  "fail",
			"message": fmt.Sprintf("getting refresh token from cache failed: %v", err),
		})
	}

	accessToken, err := utils.GenerateToken(
		config.AccessJwtExpiresIn,
		user.ID.Hex(),
		config.AccessJwtSecret,
	)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"status":  "fail",
			"message": fmt.Sprintf("generating access token failed: %v", err),
		})
	}
	refreshToken, err := utils.GenerateToken(
		config.RefreshJwtExpiresIn,
		user.ID.Hex(),
		config.RefreshJwtSecret,
	)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"status":  "fail",
			"message": fmt.Sprintf("generating refresh token failed: %v", err),
		})
	}

	_ = ac.cache.DeleteRefreshToken(c.Context(), currentSessionID)
	sessionID := uuid.New().String()

	value := cache.CacheValue{
		UserID:       user.ID.Hex(),
		RefreshToken: refreshToken,
	}
	err = ac.cache.SaveRefreshToken(c.Context(), sessionID, value, time.Duration(config.RefreshJwtMaxAge)*time.Hour)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"status":  "fail",
			"message": fmt.Sprintf("saving refresh token to cache failed: %v", err),
		})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Path:     "/",
		MaxAge:   config.AccessJwtMaxAge * 60,
		Secure:   false,
		HTTPOnly: true,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   config.RefreshJwtMaxAge * 60,
		Secure:   false,
		HTTPOnly: true,
	})

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status":        "success",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

// @Summary Выход пользователя
// @Description Удаляет refresh токен и очищает куки
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /logout [get]
func (ac *AuthController) LogOutUser(c *fiber.Ctx) error {
	sessionID := c.Cookies("session_id")
	if sessionID != "" {
		_ = ac.cache.DeleteRefreshToken(c.Context(), sessionID)
	}
	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    "",
		Expires:  time.Now().Add(-time.Hour * 24),
		HTTPOnly: true,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "session_id",
		Value:    "",
		Expires:  time.Now().Add(-time.Hour * 24),
		HTTPOnly: true,
	})
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "success"})
}

// @Summary Получить информацию о себе
// @Description Возвращает данные текущего пользователя
// @Tags users
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /users/me [get]
func (ac *AuthController) GetMeHandler(c *fiber.Ctx) error {
	user := c.Locals("user").(*models.User)
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status": "success",
		"user":   models.FilteredUserResponse(user),
	})
}

// @Summary Получить список пользователей
// @Description Доступно только для ролей admin и moderator
// @Tags users
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /users/ [get]
func (ac *AuthController) GetUsersHandler(c *fiber.Ctx) error {
	pageStr := c.Query("page", "1")
	limitStr := c.Query("limit", "10")

	page, _ := strconv.Atoi(pageStr)
	limit, _ := strconv.Atoi(limitStr)
	skip := (page - 1) * limit

	findOptions := options.Find()
	findOptions.SetLimit(int64(limit))
	findOptions.SetSkip(int64(skip))

	cursor, err := database.UserCollection.Find(context.TODO(), bson.M{}, findOptions)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"status":  "error",
			"message": "Failed to fetch users",
		})
	}
	defer cursor.Close(context.TODO())

	var users []models.User
	if err := cursor.All(context.TODO(), &users); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"status":  "error",
			"message": "Failed to parse users",
		})
	}

	var userResponses []models.UserResponse
	for _, user := range users {
		userResponses = append(userResponses, models.FilteredUserResponse(&user))
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status":  "success",
		"results": len(userResponses),
		"users":   userResponses,
	})
}
