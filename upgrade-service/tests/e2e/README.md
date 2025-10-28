# End-to-End Tests

This directory contains end-to-end tests for the API service. These tests verify the full request-response flow through the gRPC API using in-memory servers and clients with mocked dependencies.

## Test Structure

The end-to-end tests are structured to:

1. Set up an in-memory gRPC server with mocked dependencies
2. Create a client that connects to this server
3. Make actual gRPC calls and verify the responses

This approach allows us to test the entire API surface without requiring external dependencies or network connections.

## Running the Tests

To run the end-to-end tests:

```bash
cd /path/to/api-service
go test ./tests/e2e/...
```

## Adding New Tests

When adding new end-to-end tests:

1. Create a new test file for each service being tested
2. Use the provided helper functions for setting up in-memory servers and clients
3. Mock all external dependencies to ensure tests are repeatable and reliable
4. Provide comprehensive test cases that cover various scenarios including edge cases and error conditions
