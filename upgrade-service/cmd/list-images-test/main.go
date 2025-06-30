package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
)

func main() {
	fmt.Println("=== ListImages Test ===")

	// Create sonic-installer wrapper
	si := installer.NewSonicInstaller()

	// Call List method (same as ListImages RPC would do)
	result, err := si.List()
	if err != nil {
		fmt.Printf("Error listing images: %v\n", err)
		os.Exit(1)
	}

	// Display results in the same format as ListImages RPC would return
	fmt.Printf("Images (%d total):\n", len(result.Images))
	for i, img := range result.Images {
		fmt.Printf("  %d. %s\n", i+1, img.Name)
	}

	fmt.Printf("\nCurrent Image: %s\n", result.Current)
	fmt.Printf("Next Image: %s\n", result.Next)

	// Show JSON format (similar to RPC response)
	fmt.Println("\n=== ListImages RPC Response Format ===")

	// Extract just the image names (as the RPC would do)
	imageNames := make([]string, 0, len(result.Images))
	for _, img := range result.Images {
		imageNames = append(imageNames, img.Name)
	}

	// Simulate the RPC response structure
	rpcResponse := map[string]interface{}{
		"images":        imageNames,
		"current_image": result.Current,
		"next_image":    result.Next,
		"warnings":      []string{},
	}

	jsonData, _ := json.MarshalIndent(rpcResponse, "", "  ")
	fmt.Printf("%s\n", jsonData)
}
