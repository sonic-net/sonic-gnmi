package main

import (
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/diskspace"
)

func main() {
	// Handle help flag specially
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		showHelp()
		return
	}

	fmt.Printf("diskspace - Disk Space Utility\n\n")
	fmt.Printf("This tool shows total and free disk space in MB.\n\n")

	// Test root filesystem
	fmt.Printf("=== Root Filesystem (/) ===\n")
	testPath("/")

	// Test /host if it exists
	if _, err := os.Stat("/host"); err == nil {
		fmt.Printf("\n=== Host Filesystem (/host) ===\n")
		testPath("/host")
	}

	// Test /tmp
	fmt.Printf("\n=== Temp Filesystem (/tmp) ===\n")
	testPath("/tmp")
}

func testPath(path string) {
	fmt.Printf("Path: %s\n", path)

	total, err := diskspace.GetDiskTotalSpaceMB(path)
	if err != nil {
		fmt.Printf("ERROR getting total space: %v\n", err)
		return
	}

	free, err := diskspace.GetDiskFreeSpaceMB(path)
	if err != nil {
		fmt.Printf("ERROR getting free space: %v\n", err)
		return
	}

	used := total - free
	usedPercent := float64(used) / float64(total) * 100

	fmt.Printf("Total space: %d MB\n", total)
	fmt.Printf("Free space:  %d MB\n", free)
	fmt.Printf("Used space:  %d MB (%.1f%%)\n", used, usedPercent)

	// Visual bar
	fmt.Printf("Usage bar:   ")
	printUsageBar(usedPercent)
	fmt.Println()
}

func printUsageBar(usedPercent float64) {
	const barWidth = 40
	used := int(usedPercent * barWidth / 100)

	fmt.Print("[")
	for i := 0; i < barWidth; i++ {
		if i < used {
			if usedPercent > 90 {
				fmt.Print("█") // Critical - solid block
			} else if usedPercent > 80 {
				fmt.Print("▓") // Warning - medium block
			} else {
				fmt.Print("▒") // Normal - light block
			}
		} else {
			fmt.Print("░") // Free space - very light block
		}
	}
	fmt.Printf("] %.1f%%", usedPercent)
}

func showHelp() {
	fmt.Printf("diskspace - Disk Space Utility\n\n")
	fmt.Printf("DESCRIPTION:\n")
	fmt.Printf("  This test utility shows disk space information in MB.\n")
	fmt.Printf("  It provides total and free space for various filesystems.\n\n")
	fmt.Printf("USAGE:\n")
	fmt.Printf("  diskspace              Show disk space information\n")
	fmt.Printf("  diskspace help         Show this help message\n\n")
	fmt.Printf("OUTPUT:\n")
	fmt.Printf("  • Total space in MB\n")
	fmt.Printf("  • Free space in MB\n")
	fmt.Printf("  • Used space in MB and percentage\n")
	fmt.Printf("  • Visual usage bar with color coding\n\n")
	fmt.Printf("FILESYSTEMS CHECKED:\n")
	fmt.Printf("  • / (root filesystem)\n")
	fmt.Printf("  • /host (if exists)\n")
	fmt.Printf("  • /tmp (temporary filesystem)\n\n")
	fmt.Printf("PURPOSE:\n")
	fmt.Printf("  Use this tool to quickly check available disk space\n")
	fmt.Printf("  before performing operations that require storage.\n\n")
}
