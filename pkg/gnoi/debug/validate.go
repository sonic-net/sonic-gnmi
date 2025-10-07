package debug

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var ErrRejected = errors.New("command rejected by policy")

// ValidateAndExtract parses the input shell text and, if allowed by policy (i.e. the rules within this fn),
// returns (absCmdPath, args, nil). Otherwise returns ErrRejected.
func ValidateAndExtract(input string, whitelist map[string]string) (absCmd string, args []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: recover error: %v", ErrRejected, r)
		}
	}()

	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	f, err := p.Parse(strings.NewReader(input), "")
	if err != nil {
		return "", nil, fmt.Errorf("%w: parse error: %w", ErrRejected, err)
	}

	unsafe := walkForDangerousNodeTypes(f)
	if unsafe {
		return "", nil, fmt.Errorf("%w: `%s` contains unsafe statements", ErrRejected, input)
	}

	// Must be exactly one statement (complete command).
	if len(f.Stmts) != 1 {
		return "", nil, fmt.Errorf("%w: must be exactly one statement (got %d)", ErrRejected, len(f.Stmts))
	}
	stmt := f.Stmts[0]

	// Disallow negation, background, coprocessing, semicolons, and redirects.
	if stmt.Negated {
		return "", nil, fmt.Errorf("%w: negation '!' not allowed", ErrRejected)
	}
	if stmt.Background {
		return "", nil, fmt.Errorf("%w: background '&' not allowed", ErrRejected)
	}
	if stmt.Coprocess {
		return "", nil, fmt.Errorf("%w: coprocess '|&' not allowed", ErrRejected)
	}
	// Semicolon valid -> multiple commands. Deny.
	if stmt.Semicolon.IsValid() {
		return "", nil, fmt.Errorf("%w: multiple/terminated commands not allowed", ErrRejected)
	}
	if len(stmt.Redirs) > 0 {
		return "", nil, fmt.Errorf("%w: redirects not allowed", ErrRejected)
	}

	// Command must be a simple call expression (no binary/pipeline/case/...).
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return "", nil, fmt.Errorf("%w: only simple commands allowed (no pipelines, subshells, control structures)", ErrRejected)
	}

	// Disallow assignments in the call (e.g. FOO=bar echo ...)
	if len(call.Assigns) > 0 {
		return "", nil, fmt.Errorf("%w: inline assignments not allowed", ErrRejected)
	}

	// There must be at least one argument (the command name).
	if len(call.Args) == 0 {
		return "", nil, fmt.Errorf("%w: empty call", ErrRejected)
	}

	// Build argv: every Word must be a literal (no param/cmd/arith/brace/glob/...).
	argv := make([]string, 0, len(call.Args))
	for i, w := range call.Args {
		lit := w.Lit()
		if lit == "" {
			return "", nil, fmt.Errorf("%w: word %d is not a plain literal (contains expansions/substs/globs)", ErrRejected, i)
		}
		argv = append(argv, lit)
	}

	// The first argv element is the command name. Look it up in whitelist.
	cmdName := filepath.Base(argv[0]) // use basename for matching
	absPath, ok := whitelist[cmdName]
	if !ok {
		return "", nil, fmt.Errorf("%w: command %q is not whitelisted", ErrRejected, cmdName)
	}

	return absPath, argv[1:], nil
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
			if strings.ContainsAny(curr.Value, "$`|&;<>(){}[]*?") {
				unsafe = true
				return false
			}
		}
		return true
	})

	return unsafe
}
