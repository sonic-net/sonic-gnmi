package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/download"
)

func printErrorDetails(err error, showAttempts bool) {
	// Check if it's a structured download error
	downloadErr, ok := err.(*download.DownloadError)
	if !ok {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Error Category: %s\n", downloadErr.Category)
	if downloadErr.Code > 0 {
		fmt.Printf("Error Code: %d\n", downloadErr.Code)
	}
	fmt.Printf("Error Message: %s\n", downloadErr.Message)
	fmt.Printf("URL: %s\n", downloadErr.URL)

	if showAttempts && len(downloadErr.Attempts) > 0 {
		printAttemptDetails(downloadErr.Attempts)
	} else if len(downloadErr.Attempts) > 0 {
		fmt.Printf("Total Attempts: %d (use -show-attempts for details)\n", len(downloadErr.Attempts))
	}
}

func printAttemptDetails(attempts []download.Attempt) {
	fmt.Printf("\nAttempt Details:\n")
	for i, attempt := range attempts {
		fmt.Printf("  Attempt %d:\n", i+1)
		fmt.Printf("    Method: %s\n", attempt.Method)
		if attempt.Interface != "" {
			fmt.Printf("    Interface: %s\n", attempt.Interface)
		}
		fmt.Printf("    Duration: %v\n", attempt.Duration)
		if attempt.HTTPStatus > 0 {
			fmt.Printf("    HTTP Status: %d\n", attempt.HTTPStatus)
		}
		if attempt.Error != "" {
			fmt.Printf("    Error: %s\n", attempt.Error)
		}
		fmt.Println()
	}
}

func main() {
	var (
		url            = flag.String("url", "", "URL to download from (required)")
		output         = flag.String("output", "", "Output file path (optional, auto-detected from URL if not provided)")
		timeout        = flag.Duration("timeout", 300*time.Second, "Overall timeout for download")
		connectTimeout = flag.Duration("connect-timeout", 30*time.Second, "Connection timeout")
		interface_     = flag.String("interface", "eth0", "Preferred network interface")
		userAgent      = flag.String("user-agent", "sonic-download-test/1.0", "User-Agent header")
		showAttempts   = flag.Bool("show-attempts", false, "Show detailed attempt information")
		verbose        = flag.Bool("verbose", false, "Enable verbose logging")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nDownload firmware images with sophisticated retry strategies.\n")
		fmt.Fprintf(os.Stderr, "This tool mimics curl behavior with interface binding and fallback mechanisms.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -url http://example.com/firmware.bin\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -url http://example.com/firmware.bin -output /tmp/firmware.bin\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -url http://example.com/firmware.bin -interface eth1 -verbose\n", os.Args[0])
	}

	flag.Parse()

	// Validate required arguments
	if *url == "" {
		fmt.Fprintf(os.Stderr, "Error: -url is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Setup logging
	if *verbose {
		flag.Set("logtostderr", "true")
		flag.Set("v", "2")
	}

	// Create download configuration
	config := &download.DownloadConfig{
		ConnectTimeout: *connectTimeout,
		Interface:      *interface_,
		UserAgent:      *userAgent,
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Printf("Starting download from: %s\n", *url)
	if *output != "" {
		fmt.Printf("Output file: %s\n", *output)
	} else {
		fmt.Printf("Output file: auto-detected from URL\n")
	}
	fmt.Printf("Interface: %s\n", *interface_)
	fmt.Printf("Connect timeout: %v\n", *connectTimeout)
	fmt.Printf("Total timeout: %v\n", *timeout)
	fmt.Printf("User-Agent: %s\n", *userAgent)
	fmt.Println()

	// Perform download
	startTime := time.Now()
	result, err := download.DownloadFirmwareWithConfig(ctx, *url, *output, config)
	duration := time.Since(startTime)

	if err != nil {
		fmt.Printf("❌ Download failed after %v\n\n", duration)
		printErrorDetails(err, *showAttempts)
		os.Exit(1)
	}

	// Success!
	fmt.Printf("✅ Download completed successfully in %v\n\n", duration)
	fmt.Printf("File Path: %s\n", result.FilePath)
	fmt.Printf("File Size: %d bytes (%.2f MB)\n", result.FileSize, float64(result.FileSize)/1024/1024)
	fmt.Printf("Download Speed: %.2f MB/s\n", float64(result.FileSize)/1024/1024/result.Duration.Seconds())
	fmt.Printf("Attempts Made: %d\n", result.AttemptCount)
	fmt.Printf("Final Method: %s\n", result.FinalMethod)
	fmt.Printf("URL: %s\n", result.URL)

	// Show file info
	if stat, err := os.Stat(result.FilePath); err == nil {
		fmt.Printf("File Mode: %s\n", stat.Mode())
		fmt.Printf("File ModTime: %s\n", stat.ModTime().Format(time.RFC3339))
	}

	glog.Flush()
}
