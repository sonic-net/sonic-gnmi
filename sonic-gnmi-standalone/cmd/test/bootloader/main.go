package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/bootloader"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

func main() {
	// Check for help flag
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	fmt.Println("=== Bootloader Detection Test Utility ===")
	fmt.Println("This tool tests the bootloader detection and image management functionality.")
	fmt.Println("Use this to validate that the bootloader package can correctly detect and")
	fmt.Println("interact with GRUB, U-Boot, or Aboot bootloaders on the system.")
	fmt.Println()

	// Initialize config to use root filesystem
	config.Global = &config.Config{
		RootFS: "/",
	}

	fmt.Println("=== Bootloader Detection Test ===")

	// Try to detect bootloader
	bl, err := bootloader.GetBootloader()
	if err != nil {
		fmt.Printf("Error detecting bootloader: %v\n", err)
		fmt.Println("\nThis is expected if running on a system without a supported bootloader")
		fmt.Println("or if the bootloader files are not accessible.")
		os.Exit(1)
	}

	fmt.Printf("Detected bootloader: %T\n", bl)

	// Test installed images
	fmt.Println("\n=== Installed Images ===")
	images, err := bl.GetInstalledImages()
	if err != nil {
		fmt.Printf("Error getting installed images: %v\n", err)
	} else {
		fmt.Printf("Found %d installed images:\n", len(images))
		for i, image := range images {
			fmt.Printf("%d. %s\n", i+1, image)
		}
	}

	// Test current image
	fmt.Println("\n=== Current Image ===")
	current, err := bl.GetCurrentImage()
	if err != nil {
		fmt.Printf("Error getting current image: %v\n", err)
	} else {
		fmt.Printf("Current: %s\n", current)
	}

	// Test next image
	fmt.Println("\n=== Next Boot Image ===")
	next, err := bl.GetNextImage()
	if err != nil {
		fmt.Printf("Error getting next image: %v\n", err)
	} else {
		fmt.Printf("Next: %s\n", next)
	}

	// Test the convenience function
	fmt.Println("\n=== Image List Summary (JSON Format) ===")
	info, err := bootloader.ListInstalledImages()
	if err != nil {
		fmt.Printf("Error listing images: %v\n", err)
	} else {
		jsonData, _ := json.MarshalIndent(info, "", "  ")
		fmt.Printf("%s\n", jsonData)
	}

	fmt.Println("\n=== Test Complete ===")
	fmt.Println("If all sections above completed without errors, the bootloader")
	fmt.Println("package is functioning correctly on this system.")
}

func showHelp() {
	fmt.Printf("test-bootloader - Bootloader Detection Test Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility validates the bootloader detection and image management\n")
	fmt.Printf("  functionality of the upgrade service. It tests the ability to:\n")
	fmt.Printf("  - Detect the system bootloader (GRUB, U-Boot, or Aboot)\n")
	fmt.Printf("  - List installed SONiC images\n")
	fmt.Printf("  - Identify current and next boot images\n")
	fmt.Printf("  - Parse bootloader configuration files\n\n")
	fmt.Printf("USAGE:\n")
	fmt.Printf("  test-bootloader [OPTIONS]\n\n")
	fmt.Printf("OPTIONS:\n")
	fmt.Printf("  -h, --help, help    Show this help message\n\n")
	fmt.Printf("PURPOSE:\n")
	fmt.Printf("  This tool is for testing and validation purposes only.\n")
	fmt.Printf("  It does not modify system configuration or bootloader settings.\n")
	fmt.Printf("  Use this to verify that the upgrade service can properly interact\n")
	fmt.Printf("  with your system's bootloader before deploying the service.\n\n")
	fmt.Printf("EXAMPLES:\n")
	fmt.Printf("  test-bootloader         # Run all bootloader tests\n")
	fmt.Printf("  test-bootloader --help  # Show this help\n\n")
}
