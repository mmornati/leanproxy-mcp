// mockmcp can be used as a library (Server type in server.go) or as a
// standalone stdio MCP server (this file). Build with:
//   go build ./tests/bench/mockmcp/cmd
// Run with:
//   ./cmd --tools=41 --response-bytes=256
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mmornati/leanproxy-mcp/tests/bench/mockmcp"
)

func main() {
	var (
		tools      = flag.Int("tools", 41, "number of tools to advertise")
		prefix     = flag.String("prefix", "tool", "tool name prefix")
		desc       = flag.String("description", "Mock MCP tool used for benchmarking LeanProxy.", "description for every tool")
		respBytes  = flag.Int("response-bytes", 256, "canned tools/call response size in bytes")
	)
	flag.Parse()

	srv := mockmcp.New(mockmcp.Config{
		ToolCount:       *tools,
		ToolNamePrefix:  *prefix,
		DescriptionBase: *desc,
		ResponseBytes:   *respBytes,
	})

	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "mockmcp: read: %v\n", err)
			os.Exit(1)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		resp, err := srv.HandleRequest(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mockmcp: handle: %v\n", err)
			continue
		}
		if resp == "" {
			continue
		}
		if _, err := io.WriteString(os.Stdout, resp+"\n"); err != nil {
			fmt.Fprintf(os.Stderr, "mockmcp: write: %v\n", err)
			return
		}
	}
}
