package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
)

var (
	outputFormat  = flag.String("format", "human", "Output format: human, json")
	verbose       = flag.Bool("verbose", false, "Enable verbose output")
	showHelp      = flag.Bool("help", false, "Show help message")
	searchVersion = flag.String("search-version", "",
		"Search for images with specific version instead of inspecting a file")
	listAll = flag.Bool("list-all", false, "List all firmware images found in configured directories")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] [<image-file>]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Sonic Image Inspector - Extract version information from SONiC image files\n\n")
		fmt.Fprintf(os.Stderr, "Modes:\n")
		fmt.Fprintf(os.Stderr, "  1. Inspect single file: %s <image-file>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  2. Search by version:   %s -search-version=<version>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  3. List all images:     %s -list-all\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Arguments:\n")
		fmt.Fprintf(os.Stderr, "  <image-file>    Path to SONiC image file (.bin or .swi)\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s sonic-image.bin\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -format=json sonic-image.swi\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -search-version=202311.1-test123\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -list-all -format=json\n", os.Args[0])
	}

	flag.Parse()

	if *showHelp {
		flag.Usage()
		os.Exit(0)
	}

	// Initialize config for directory operations
	initializeConfig()

	// Determine operating mode
	if *listAll {
		handleListAll()
	} else if *searchVersion != "" {
		handleSearchVersion(*searchVersion)
	} else {
		handleInspectFile()
	}
}

func initializeConfig() {
	// Initialize minimal config for directory operations
	if config.Global == nil {
		config.Global = &config.Config{
			RootFS:          "/",
			Addr:            ":50051",
			ShutdownTimeout: 10 * time.Second,
			TLSEnabled:      false,
		}
	}
}

func handleListAll() {
	results, err := firmware.FindAllImages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to find images: %v\n", err)
		os.Exit(1)
	}

	switch *outputFormat {
	case "json":
		outputSearchResultsJSON(results)
	case "human":
		outputSearchResultsHuman(results, "All firmware images")
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown output format '%s'. Use 'human' or 'json'\n", *outputFormat)
		os.Exit(1)
	}
}

func handleSearchVersion(version string) {
	results, err := firmware.FindImagesByVersion(version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to search for version %s: %v\n", version, err)
		os.Exit(1)
	}

	switch *outputFormat {
	case "json":
		outputSearchResultsJSON(results)
	case "human":
		outputSearchResultsHuman(results, fmt.Sprintf("Images matching version: %s", version))
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown output format '%s'. Use 'human' or 'json'\n", *outputFormat)
		os.Exit(1)
	}
}

func handleInspectFile() {
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "Error: Please provide exactly one image file path, or use -search-version or -list-all\n\n")
		flag.Usage()
		os.Exit(1)
	}

	imagePath := flag.Arg(0)

	// Extract version information
	versionInfo, err := firmware.GetBinaryImageVersion(imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to extract version from %s: %v\n", imagePath, err)
		os.Exit(1)
	}

	// Output results in requested format
	switch *outputFormat {
	case "json":
		outputJSON(versionInfo, imagePath)
	case "human":
		outputHuman(versionInfo, imagePath)
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown output format '%s'. Use 'human' or 'json'\n", *outputFormat)
		os.Exit(1)
	}
}

func outputHuman(info *firmware.ImageVersionInfo, imagePath string) {
	fmt.Printf("SONiC Image Inspector Results\n")
	fmt.Printf("=============================\n")
	fmt.Printf("File:         %s\n", imagePath)
	fmt.Printf("Image Type:   %s\n", info.ImageType)
	fmt.Printf("Version:      %s\n", info.Version)
	fmt.Printf("Full Version: %s\n", info.FullVersion)

	// Additional information based on image type
	switch info.ImageType {
	case "onie":
		fmt.Printf("\nImage Details:\n")
		fmt.Printf("- Format: ONIE Binary Installer (.bin)\n")
		fmt.Printf("- Structure: Shell script + binary payload\n")
		fmt.Printf("- Compatible with: GRUB and U-Boot bootloaders\n")
	case "aboot":
		fmt.Printf("\nImage Details:\n")
		fmt.Printf("- Format: Aboot Switch Image (.swi)\n")
		fmt.Printf("- Structure: ZIP archive with .imagehash file\n")
		fmt.Printf("- Compatible with: Arista Aboot bootloader\n")
	}

	fmt.Printf("\nCompatibility Check:\n")
	if info.FullVersion != "" {
		fmt.Printf("✓ Valid SONiC image with extractable version\n")
		fmt.Printf("✓ Can be used with sonic_installer commands\n")
	} else {
		fmt.Printf("✗ Unable to extract version information\n")
	}
}

func outputJSON(info *firmware.ImageVersionInfo, imagePath string) {
	result := struct {
		*firmware.ImageVersionInfo
		FilePath string `json:"filePath"`
		Status   string `json:"status"`
	}{
		ImageVersionInfo: info,
		FilePath:         imagePath,
		Status:           "success",
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to encode JSON output: %v\n", err)
		os.Exit(1)
	}
}

func outputSearchResultsJSON(results []*firmware.ImageSearchResult) {
	response := struct {
		Count   int                           `json:"count"`
		Results []*firmware.ImageSearchResult `json:"results"`
		Status  string                        `json:"status"`
	}{
		Count:   len(results),
		Results: results,
		Status:  "success",
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to encode JSON output: %v\n", err)
		os.Exit(1)
	}
}

func outputSearchResultsHuman(results []*firmware.ImageSearchResult, title string) {
	fmt.Printf("SONiC Image Inspector Results\n")
	fmt.Printf("=============================\n")
	fmt.Printf("Search: %s\n", title)
	fmt.Printf("Found: %d images\n\n", len(results))

	if len(results) == 0 {
		fmt.Printf("No images found.\n")
		return
	}

	for i, result := range results {
		fmt.Printf("Image %d:\n", i+1)
		fmt.Printf("  File:         %s\n", result.FilePath)
		fmt.Printf("  Image Type:   %s\n", result.VersionInfo.ImageType)
		fmt.Printf("  Version:      %s\n", result.VersionInfo.Version)
		fmt.Printf("  Full Version: %s\n", result.VersionInfo.FullVersion)
		fmt.Printf("  File Size:    %d bytes (%.2f MB)\n", result.FileSize, float64(result.FileSize)/(1024*1024))

		// Additional information based on image type
		switch result.VersionInfo.ImageType {
		case "onie":
			fmt.Printf("  Format:       ONIE Binary Installer (.bin)\n")
		case "aboot":
			fmt.Printf("  Format:       Aboot Switch Image (.swi)\n")
		}

		if i < len(results)-1 {
			fmt.Printf("\n")
		}
	}

	fmt.Printf("\nSummary:\n")
	fmt.Printf("- Total images: %d\n", len(results))

	// Count by type
	onieCount := 0
	abootCount := 0
	for _, result := range results {
		switch result.VersionInfo.ImageType {
		case "onie":
			onieCount++
		case "aboot":
			abootCount++
		}
	}
	fmt.Printf("- ONIE images: %d\n", onieCount)
	fmt.Printf("- Aboot images: %d\n", abootCount)
}
