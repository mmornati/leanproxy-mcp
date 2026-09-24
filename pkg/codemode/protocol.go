//go:build codemode

package codemode

import "encoding/json"

// The proxy and its sandbox process speak newline-delimited JSON over the
// child's stdin and stdout; nothing else connects them.
//
//	proxy -> sandbox  {"type":"run", "code":..., "servers":[...], "limits":{...}}
//	sandbox -> proxy  {"type":"call", "id":1, "server":"github", "tool":"list_issues", "arguments":{...}}
//	proxy -> sandbox  {"type":"result", "id":1, "value":<JSON>}  or  {"type":"result", "id":1, "error":"..."}
//	sandbox -> proxy  {"type":"done", "output":"...", "logs":[...]}  or  {"type":"done", "error":"...", "kind":"timeout"}
//
// The sandbox never sees a raw MCP result: the proxy decodes each tool
// result (after the whole middleware pipeline ran on it) into the value
// the program gets (see toolValue).

const (
	msgRun    = "run"
	msgCall   = "call"
	msgResult = "result"
	msgDone   = "done"
)

// Failure kinds a done message (or the proxy, when the sandbox dies) can
// report.
const (
	KindError   = "error"   // the program threw, or failed to compile
	KindTimeout = "timeout" // the wall-clock limit
	KindCPU     = "cpu"     // the CPU time limit
	KindMemory  = "memory"  // the memory limit
	KindOutput  = "output"  // the output size limit
	KindCalls   = "calls"   // the tool call limit
	KindCrash   = "crash"   // the sandbox process died without a verdict
)

type message struct {
	Type string `json:"type"`

	// run
	Code    string   `json:"code,omitempty"`
	Servers []string `json:"servers,omitempty"`
	Limits  *Limits  `json:"limits,omitempty"`

	// call / result
	ID        int64           `json:"id,omitempty"`
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`

	// result / done
	Error string `json:"error,omitempty"`
	Kind  string `json:"kind,omitempty"`

	// done
	Output string   `json:"output,omitempty"`
	Logs   []string `json:"logs,omitempty"`
}
