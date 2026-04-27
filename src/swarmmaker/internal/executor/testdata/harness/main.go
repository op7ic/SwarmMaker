package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	behavior := strings.TrimSpace(os.Getenv("SWARMAKER_TEST_BEHAVIOR"))
	if behavior == "" {
		behavior = "stdout-only"
	}

	stdout := envOr("SWARMAKER_TEST_STDOUT", strings.Repeat("stdout harness output ", 15))
	stderr := envOr("SWARMAKER_TEST_STDERR", "stderr harness output")
	outputFile := os.Getenv("SWARMAKER_OUTPUT_FILE")

	switch behavior {
	case "stdout-only":
		fmt.Print(stdout)
	case "stderr":
		fmt.Fprint(os.Stderr, stderr)
		os.Exit(exitCodeFromEnv(1))
	case "non-zero":
		fmt.Print(stdout)
		os.Exit(exitCodeFromEnv(3))
	case "timeout":
		sleepMS := envInt("SWARMAKER_TEST_SLEEP_MS", 1000)
		time.Sleep(time.Duration(sleepMS) * time.Millisecond)
		fmt.Print(stdout)
	case "partial-write":
		if outputFile != "" {
			if err := os.WriteFile(outputFile, []byte(envOr("SWARMAKER_TEST_PARTIAL_FILE", "short")), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "write partial output: %v", err)
				os.Exit(2)
			}
		}
		fmt.Print(stdout)
	case "write-file":
		if outputFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_OUTPUT_FILE")
			os.Exit(2)
		}
		content := envOr("SWARMAKER_TEST_FILE_CONTENT", strings.Repeat("file harness output ", 15))
		if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v", err)
			os.Exit(2)
		}
		fmt.Print(stdout)
	case "write-extra-file":
		if outputFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_OUTPUT_FILE")
			os.Exit(2)
		}
		content := envOr("SWARMAKER_TEST_FILE_CONTENT", strings.Repeat("file harness output ", 15))
		if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v", err)
			os.Exit(2)
		}
		extraFile := strings.TrimSpace(os.Getenv("SWARMAKER_TEST_EXTRA_FILE"))
		if extraFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_TEST_EXTRA_FILE")
			os.Exit(2)
		}
		if err := os.WriteFile(extraFile, []byte("unexpected extra file"), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write extra output: %v", err)
			os.Exit(2)
		}
		fmt.Print(stdout)
	case "delete-file":
		if outputFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_OUTPUT_FILE")
			os.Exit(2)
		}
		content := envOr("SWARMAKER_TEST_FILE_CONTENT", strings.Repeat("file harness output ", 15))
		if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v", err)
			os.Exit(2)
		}
		deleteFile := strings.TrimSpace(os.Getenv("SWARMAKER_TEST_DELETE_FILE"))
		if deleteFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_TEST_DELETE_FILE")
			os.Exit(2)
		}
		if err := os.Remove(deleteFile); err != nil {
			fmt.Fprintf(os.Stderr, "delete file: %v", err)
			os.Exit(2)
		}
		fmt.Print(stdout)
	case "stdin-echo":
		// Read prompt from stdin and echo it to stdout.
		// Used to verify stdin-based prompt delivery for large prompts.
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading stdin: %v", err)
			os.Exit(2)
		}
		fmt.Print(string(data))
	case "stdin-to-file":
		// Read prompt from stdin and write to output file.
		// Used to verify stdin-based prompt delivery with file output.
		if outputFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_OUTPUT_FILE")
			os.Exit(2)
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading stdin: %v", err)
			os.Exit(2)
		}
		if err := os.WriteFile(outputFile, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v", err)
			os.Exit(2)
		}
		fmt.Print(stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown behavior %q", behavior)
		os.Exit(2)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func exitCodeFromEnv(fallback int) int {
	return envInt("SWARMAKER_TEST_EXIT_CODE", fallback)
}
