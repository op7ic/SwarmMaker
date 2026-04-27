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
		behavior = "write-file"
	}

	outputFile := os.Getenv("SWARMAKER_OUTPUT_FILE")
	stdout := envOr("SWARMAKER_TEST_STDOUT", strings.Repeat("stdout harness output ", 15))

	switch behavior {
	case "stdout-only":
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
	case "fail":
		fmt.Fprint(os.Stderr, "simulated failure")
		os.Exit(1)
	case "slow":
		sleepMS := envInt("SWARMAKER_TEST_SLEEP_MS", 50)
		time.Sleep(time.Duration(sleepMS) * time.Millisecond)
		if outputFile != "" {
			content := envOr("SWARMAKER_TEST_FILE_CONTENT", strings.Repeat("slow harness output ", 15))
			if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "write output: %v", err)
				os.Exit(2)
			}
		}
		fmt.Print(stdout)
	case "stdin-echo":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read stdin: %v", err)
			os.Exit(2)
		}
		fmt.Print(string(data))
	case "stdin-to-file":
		if outputFile == "" {
			fmt.Fprint(os.Stderr, "missing SWARMAKER_OUTPUT_FILE")
			os.Exit(2)
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read stdin: %v", err)
			os.Exit(2)
		}
		if err := os.WriteFile(outputFile, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v", err)
			os.Exit(2)
		}
		fmt.Print(string(data))
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
