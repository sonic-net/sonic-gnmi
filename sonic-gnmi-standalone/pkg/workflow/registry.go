package workflow

import (
	"fmt"
	"sort"
	"sync"
)

// DefaultStepRegistry maps step type names to factory functions.
type DefaultStepRegistry struct {
	mu        sync.RWMutex
	factories map[string]StepFactory
}

// NewRegistry creates a new empty step registry.
// Step types must be explicitly registered using the Register method.
func NewRegistry() *DefaultStepRegistry {
	return &DefaultStepRegistry{
		factories: make(map[string]StepFactory),
	}
}

// Register associates a step type name with its factory function.
// If a step type is already registered, it will be overwritten with the new factory.
//
// This method is thread-safe and can be called during application initialization
// to register all available step types.
func (r *DefaultStepRegistry) Register(stepType string, factory StepFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[stepType] = factory
}

// CreateStep creates a step instance of the specified type using the registered factory.
// Returns an error if the step type is not registered or if the factory fails.
//
// The factory is responsible for parsing the params map and creating a properly
// configured step instance with validation.
func (r *DefaultStepRegistry) CreateStep(stepType, name string, params map[string]interface{}) (Step, error) {
	r.mu.RLock()
	factory, exists := r.factories[stepType]
	r.mu.RUnlock()

	if !exists {
		supportedTypes := r.GetSupportedTypes()
		if len(supportedTypes) == 0 {
			return nil, fmt.Errorf("unknown step type '%s' (no step types are registered)", stepType)
		}
		return nil, fmt.Errorf("unknown step type '%s' (supported types: %v)", stepType, supportedTypes)
	}

	step, err := factory(name, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create step of type '%s': %w", stepType, err)
	}

	return step, nil
}

// GetSupportedTypes returns a sorted list of all registered step types.
// This is useful for error messages and help text.
func (r *DefaultStepRegistry) GetSupportedTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.factories))
	for stepType := range r.factories {
		types = append(types, stepType)
	}
	sort.Strings(types)
	return types
}
