package handlers

import "fmt"

// UnknownModelError reports that no configured provider can serve a model.
type UnknownModelError struct {
	Model string
}

func (e *UnknownModelError) Error() string {
	if e == nil || e.Model == "" {
		return "unknown provider for model"
	}
	return fmt.Sprintf("unknown provider for model %s", e.Model)
}
