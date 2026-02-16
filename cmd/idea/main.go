// Copyright 2025 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"changkun.de/x/login"
	"golang.org/x/term"
)

func main() {
	title := flag.String("t", "", "idea title (optional, auto-generated if empty)")
	flag.Parse()

	url := os.Getenv("IDEAS_URL")
	if url == "" {
		url = "https://api.changkun.de"
	}
	if v := os.Getenv("LOGIN_URL"); v != "" {
		login.AuthEndpoint = strings.TrimRight(v, "/") + "/auth"
	}
	loginUser := os.Getenv("LOGIN_USER")
	if loginUser == "" {
		fmt.Fprintln(os.Stderr, "LOGIN_USER is required")
		os.Exit(1)
	}
	loginPass := os.Getenv("LOGIN_PASS")
	if loginPass == "" {
		fmt.Fprintln(os.Stderr, "LOGIN_PASS is required")
		os.Exit(1)
	}

	// Obtain JWT from login service.
	token, err := login.RequestToken(loginUser, loginPass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
		os.Exit(1)
	}

	var content string

	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println("idea (Alt+Enter or Ctrl+J for newline, Enter to send)")
		content, err = readInput()
		if err != nil {
			if err.Error() == "interrupted" {
				os.Exit(130)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		content = string(data)
	}

	content = strings.TrimSpace(content)
	if content == "" {
		os.Exit(0)
	}

	fmt.Print("Posting idea... ")

	body, _ := json.Marshal(map[string]string{
		"title":   *title,
		"content": content,
	})
	req, _ := http.NewRequest("POST", strings.TrimRight(url, "/")+"/ideas/post", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.OK {
		fmt.Println("done")
	} else {
		fmt.Fprintf(os.Stderr, "failed: %s\n", result.Message)
		os.Exit(1)
	}
}

const (
	prompt     = "> "
	contPrompt = "  "
)

type escAction int

const (
	escNone escAction = iota
	escNewline
	escPasteStart
	escPasteEnd
)

// parseEscape tries to parse an escape sequence from data.
// Returns (bytes consumed, action). Returns (0, escNone) if incomplete.
func parseEscape(data []byte) (int, escAction) {
	if len(data) < 2 || data[0] != 0x1b {
		return 0, escNone
	}

	// ESC [ = CSI sequence.
	if data[1] == '[' {
		for i := 2; i < len(data); i++ {
			ch := data[i]
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '~' {
				seq := string(data[2 : i+1])
				switch seq {
				case "13;2u":
					return i + 1, escNewline // Shift+Enter (kitty protocol)
				case "200~":
					return i + 1, escPasteStart
				case "201~":
					return i + 1, escPasteEnd
				}
				return i + 1, escNone
			}
		}
		if len(data) > 20 {
			return len(data), escNone
		}
		return 0, escNone // incomplete
	}

	// ESC + Enter = Alt+Enter.
	if data[1] == '\r' || data[1] == '\n' {
		return 2, escNewline
	}

	return 2, escNone
}

func readInput() (string, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)

	// Enable bracket paste mode.
	os.Stdout.WriteString("\x1b[?2004h")
	defer os.Stdout.WriteString("\x1b[?2004l")

	var buf []rune
	inPaste := false
	displayLines := 1

	write := func(s string) { os.Stdout.WriteString(s) }
	write(prompt)

	raw := make([]byte, 256)
	var pending []byte

	for {
		n, err := os.Stdin.Read(raw)
		if err != nil {
			return "", err
		}
		pending = append(pending, raw[:n]...)

		for len(pending) > 0 {
			// Escape sequences.
			if pending[0] == 0x1b {
				consumed, action := parseEscape(pending)
				if consumed == 0 {
					break // incomplete
				}
				pending = pending[consumed:]
				switch action {
				case escNewline:
					buf = append(buf, '\n')
					displayLines++
					write("\r\n" + contPrompt)
				case escPasteStart:
					inPaste = true
				case escPasteEnd:
					inPaste = false
				}
				continue
			}

			ch := pending[0]

			switch {
			case ch == 0x03: // Ctrl+C
				write("\r\n")
				return "", fmt.Errorf("interrupted")

			case ch == 0x15: // Ctrl+U: clear all
				pending = pending[1:]
				buf = nil
				displayLines = redraw(buf, displayLines)

			case ch == 0x17: // Ctrl+W: delete word
				pending = pending[1:]
				for len(buf) > 0 && buf[len(buf)-1] == ' ' {
					buf = buf[:len(buf)-1]
				}
				for len(buf) > 0 && buf[len(buf)-1] != ' ' && buf[len(buf)-1] != '\n' {
					buf = buf[:len(buf)-1]
				}
				displayLines = redraw(buf, displayLines)

			case ch == '\n': // Ctrl+J: newline
				pending = pending[1:]
				buf = append(buf, '\n')
				displayLines++
				write("\r\n" + contPrompt)

			case ch == '\r': // Enter: submit (or newline in paste mode)
				pending = pending[1:]
				if inPaste {
					buf = append(buf, '\n')
					displayLines++
					write("\r\n" + contPrompt)
				} else {
					write("\r\n")
					return string(buf), nil
				}

			case ch == 0x7f || ch == 0x08: // Backspace
				pending = pending[1:]
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
					displayLines = redraw(buf, displayLines)
				}

			default:
				r, size := utf8.DecodeRune(pending)
				if r == utf8.RuneError && size <= 1 && len(pending) < 4 {
					break // incomplete UTF-8
				}
				if r == utf8.RuneError {
					pending = pending[1:]
					continue
				}
				pending = pending[size:]
				if r >= 0x20 || r == '\t' {
					buf = append(buf, r)
					var rb [4]byte
					n := utf8.EncodeRune(rb[:], r)
					os.Stdout.Write(rb[:n])
				}
			}
		}
	}
}

// redraw clears the input area and reprints the buffer.
// Returns the new display line count.
func redraw(buf []rune, prevLines int) int {
	if prevLines > 1 {
		fmt.Fprintf(os.Stdout, "\x1b[%dA", prevLines-1)
	}
	os.Stdout.WriteString("\r\x1b[J")

	newLines := 1
	os.Stdout.WriteString(prompt)
	for _, r := range buf {
		if r == '\n' {
			newLines++
			os.Stdout.WriteString("\r\n" + contPrompt)
		} else {
			var rb [4]byte
			n := utf8.EncodeRune(rb[:], r)
			os.Stdout.Write(rb[:n])
		}
	}
	return newLines
}
