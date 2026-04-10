package handler

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func BindAndValidate(c *gin.Context, obj interface{}) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		errors := FormatValidationErrors(err)
		RespondValidationError(c, errors)
		return false
	}
	return true
}

func FormatValidationErrors(err error) []ValidationError {
	var errors []ValidationError
	if ve, ok := err.(validator.ValidationErrors); ok {
		for _, fe := range ve {
			errors = append(errors, ValidationError{
				Field:   toSnakeCase(fe.Field()),
				Message: formatFieldError(fe),
			})
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "body",
			Message: err.Error(),
		})
	}
	return errors
}

func formatFieldError(fe validator.FieldError) string {
	field := toSnakeCase(fe.Field())
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "email":
		return fmt.Sprintf("%s must be a valid email address", field)
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", field, fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, fe.Param())
	default:
		return fmt.Sprintf("%s failed validation: %s", field, fe.Tag())
	}
}

func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}
