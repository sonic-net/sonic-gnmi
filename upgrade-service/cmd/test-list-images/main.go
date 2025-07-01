package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
)

func main() {
	// Check for help flag
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	fmt.Println("=== ListImages RPC Test Utility ===")
	fmt.Println("This tool tests the ListImages RPC functionality by calling the")
	fmt.Println("sonic-installer wrapper and displaying results in both human-readable")
	fmt.Println("and RPC response formats.")
	fmt.Println()

	// Create sonic-installer wrapper
	si := installer.NewSonicInstaller()

	// Call List method (same as ListImages RPC would do)
	result, err := si.List()
	if err != nil {
		fmt.Printf("Error listing images: %v\n", err)
		fmt.Println("\nThis is expected if sonic-installer is not available or")
		fmt.Println("if the system does not have SONiC images installed.")
		os.Exit(1)
	}

	// Display results in the same format as ListImages RPC would return
	fmt.Printf("=== Installed SONiC Images ===\n")
	fmt.Printf("Found %d images:\n", len(result.Images))
	for i, img := range result.Images {
		status := ""
		if img.Current {
			status += " (Current)"
		}
		if img.Next {
			status += " (Next Boot)"
		}
		fmt.Printf("  %d. %s%s\n", i+1, img.Name, status)
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

	fmt.Println("\n=== Test Complete ===")
	fmt.Println("The output above matches what the ListImages RPC would return.")
	fmt.Println("If no errors occurred, the ListImages functionality is working correctly.")
}

func showHelp() {
	fmt.Printf("test-list-images - ListImages RPC Test Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility validates the ListImages RPC functionality by testing\n")
	fmt.Printf("  the sonic-installer wrapper integration. It demonstrates:\n")
	fmt.Printf("  - Calling sonic-installer list command\n")
	fmt.Printf("  - Parsing the output to extract image information\n")
	fmt.Printf("  - Formatting results to match RPC response structure\n")
	fmt.Printf("  - Identifying current and next boot images\n\n")
	fmt.Printf("USAGE:\n")
	fmt.Printf("  test-list-images [OPTIONS]\n\n")
	fmt.Printf("OPTIONS:\n")
	fmt.Printf("  -h, --help, help    Show this help message\n\n")
	fmt.Printf("PURPOSE:\n")
	fmt.Printf("  This tool is for testing and validation purposes only.\n")
	fmt.Printf("  It calls sonic-installer in read-only mode and does not modify\n")
	fmt.Printf("  any system configuration. Use this to verify that the ListImages\n")
	fmt.Printf("  RPC would work correctly on your system.\n\n")
	fmt.Printf("EXAMPLES:\n")
	fmt.Printf("  test-list-images         # Test ListImages functionality\n")
	fmt.Printf("  test-list-images --help  # Show this help\n\n")
}
