package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/redis"
)

func main() {
	// Handle help flag specially
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	fmt.Printf("test-redis - Redis Client Test Utility\n\n")
	fmt.Printf("This tool tests the Redis client wrapper functionality.\n")
	fmt.Printf("It connects to the local Redis instance on database 4 (CONFIG_DB).\n\n")

	// Create Redis client with default config (localhost:6379, DB 4)
	client, err := redis.NewClient(nil)
	if err != nil {
		fmt.Printf("ERROR: Failed to connect to Redis: %v\n", err)
		fmt.Printf("\nMake sure Redis is running on localhost:6379\n")
		os.Exit(1)
	}
	defer client.Close()

	fmt.Printf("✓ Successfully connected to Redis (localhost:6379, database 4)\n\n")

	ctx := context.Background()

	// Test 1: Get device type
	fmt.Printf("=== Test 1: Get Device Type ===\n")
	deviceType, err := client.HGet(ctx, "DEVICE_METADATA|localhost", "type")
	if err != nil {
		fmt.Printf("Failed to get device type: %v\n", err)
	} else {
		fmt.Printf("Device type: %s\n", deviceType)
	}
	fmt.Println()

	// Test 2: Get platform
	fmt.Printf("=== Test 2: Get Platform ===\n")
	platform, err := client.HGet(ctx, "DEVICE_METADATA|localhost", "platform")
	if err != nil {
		fmt.Printf("Failed to get platform: %v\n", err)
	} else {
		fmt.Printf("Platform: %s\n", platform)
	}
	fmt.Println()

	// Test 3: Get hwsku
	fmt.Printf("=== Test 3: Get Hardware SKU ===\n")
	hwsku, err := client.HGet(ctx, "DEVICE_METADATA|localhost", "hwsku")
	if err != nil {
		fmt.Printf("Failed to get hwsku: %v\n", err)
	} else {
		fmt.Printf("Hardware SKU: %s\n", hwsku)
	}
	fmt.Println()

	// Test 4: Get deployment_id
	fmt.Printf("=== Test 4: Get Deployment ID ===\n")
	deploymentID, err := client.HGet(ctx, "DEVICE_METADATA|localhost", "deployment_id")
	if err != nil {
		fmt.Printf("Failed to get deployment_id: %v\n", err)
	} else {
		fmt.Printf("Deployment ID: %s\n", deploymentID)
	}
	fmt.Println()

	// Test 5: Test non-existent field
	fmt.Printf("=== Test 5: Get Non-existent Field ===\n")
	_, err = client.HGet(ctx, "DEVICE_METADATA|localhost", "non_existent_field")
	if err != nil {
		fmt.Printf("Expected error for non-existent field: %v\n", err)
	} else {
		fmt.Printf("WARNING: Non-existent field returned a value (unexpected)\n")
	}
	fmt.Println()

	// Test 6: Test connection health
	fmt.Printf("=== Test 6: Connection Health Check ===\n")
	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx); err != nil {
		fmt.Printf("Ping failed: %v\n", err)
	} else {
		fmt.Printf("✓ Redis connection is healthy\n")
	}
	fmt.Println()

	// Summary
	fmt.Printf("=== Summary ===\n")
	summary := map[string]interface{}{
		"connection": "localhost:6379",
		"database":   4,
		"status":     "connected",
	}
	if deviceType != "" {
		summary["device_type"] = deviceType
	}
	if platform != "" {
		summary["platform"] = platform
	}
	if hwsku != "" {
		summary["hwsku"] = hwsku
	}

	jsonData, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Printf("%s\n", jsonData)
}

func showHelp() {
	fmt.Printf("test-redis - Redis Client Test Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility validates the Redis client wrapper functionality.\n")
	fmt.Printf("  It connects to the local Redis instance on database 4 (CONFIG_DB)\n")
	fmt.Printf("  and performs various HGET operations on DEVICE_METADATA.\n\n")
	fmt.Printf("USAGE:\n")
	fmt.Printf("  test-redis              Run all Redis tests\n")
	fmt.Printf("  test-redis help         Show this help message\n\n")
	fmt.Printf("TESTS PERFORMED:\n")
	fmt.Printf("  1. Connect to Redis (localhost:6379, database 4)\n")
	fmt.Printf("  2. Get device type from DEVICE_METADATA|localhost\n")
	fmt.Printf("  3. Get platform information\n")
	fmt.Printf("  4. Get hardware SKU\n")
	fmt.Printf("  5. Get deployment ID\n")
	fmt.Printf("  6. Test error handling with non-existent field\n")
	fmt.Printf("  7. Test connection health with ping\n\n")
	fmt.Printf("REQUIREMENTS:\n")
	fmt.Printf("  - Redis must be running on localhost:6379\n")
	fmt.Printf("  - The tool connects to database 4 (CONFIG_DB)\n")
	fmt.Printf("  - DEVICE_METADATA|localhost hash should exist\n\n")
	fmt.Printf("PURPOSE:\n")
	fmt.Printf("  Use this tool to verify that the Redis client works correctly\n")
	fmt.Printf("  on a real SONiC device before integrating it into other services.\n\n")
}
