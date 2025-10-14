package debug

import (
	"errors"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const dangerousCharSet = "$`&;<>(){}[]*?"

var ErrRejected = errors.New("command rejected by policy")

// ValidateAndExtract parses the input shell text and, if allowed by policy (i.e. the rules within this fn),
// returns (absCmdPath, args, nil). Otherwise returns ErrRejected.
func ValidateAndExtract(input string, whitelist []string) (absCmd string, args []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: recover error: %v", ErrRejected, r)
		}
	}()

	// Parse into an AST for introspection
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	ast, err := p.Parse(strings.NewReader(input), input)
	if err != nil {
		return "", nil, fmt.Errorf("%w: parse error: %v", ErrRejected, err)
	}

	// Reject any remaining dangerous nodes
	unsafe := walkForDangerousNodeTypes(ast)
	if unsafe {
		return "", nil, fmt.Errorf("%w: `%s` contains unsafe statements", ErrRejected, input)
	}

	// Must be exactly one statement (complete command).
	if len(ast.Stmts) == 0 {
		return "", nil, fmt.Errorf("%w: no statements found within parsed command: %s", ErrRejected, input)
	}

	for _, stmt := range ast.Stmts {
		err := validateStmt(stmt, whitelist)
		if err != nil {
			return "", nil, err
		}
	}

	finalCmd := strings.Split(input, " ")

	return finalCmd[0], finalCmd[1:], nil
}

// Helper which validates a provided statement against statement-specific policies, along with the whitelist.
// Recurses on any found pipelines, to validate the commands on either side.
//
// Returns nil on success, or the specific validation error, if any.
func validateStmt(stmt *syntax.Stmt, whitelist []string) error {
	// Disallow negation, background, coprocessing, semicolons, and redirects.
	if stmt.Negated {
		return fmt.Errorf("%w: negation '!' not allowed", ErrRejected)
	}
	if stmt.Background {
		return fmt.Errorf("%w: background '&' not allowed", ErrRejected)
	}
	if stmt.Coprocess {
		return fmt.Errorf("%w: coprocess '|&' not allowed", ErrRejected)
	}
	// Semicolon valid -> multiple commands. Deny.
	if stmt.Semicolon.IsValid() {
		return fmt.Errorf("%w: multiple/terminated commands not allowed", ErrRejected)
	}
	if len(stmt.Redirs) > 0 {
		return fmt.Errorf("%w: redirects not allowed", ErrRejected)
	}

	// Command must be a simple call expression or pipeline (no binary/case/...).
	switch stmt.Cmd.(type) {
	case *syntax.CallExpr:
		call := stmt.Cmd.(*syntax.CallExpr)
		// Disallow assignments in the call (e.g. FOO=bar echo ...)
		if len(call.Assigns) > 0 {
			return fmt.Errorf("%w: inline assignments not allowed", ErrRejected)
		}

		// There must be at least one argument (the command name).
		if len(call.Args) == 0 {
			return fmt.Errorf("%w: empty call", ErrRejected)
		}

		// Every Word must be a literal (no param/cmd/arith/brace/glob/...).
		for i, w := range call.Args {
			lit := w.Lit()
			if lit == "" {
				return fmt.Errorf("%w: word %d is not a plain literal (contains expansions/substs/globs)", ErrRejected, i)
			}
		}

		// The first argv element is the command name. Look it up in whitelist.
		cmdName := call.Args[0].Lit()
		if !sliceContains(whitelist, cmdName) {
			return fmt.Errorf("%w: command %q is not whitelisted", ErrRejected, cmdName)
		}
	case *syntax.BinaryCmd:
		binCmd := stmt.Cmd.(*syntax.BinaryCmd)
		if binCmd.Op != syntax.Pipe {
			return fmt.Errorf("%w: only simple commands and pipelines allowed (no subshells, control structures, etc)", ErrRejected)
		}

		errX := validateStmt(binCmd.X, whitelist)
		if errX != nil {
			return errX
		}
		errY := validateStmt(binCmd.Y, whitelist)
		if errY != nil {
			return errY
		}
	default:
		return fmt.Errorf("%w: only simple commands and pipelines allowed (no subshells, control structures, etc)", ErrRejected)
	}

	return nil
}

// Helper which walks a given AST from the provided node, checking for potentially dangerous node types.
// Returns false if any are found.
func walkForDangerousNodeTypes(node syntax.Node) bool {
	var unsafe bool

	syntax.Walk(node, func(node syntax.Node) bool {
		switch curr := node.(type) {
		case *syntax.Subshell, *syntax.CmdSubst, *syntax.ArithmExp:
			unsafe = true
			return false
		case *syntax.BinaryCmd:
			if curr.Op == syntax.Pipe {
				return true
			}

			unsafe = true
			return false
		case *syntax.Redirect:
			unsafe = true
			return false
		case *syntax.ParamExp:
			unsafe = true
			return false
		case *syntax.ExtGlob:
			unsafe = true
			return false
		case *syntax.Lit:
			if strings.ContainsAny(curr.Value, dangerousCharSet) {
				unsafe = true
				return false
			}
		}

		return true
	})

	return unsafe
}

// Helper which ports the slices.Contains functionality for string slices to this version of Go.
// Returns whether the string exists within the provided slice.
func sliceContains(slice []string, str string) bool {
	for _, slice_str := range slice {
		if slice_str == str {
			return true
		}
	}

	return false
}
