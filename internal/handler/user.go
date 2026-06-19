package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/nacl-org/nacl-cloud-go/internal/service"
	"github.com/nacl-org/nacl-cloud-go/internal/repository"
)

// UserHandler intercepts HTTP request channels and marshals JSON responses.
type UserHandler struct {
	svc *service.UserService
}

// NewUserHandler is the factory constructor used by Google Wire.
func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// CreateUser parses and passes a new user entity to our business services.
func (h *UserHandler) CreateUser(c *fiber.Ctx) error {
	var user repository.User
	if err := c.BodyParser(&user); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"status": "error",
			"message": err.Error(),
		})
	}

	if err := h.svc.CreateUser(c.UserContext(), &user); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"status": "error",
			"message": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(user)
}

// GetUserByID loads and returns a user matching the target route parameter.
func (h *UserHandler) GetUserByID(c *fiber.Ctx) error {
	id := c.Params("id")
	user, err := h.svc.GetUserByID(c.UserContext(), id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"status": "error",
			"message": err.Error(),
		})
	}
	return c.JSON(user)
}
