//go:build !codemode

package codemode

// Available reports whether this binary was built with `-tags codemode`,
// i.e. whether it contains the sandbox. It does not: a `code_mode:` block
// is parsed and validated, then ignored.
const Available = false
