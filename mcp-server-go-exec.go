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

type ExecParams struct {
	Cmd string `json:"cmd"`
	Cwd string `json:"cwd"`
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

func main() {
	fmt.Fprintln(os.Stderr, "MCP Exec Server (DANGEROUS) started")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintln(os.Stderr, "Invalid JSON:", err)
			continue
		}

		fmt.Fprintln(os.Stderr, "→", req.Method, "ID:", string(req.ID))

		switch req.Method {
		case "initialize":
			handleInitialize(req)

		case "notifications/initialized", "initialized":
			fmt.Fprintln(os.Stderr, "Received initialized notification")
			continue

		case "tools/list":
			handleToolsList(req)

		case "tools/call":
			handleToolCall(req)

		case "notifications/cancelled":
			fmt.Fprintln(os.Stderr, "Received notifications/cancelled – ignoring")
			continue

		default:
			fmt.Fprintln(os.Stderr, "Unhandled method:", req.Method)
			if len(req.ID) > 0 {
				send(req.ID, map[string]interface{}{}, nil)
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
			"name":    "exec-mcp-server",
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
			"cmd": map[string]interface{}{
				"type":        "string",
				"description": "Shell command to execute (via bash -c)",
			},
			"cwd": map[string]interface{}{
				"type":        "string",
				"description": "Working directory (optional)",
			},
		},
		"required": []string{"cmd"},
	}

	tool := map[string]interface{}{
		"name":        "exec",
		"description": "Execute arbitrary shell commands via bash -c (VERY DANGEROUS – only in trusted/sandboxed environments)",
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

	if p.Name != "exec" {
		send(req.ID, nil, &RPCError{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", p.Name)})
		return
	}

	cmd, ok := p.Arguments["cmd"].(string)
	if !ok || cmd == "" {
		send(req.ID, nil, &RPCError{Code: -32602, Message: "Missing or invalid 'cmd' parameter"})
		return
	}

	cwd, _ := p.Arguments["cwd"].(string)

	fmt.Fprintf(os.Stderr, "Executing: %s in dir: %s\n", cmd, cwd)
	stdout, stderr, runErr := runCommand(cmd, cwd)

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

func runCommand(cmdStr string, cwd string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	if cwd != "" {
		cmd.Dir = cwd
		fmt.Fprintf(os.Stderr, "Working directory: %s\n", cwd)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	fmt.Fprintf(os.Stderr, "Running command: %s\n", cmdStr)

	err = cmd.Run()

	stdout = outBuf.String()
	stderr = errBuf.String()

	fmt.Fprintf(os.Stderr, "Command finished. Exit: %v, stdout: %d bytes, stderr: %d bytes\n",
		err, len(stdout), len(stderr))

	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("command timed out after 120 seconds")
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

	b, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		fmt.Fprintln(os.Stderr, "json.Marshal failed:", marshalErr)
		return
	}

	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "← sent response for ID: %s\n", string(id))
}
