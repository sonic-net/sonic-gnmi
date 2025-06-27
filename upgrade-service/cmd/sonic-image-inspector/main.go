package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
)

var (
	outputFormat = flag.String("format", "human", "Output format: human, json")
	verbose      = flag.Bool("verbose", false, "Enable verbose output")
	showHelp     = flag.Bool("help", false, "Show help message")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] <image-file>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Sonic Image Inspector - Extract version information from SONiC image files\n\n")
		fmt.Fprintf(os.Stderr, "Arguments:\n")
		fmt.Fprintf(os.Stderr, "  <image-file>    Path to SONiC image file (.bin or .swi)\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s sonic-image.bin\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -format=json sonic-image.swi\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -verbose sonic-image.bin\n", os.Args[0])
	}

	flag.Parse()

	if *showHelp {
		flag.Usage()
		os.Exit(0)
	}

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "Error: Please provide exactly one image file path\n\n")
		flag.Usage()
		os.Exit(1)
	}

	imagePath := flag.Arg(0)

	// Note: verbose flag is available for future use with glog if needed

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
