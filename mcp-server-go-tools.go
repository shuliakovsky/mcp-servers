package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type Request struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type GoToolParams struct {
	Action string `json:"action"`
	Cwd    string `json:"cwd"`
}

type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type Response struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var allowed = map[string][]string{
	"vet":    {"go", "vet", "./..."},
	"test":   {"go", "test", "-json", "./..."},
	"build":  {"go", "build", "-o", "/dev/null", "./..."},
	"fmt":    {"go", "fmt", "./..."},
	"tidy":   {"go", "mod", "tidy"},
	"verify": {"go", "mod", "verify"},
}

func main() {
	fmt.Fprintln(os.Stderr, "MCP Go Tools server started")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintln(os.Stderr, "JSON parse error:", err)
			continue
		}

		fmt.Fprintln(os.Stderr, "→ Method:", req.Method, "ID:", string(req.ID))

		switch req.Method {
		case "initialize":
			handleInitialize(req)

		case "initialized":
			// Уведомление без ответа
			continue

		case "tools/list":
			handleToolsList(req)

		case "tools/call":
			handleToolCall(req)

		default:
			if len(req.ID) > 0 {
				send(req.ID, map[string]any{}, nil)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "Scanner error:", err)
	}
}

func handleInitialize(req Request) {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]interface{}{
			"name":    "go-tools-email-checker",
			"version": "0.1.0",
		},
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
	}

	send(req.ID, result, nil)
}

func handleToolsList(req Request) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "One of: vet, test, build, fmt, tidy, verify",
				"enum":        []string{"vet", "test", "build", "fmt", "tidy", "verify"},
			},
			"cwd": map[string]interface{}{
				"type":        "string",
				"description": "Path to the project directory (optional)",
			},
		},
		"required": []string{"action"},
	}

	tool := map[string]interface{}{
		"name":        "go_tool",
		"description": "Executes allowed Go commands in the project (vet, test with -json, build check, fmt, tidy, verify)",
		"inputSchema": schema,
	}

	response := map[string]interface{}{
		"tools": []map[string]interface{}{tool},
	}

	send(req.ID, response, nil)
}

func handleToolCall(req Request) {
	var p ToolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to parse params:", err)
		send(req.ID, nil, &RPCError{Code: -32602, Message: "Invalid params"})
		return
	}

	fmt.Fprintf(os.Stderr, "Tool call: %s with args: %+v\n", p.Name, p.Arguments)

	if p.Name != "go_tool" {
		send(req.ID, nil, &RPCError{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", p.Name)})
		return
	}

	action, ok := p.Arguments["action"].(string)
	if !ok {
		send(req.ID, nil, &RPCError{Code: -32602, Message: "Missing or invalid 'action' parameter"})
		return
	}

	cwd, _ := p.Arguments["cwd"].(string)

	args, ok := allowed[action]
	if !ok {
		send(req.ID, nil, &RPCError{Code: -32602, Message: fmt.Sprintf("Action '%s' not allowed", action)})
		return
	}

	fmt.Fprintf(os.Stderr, "Executing: %v in dir: %s\n", args, cwd)
	stdout, stderr, runErr := runCommand(args, cwd)

	var textContent string
	if stdout != "" {
		textContent = stdout
	}
	if stderr != "" {
		if textContent != "" {
			textContent += "\n--- stderr ---\n"
		}
		textContent += stderr
	}

	if textContent == "" {
		if runErr != nil {
			textContent = fmt.Sprintf("Command failed: %v", runErr)
		} else {
			textContent = "Command completed successfully (no output)"
		}
	}

	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": textContent,
			},
		},
	}

	if runErr != nil {
		result["isError"] = true
	}

	fmt.Fprintf(os.Stderr, "Sending result with %d chars of text, isError=%v\n", len(textContent), runErr != nil)
	send(req.ID, result, nil)
}

func runCommand(args []string, cwd string) (stdout, stderr string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("empty command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
		fmt.Fprintf(os.Stderr, "Working directory: %s\n", cwd)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	fmt.Fprintf(os.Stderr, "Running: %v\n", args)

	err = cmd.Run()

	stdout = outBuf.String()
	stderr = errBuf.String()

	fmt.Fprintf(os.Stderr, "Command finished. Exit: %v, stdout: %d bytes, stderr: %d bytes\n",
		err, len(stdout), len(stderr))

	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("command timed out after 60 seconds")
	}

	return stdout, stderr, err
}

func send(id json.RawMessage, result interface{}, err *RPCError) {
	resp := Response{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  result,
		Error:   err,
	}

	b, errMarshal := json.Marshal(resp)
	if errMarshal != nil {
		fmt.Fprintln(os.Stderr, "Failed to marshal response:", errMarshal)
		return
	}

	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "← Sent response for ID: %s\n", string(id))
}
