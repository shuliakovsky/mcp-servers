MCP Servers: Exec & Go Tools

This repository  MCP-compatible servers written in Go:

1. exec-mcp-server — executes arbitrary shell commands via bash -c. Extremely dangerous and should only be used in fully sandboxed environments.

2. go-tools-server — provides a restricted set of safe Go tooling commands (vet, test, build, fmt, tidy, verify).

Both servers communicate over stdin/stdout and implement the MCP protocol (initialize, tools/list, tools/call).

Repository structure:
exec-server/main.go        - MCP server that executes arbitrary shell commands
go-tools-server/main.go    - MCP server that runs restricted Go tooling commands

1. Exec MCP Server

Warning: This server can execute any shell command. Use only in trusted or isolated environments.

Features:
- Provides a single tool: exec
- Executes arbitrary commands via bash -c
- Optional working directory (cwd)
- Returns stdout and stderr
- 120-second timeout

Tool schema:
name: exec
description: Execute arbitrary shell commands via bash -c
input:
cmd: string (required)
cwd: string (optional)

Example call:
{
"name": "exec",
"arguments": {
"cmd": "ls -la",
"cwd": "/tmp"
}
}

2. Go Tools MCP Server

This server runs only whitelisted Go commands.

Supported actions:
vet     -> go vet ./...
test    -> go test -json ./...
build   -> go build -o /dev/null ./...
fmt     -> go fmt ./...
tidy    -> go mod tidy
verify  -> go mod verify

Example call:
{
"name": "go_tool",
"arguments": {
"action": "test",
"cwd": "/path/to/project"
}
}

Restrictions:
- 60-second timeout
- Only predefined commands allowed
- Returns stdout and stderr

Build:
cd exec-server
go build -o exec-mcp-server

cd ../go-tools-server
go build -o go-tools-server

Run:
./exec-mcp-server
./go-tools-server

Example MCP interaction:
initialize:
{ "jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {} }

list tools:
{ "jsonrpc": "2.0", "id": 2, "method": "tools/list" }

call tool:
{
"jsonrpc": "2.0",
"id": 3,
"method": "tools/call",
"params": {
"name": "exec",
"arguments": { "cmd": "echo hello" }
}
}

Security notes:
- exec-server must never be used in production
- go-tools-server is safer but still requires a trusted environment
- neither server isolates processes or limits resource usage

License:
MIT (or add your own)
