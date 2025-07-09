# Internal Package

This directory contains internal packages that are private to this repository.
They should not be imported by external projects.

## Overview

In Go, the `internal` directory has special meaning. Go will prevent packages outside the parent module from importing anything inside this directory. This means:

- Code in `internal` can only be imported by code in this module
- It cannot be imported by any other project even if they depend on this module

## Library Design Guidelines

### 1. Separate Interfaces from Implementations

Always define interfaces separate from their implementations. This practice:

- Makes code more modular and maintainable
- Enables dependency injection for testing
- Allows for multiple implementations behind a stable API

**Example:**

```go
// Define the interface (in platform.go)
type PlatformInfoProvider interface {
    GetPlatformInfo(ctx context.Context) (*PlatformInfo, error)
    GetPlatformType(ctx context.Context, info *PlatformInfo) int
}

// Implement the interface (same or different file)
type DefaultPlatformInfoProvider struct {
    // Fields needed for implementation
}

func (p *DefaultPlatformInfoProvider) GetPlatformInfo(ctx context.Context) (*PlatformInfo, error) {
    // Check for context cancellation
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        // Continue with normal operation
    }

    // Implementation logic
    return &PlatformInfo{}, nil
}

func (p *DefaultPlatformInfoProvider) GetPlatformType(ctx context.Context, info *PlatformInfo) int {
    // Implementation logic with context awareness
    return 0
}

// Factory function for default implementation
func NewPlatformInfoProvider() PlatformInfoProvider {
    return &DefaultPlatformInfoProvider{}
}
```

### 2. Use Dependency Injection

When a component needs to use a library, inject the interface rather than the concrete implementation:

```go
// Server depending on the interface, not the implementation
type SystemInfoServer struct {
    platformProvider hostinfo.PlatformInfoProvider
}

// Constructor with dependency injection
func NewSystemInfoServer(platformProvider hostinfo.PlatformInfoProvider) *SystemInfoServer {
    return &SystemInfoServer{
        platformProvider: platformProvider,
    }
}
```

### 3. Mock Generation for Testing

For each interface, generate mocks to use in tests:

1. Add the interface to the `MOCK_CONFIGS` variable in the root Makefile:

```makefile
MOCK_CONFIGS := \
    internal/hostinfo/platform.go:PlatformInfoProvider:internal/hostinfo/mocks
```

2. Run `make mocks` to generate mocks
3. Verify mocks are up to date with `make validate-mocks`

### 4. Writing Unit Tests with Mocks

Leverage generated mocks for thorough unit testing:

```go
func TestGetPlatformType(t *testing.T) {
    // Setup the mock controller
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    // Create a mock
    mockPlatform := mocks.NewMockPlatformInfoProvider(ctrl)

    // Create test data
    platformInfo := &hostinfo.PlatformInfo{
        Vendor: "Mellanox",
        Platform: "msn2700",
    }

    // Set expectations with context matchers
    mockPlatform.EXPECT().
        GetPlatformInfo(gomock.Any()).
        Return(platformInfo, nil)

    mockPlatform.EXPECT().
        GetPlatformType(gomock.Any(), platformInfo).
        Return(201) // PLATFORM_TYPE_MELLANOX_SN2700

    // Create the service with the mock
    server := server.NewSystemInfoServerWithProvider(mockPlatform)

    // Test the service (passing context)
    resp, err := server.GetPlatformType(context.Background(), &pb.GetPlatformTypeRequest{})

    // Assertions
    assert.NoError(t, err)
    assert.Equal(t, pb.PlatformType(201), resp.PlatformType)
}
```

## Adding a New Library

Follow these steps when adding a new library to the `internal` directory:

1. **Create the package directory**:
   ```
   mkdir -p internal/mynewlibrary
   ```

2. **Define interfaces**:
   Create a file with the interface definition (e.g., `internal/mynewlibrary/service.go`).

3. **Implement the interface**:
   Create implementation files as needed. Consider separating implementation details into separate files.

4. **Add mock configuration**:
   Update the Makefile's `MOCK_CONFIGS` variable:
   ```makefile
   MOCK_CONFIGS := \
       internal/hostinfo/platform.go:PlatformInfoProvider:internal/hostinfo/mocks \
       internal/mynewlibrary/service.go:MyServiceInterface:internal/mynewlibrary/mocks
   ```

5. **Generate mocks**:
   ```
   make mocks
   ```

6. **Write tests**:
   Create test files using the generated mocks.

7. **Validate your changes**:
   ```
   make ci
   ```

## Best Practices

1. **Small, focused interfaces**: Define interfaces with as few methods as necessary.
2. **Consistent error handling**: Return detailed errors that can be properly handled by callers.
3. **Context usage**: Pass context.Context as the first parameter for methods that perform I/O operations or long-running tasks to enable cancellation, timeouts, and tracing.
4. **Documentation**: Add comments to interfaces and exported functions.
5. **Unit tests**: Aim for high test coverage, especially for core logic.
6. **Use constructor functions**: Provide constructor functions (e.g., `New...`) for implementations.
