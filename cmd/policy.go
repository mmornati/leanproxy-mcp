package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/policy"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
)

// Per-tool policy CLI (issue #314): `leanproxy-mcp policy check
// <server.tool>` explains which rule decides a call.

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Inspect the per-tool policy (allow / deny / confirm)",
	Long: `The policy: block of the leanproxy config decides, for every tool call and in
both front ends (server run --stdio and serve), whether it is allowed, denied or
needs the user's confirmation. See "Per-tool policy" in docs/configuration.md.`,
}

var policyCheckFlags struct {
	annotations []string
	jsonOut     bool
}

var policyCheckCmd = &cobra.Command{
	Use:   "check <server.tool>",
	Short: "Explain which policy rule decides a call to a tool",
	Long: `Evaluate the policy for one tool, exactly as the proxy does for a call: the
tool's annotations and whether the server advertises it come from the
persistent tool cache (written by a running proxy); --annotation overrides
or adds a hint, e.g. --annotation destructiveHint=true.`,
	Example: `  leanproxy-mcp policy check github.delete_file
  leanproxy-mcp policy check fs.remove --annotation destructiveHint=true`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadPolicyConfig()
		if err != nil {
			return err
		}
		e, err := policy.Compile(cfg.Policy)
		if err != nil {
			return err
		}
		return runPolicyCheck(cmd.OutOrStdout(), cfg, e, args[0], policyCheckFlags.annotations, policyCheckFlags.jsonOut)
	},
}

func init() {
	policyCheckCmd.Flags().StringArrayVar(&policyCheckFlags.annotations, "annotation", nil, "Tool annotation to assume, as name=true|false (readOnlyHint, destructiveHint, idempotentHint, openWorldHint); repeatable")
	policyCheckCmd.Flags().BoolVar(&policyCheckFlags.jsonOut, "json", false, "Print the explanation as JSON")
	policyCmd.AddCommand(policyCheckCmd)
	RootCmd.AddCommand(policyCmd)
}

// newPolicy builds the per-tool policy of a front end from the policy:
// block (already validated when the config was loaded). The firewall's
// redactor redacts the arguments a confirmation shows.
func newPolicy(cfg *migrate.Config, firewall *mcp.Firewall) *mcp.Policy {
	var pcfg *policy.Config
	if cfg != nil {
		pcfg = cfg.Policy
	}
	e, err := policy.Compile(pcfg)
	if err != nil {
		logError("policy: %v", err)
	}
	p := mcp.NewPolicy(e)
	if firewall != nil && firewall.Redaction != nil {
		p.SetRedactor(firewall.Redaction.RedactText)
	}
	return p
}

// loadPolicyConfig loads the proxy config (the empty config when there is
// none).
func loadPolicyConfig() (*migrate.Config, error) {
	path := GlobalConfigPath
	if path == "" {
		path = userConfigPath()
	}
	cfg, err := migrate.LoadConfig(context.Background(), path)
	if err != nil {
		if errors.Is(err, migrate.ErrConfigNotFound) {
			return &migrate.Config{}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// policyCheckResult is `policy check --json`.
type policyCheckResult struct {
	Server      string            `json:"server"`
	Tool        string            `json:"tool"`
	Listed      string            `json:"listed"` // yes, no, unknown
	Annotations map[string]bool   `json:"annotations,omitempty"`
	Decision    string            `json:"decision"`
	Rule        string            `json:"rule"`
	Steps       []policyCheckStep `json:"steps,omitempty"`
}

type policyCheckStep struct {
	Rule        string `json:"rule"`
	Glob        bool   `json:"glob_matches"`
	Annotations bool   `json:"annotations_match"`
	Action      string `json:"action"`
}

func runPolicyCheck(w io.Writer, cfg *migrate.Config, e *policy.Engine, ref string, overrides []string, jsonOut bool) error {
	servers := make([]string, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s != nil {
			servers = append(servers, s.Name)
		}
	}
	server, tool, err := mcp.SplitToolName(ref, servers)
	if err != nil {
		return fmt.Errorf("%q does not name a tool of a configured server (%s): expected <server>.<tool>", ref, strings.Join(servers, ", "))
	}

	// What the proxy knows of the tool: the persistent tool cache.
	listed := "unknown"
	var hints policy.Hints
	annotations := map[string]bool{}
	if store, err := toolstore.NewFileCache(slog.Default()); err == nil {
		if cached, err := store.GetTools(server); err == nil && cached != nil {
			listed = "no"
			for _, ct := range cached {
				if ct.Name != tool {
					continue
				}
				listed = "yes"
				var a mcp.ToolAnnotations
				if len(ct.Annotations) > 0 && json.Unmarshal(ct.Annotations, &a) == nil {
					setHint(annotations, policy.HintReadOnly, a.ReadOnlyHint)
					setHint(annotations, policy.HintDestructive, a.DestructiveHint)
					setHint(annotations, policy.HintIdempotent, a.IdempotentHint)
					setHint(annotations, policy.HintOpenWorld, a.OpenWorldHint)
				}
			}
		}
	}
	for _, o := range overrides {
		name, val, ok := strings.Cut(o, "=")
		b, perr := strconv.ParseBool(strings.TrimSpace(val))
		name = strings.TrimSpace(name)
		if !ok || perr != nil {
			return fmt.Errorf("--annotation %q: expected name=true|false", o)
		}
		switch name {
		case policy.HintReadOnly, policy.HintDestructive, policy.HintIdempotent, policy.HintOpenWorld:
		default:
			return fmt.Errorf("--annotation %q: unknown hint (readOnlyHint, destructiveHint, idempotentHint, openWorldHint)", o)
		}
		annotations[name] = b
	}
	for name, v := range annotations {
		switch name {
		case policy.HintReadOnly:
			hints.ReadOnly = &v
		case policy.HintDestructive:
			hints.Destructive = &v
		case policy.HintIdempotent:
			hints.Idempotent = &v
		case policy.HintOpenWorld:
			hints.OpenWorld = &v
		}
	}

	// An unknown listing (no cached tool list) is evaluated as listed: the
	// running proxy fetches the list before deciding.
	ex := e.Explain(server, tool, hints, listed != "no")
	res := policyCheckResult{
		Server: server, Tool: tool, Listed: listed, Annotations: annotations,
		Decision: string(ex.Decision.Action), Rule: ex.Decision.Label,
	}
	for _, st := range ex.Steps {
		res.Steps = append(res.Steps, policyCheckStep{Rule: st.Label, Glob: st.Glob, Annotations: st.Hints, Action: string(st.Action)})
	}
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	fmt.Fprintf(w, "Tool:        %s.%s\n", server, tool)
	switch listed {
	case "yes":
		fmt.Fprintf(w, "Listed:      yes (in the cached tools/list of %s)\n", server)
	case "no":
		fmt.Fprintf(w, "Listed:      no (not in the cached tools/list of %s)\n", server)
	default:
		fmt.Fprintf(w, "Listed:      unknown (no cached tools/list for %s yet; evaluated as listed)\n", server)
	}
	if len(annotations) > 0 {
		names := make([]string, 0, len(annotations))
		for n, v := range annotations {
			names = append(names, n+"="+strconv.FormatBool(v))
		}
		sort.Strings(names)
		fmt.Fprintf(w, "Annotations: %s\n", strings.Join(names, ", "))
	} else {
		fmt.Fprintln(w, "Annotations: none known")
	}
	fmt.Fprintf(w, "Policy:      %s\n", strings.TrimPrefix(e.Summary(), "policy: "))
	if len(ex.Steps) > 0 {
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  RULE\tGLOB\tANNOTATIONS\tACTION\t")
		for _, st := range ex.Steps {
			mark := ""
			if st.Glob && st.Hints {
				mark = "<- first match"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", st.Label, yesNo(st.Glob), yesNo(st.Hints), st.Action, mark)
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(w)
	switch ex.Decision.Rule {
	case policy.RuleUnknownTools:
		fmt.Fprintf(w, "Decision:    %s (policy.unknown_tools: the server does not advertise this tool)\n", ex.Decision.Action)
	case policy.RuleDefault:
		fmt.Fprintf(w, "Decision:    %s (no rule matches: policy.default)\n", ex.Decision.Action)
	default:
		fmt.Fprintf(w, "Decision:    %s (%s)\n", ex.Decision.Action, ex.Decision.Label)
	}
	return nil
}

func setHint(m map[string]bool, name string, v *bool) {
	if v != nil {
		m[name] = *v
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// printPolicyStatus is the policy section of `doctor security` (#314).
func printPolicyStatus(w io.Writer) {
	fmt.Fprintln(w, "## Per-Tool Policy")
	fmt.Fprintln(w)
	cfg, err := loadPolicyConfig()
	if err != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", err)
		return
	}
	e, err := policy.Compile(cfg.Policy)
	if err != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", err)
		return
	}
	fmt.Fprintf(w, "  Default: %s\n", e.Default())
	fmt.Fprintf(w, "  Unknown tools (not in the server's tools/list): %s\n", e.UnknownTools())
	fmt.Fprintf(w, "  Confirmation timeout: %s\n", e.ConfirmTimeout())
	rules := e.Rules()
	fmt.Fprintf(w, "  Rules (first match wins): %d\n", len(rules))
	for i := range rules {
		fmt.Fprintf(w, "    - %s\n", e.Describe(i))
	}
	if cfg.Policy == nil {
		fmt.Fprintln(w, "  No policy: block: every advertised tool is allowed; calls to tools a server does not advertise are refused.")
	}
	fmt.Fprintln(w, "  Explain a decision: leanproxy-mcp policy check <server.tool>")
}
