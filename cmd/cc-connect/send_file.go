package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func runSendFile(args []string) {
	var project, sessionKey, dataDir, caption string

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		case "--session", "-s":
			if i+1 < len(args) {
				i++
				sessionKey = args[i]
			}
		case "--caption", "-c":
			if i+1 < len(args) {
				i++
				caption = args[i]
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--help", "-h":
			printSendFileUsage()
			return
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) == 0 {
		fmt.Fprintln(os.Stderr, "Error: file path is required")
		printSendFileUsage()
		os.Exit(1)
	}

	filePath := positional[0]

	// Resolve to absolute path
	if !filepath.IsAbs(filePath) {
		abs, err := filepath.Abs(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot resolve path: %v\n", err)
			os.Exit(1)
		}
		filePath = abs
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: file not found: %s\n", filePath)
		os.Exit(1)
	}

	// Resolve env vars
	if project == "" {
		project = os.Getenv("CC_PROJECT")
	}
	if sessionKey == "" {
		sessionKey = os.Getenv("CC_SESSION_KEY")
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]string{
		"project":     project,
		"session_key": sessionKey,
		"file_path":   filePath,
		"caption":     caption,
	})

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Post("http://unix/send-file", "application/json", bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		os.Exit(1)
	}

	fmt.Println("File sent successfully.")
}

func printSendFileUsage() {
	fmt.Println(`Usage: cc-connect send-file [options] <path>

Send a file as a document attachment to the active chat session.
Markdown (.md) files are automatically converted to HTML for mobile viewing.

Options:
  -c, --caption <text>     Caption to display under the file
  -p, --project <name>     Target project (optional if only one project)
  -s, --session <key>      Target session key (optional, picks first active)
      --data-dir <path>    Data directory (default: ~/.cc-connect)
  -h, --help               Show this help

Examples:
  cc-connect send-file report.md
  cc-connect send-file --caption "Weekly report" results/report.md
  cc-connect send-file -c "Build log" /tmp/build.txt`)
}
