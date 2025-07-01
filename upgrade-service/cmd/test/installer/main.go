package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
)

func main() {
	// Handle help flag specially
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	if len(os.Args) < 2 {
		fmt.Printf("test-installer - Sonic Installer Test Utility\n\n")
		fmt.Printf("This tool tests the sonic-installer CLI wrapper functionality.\n")
		fmt.Printf("It provides a safe way to test sonic-installer commands.\n\n")
		showUsage()
		os.Exit(1)
	}

	si := installer.NewSonicInstaller()
	command := os.Args[1]

	switch command {
	case "list":
		fmt.Println("=== Testing sonic-installer list command ===")
		result, err := si.List()
		if err != nil {
			fmt.Printf("Error listing images: %v\n", err)
			fmt.Println("\nThis is expected if sonic-installer is not available.")
			os.Exit(1)
		}

		fmt.Printf("Successfully parsed %d images from sonic-installer output:\n", len(result.Images))
		for _, img := range result.Images {
			status := ""
			if img.Current {
				status += " (Current)"
			}
			if img.Next {
				status += " (Next)"
			}
			fmt.Printf("  %s%s\n", img.Name, status)
		}

		fmt.Printf("\nCurrent: %s\n", result.Current)
		fmt.Printf("Next: %s\n", result.Next)

		// Also show JSON output
		fmt.Println("\n=== JSON Output ===")
		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Printf("%s\n", jsonData)

	case "set-default":
		if len(os.Args) < 3 {
			fmt.Println("Error: set-default requires an image name")
			fmt.Println("Usage: test-installer set-default <image-name>")
			os.Exit(1)
		}

		imageName := os.Args[2]
		fmt.Printf("=== Testing sonic-installer set-default command ===\n")
		fmt.Printf("Setting default image to: %s\n", imageName)

		result, err := si.SetDefault(imageName)
		if err != nil {
			fmt.Printf("Error setting default image: %v\n", err)
			fmt.Println("\nThis is expected if sonic-installer is not available or")
			fmt.Println("if the specified image does not exist.")
			os.Exit(1)
		}

		fmt.Printf("Successfully set default image to: %s\n", result.DefaultImage)
		fmt.Printf("Message: %s\n", result.Message)

	case "cleanup":
		fmt.Println("=== Testing sonic-installer cleanup command ===")
		fmt.Println("WARNING: This will remove unused SONiC images from your system!")
		fmt.Println("Make sure you have backups and understand the implications.")

		result, err := si.Cleanup()
		if err != nil {
			fmt.Printf("Error cleaning up images: %v\n", err)
			fmt.Println("\nThis is expected if sonic-installer is not available.")
			os.Exit(1)
		}

		if len(result.RemovedImages) > 0 {
			fmt.Printf("Removed %d images:\n", len(result.RemovedImages))
			for _, img := range result.RemovedImages {
				fmt.Printf("  - %s\n", img)
			}
		} else {
			fmt.Println("No images were removed")
		}

		fmt.Printf("\nOutput: %s\n", result.Message)

	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Printf("Use 'test-installer help' to see available commands.\n")
		os.Exit(1)
	}
}

func showUsage() {
	fmt.Printf("Usage: test-installer <command> [args...]\n\n")
	fmt.Printf("Commands:\n")
	fmt.Printf("  list                    - List installed images (read-only)\n")
	fmt.Printf("  set-default <image>     - Set default image (MODIFIES SYSTEM)\n")
	fmt.Printf("  cleanup                 - Remove unused images (MODIFIES SYSTEM)\n")
	fmt.Printf("  help                    - Show detailed help\n\n")
}

func showHelp() {
	fmt.Printf("test-installer - Sonic Installer Test Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility validates the sonic-installer CLI wrapper functionality.\n")
	fmt.Printf("  It provides a way to test the installer package integration with the\n")
	fmt.Printf("  sonic-installer command-line tool. The utility demonstrates:\n")
	fmt.Printf("  - Calling sonic-installer commands through the wrapper\n")
	fmt.Printf("  - Parsing command output and error handling\n")
	fmt.Printf("  - Converting results to structured data formats\n\n")
	showUsage()
	fmt.Printf("SAFETY NOTES:\n")
	fmt.Printf("  - 'list' command is read-only and safe to use\n")
	fmt.Printf("  - 'set-default' and 'cleanup' commands MODIFY your system\n")
	fmt.Printf("  - Always backup your system before using modifying commands\n")
	fmt.Printf("  - Test on non-production systems first\n\n")
	fmt.Printf("PURPOSE:\n")
	fmt.Printf("  This tool is for testing and validation purposes.\n")
	fmt.Printf("  Use this to verify that the sonic-installer wrapper works\n")
	fmt.Printf("  correctly with your system's sonic-installer installation.\n\n")
	fmt.Printf("EXAMPLES:\n")
	fmt.Printf("  test-installer list                           # List images (safe)\n")
	fmt.Printf("  test-installer set-default SONiC-OS-202311.1  # Change default (dangerous)\n")
	fmt.Printf("  test-installer cleanup                        # Remove old images (dangerous)\n")
	fmt.Printf("  test-installer help                           # Show this help\n\n")
}
