package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <command> [args...]\n", os.Args[0])
		fmt.Println("Commands:")
		fmt.Println("  list                    - List installed images")
		fmt.Println("  set-default <image>     - Set default image")
		fmt.Println("  cleanup                 - Remove unused images")
		os.Exit(1)
	}

	si := installer.NewSonicInstaller()
	command := os.Args[1]

	switch command {
	case "list":
		result, err := si.List()
		if err != nil {
			fmt.Printf("Error listing images: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("=== SONiC Images ===")
		for _, img := range result.Images {
			status := ""
			if img.Current {
				status += " (Current)"
			}
			if img.Next {
				status += " (Next)"
			}
			fmt.Printf("%s%s\n", img.Name, status)
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
			os.Exit(1)
		}

		imageName := os.Args[2]
		result, err := si.SetDefault(imageName)
		if err != nil {
			fmt.Printf("Error setting default image: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Successfully set default image to: %s\n", result.DefaultImage)
		fmt.Printf("Message: %s\n", result.Message)

	case "cleanup":
		result, err := si.Cleanup()
		if err != nil {
			fmt.Printf("Error cleaning up images: %v\n", err)
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
		os.Exit(1)
	}
}
