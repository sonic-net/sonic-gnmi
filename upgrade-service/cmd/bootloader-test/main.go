package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/bootloader"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
)

func main() {
	// Initialize config to use root filesystem
	config.Global = &config.Config{
		RootFS: "/",
	}

	fmt.Println("=== Bootloader Detection Test ===")
	
	// Try to detect bootloader
	bl, err := bootloader.GetBootloader()
	if err != nil {
		fmt.Printf("Error detecting bootloader: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Detected bootloader: %T\n", bl)

	// Test installed images
	fmt.Println("\n=== Installed Images ===")
	images, err := bl.GetInstalledImages()
	if err != nil {
		fmt.Printf("Error getting installed images: %v\n", err)
	} else {
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
	fmt.Println("\n=== Image List Summary ===")
	info, err := bootloader.ListInstalledImages()
	if err != nil {
		fmt.Printf("Error listing images: %v\n", err)
	} else {
		jsonData, _ := json.MarshalIndent(info, "", "  ")
		fmt.Printf("%s\n", jsonData)
	}
}