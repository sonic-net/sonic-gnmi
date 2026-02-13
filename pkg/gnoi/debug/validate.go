package debug

import (
	"errors"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const dangerousCharSet = "~$`&;<>(){}[]*?"

var ErrRejected = errors.New("command rejected by policy")

// ValidateCommand parses the input shell text and, if allowed by policy (i.e. the rules within this fn).
// Policies:
//   - Must not contain potentially dangerous characters: $`&;<>(){}[]*?
//   - Must not contain potentially dangerous node types:
//   - Subshell
//   - Command substitution
//   - Arithmetic expression
//   - Binary commands (other than pipe)
//   - Redirect
//   - Parameter expansion
//   - Extended glob expressions
//   - Must contain at least one statement
//   - Each statement must not contain:
//   - Negation
//   - Background
//   - Coprocess
//   - Semicolons
//   - Redirect
//   - Each statement must either be a:
//   - Call expression (evaluated against the above)
//   - Binary pipe expression (with left and right recursed upon)
//
// Returns nil for valid command. Otherwise returns ErrRejected.
func ValidateCommand(input string, whitelist []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: recover error: %v", ErrRejected, r)
		}
	}()

	// Parse into an AST for introspection
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	ast, err := p.Parse(strings.NewReader(input), input)
	if err != nil {
		return fmt.Errorf("%w: parse error: %v", ErrRejected, err)
	}

	// Reject any remaining dangerous nodes
	unsafe := walkForDangerousNodeTypes(ast)
	if unsafe {
		return fmt.Errorf("%w: `%s` contains unsafe statements", ErrRejected, input)
	}

	// Must be contain at least one statement
	if len(ast.Stmts) == 0 {
		return fmt.Errorf("%w: no statements found within parsed command: %s", ErrRejected, input)
	}

	for _, statement := range ast.Stmts {
		err := validateStatement(statement, whitelist)
		if err != nil {
			return err
		}
	}

	return nil
}

// Helper which validates a provided statement against statement-specific policies, along with the whitelist.
// Recurses on any found pipelines, to validate the commands on either side.
//
// Returns nil on success, or the specific validation error, if any.
func validateStatement(statement *syntax.Stmt, whitelist []string) error {
	// Disallow negation, background, coprocessing, semicolons, and redirects.
	if statement.Negated {
		return fmt.Errorf("%w: negation '!' not allowed", ErrRejected)
	}
	if statement.Background {
		return fmt.Errorf("%w: background '&' not allowed", ErrRejected)
	}
	if statement.Coprocess {
		return fmt.Errorf("%w: coprocess '|&' not allowed", ErrRejected)
	}
	// Semicolon valid -> multiple commands. Deny.
	if statement.Semicolon.IsValid() {
		return fmt.Errorf("%w: multiple/terminated commands not allowed", ErrRejected)
	}
	if len(statement.Redirs) > 0 {
		return fmt.Errorf("%w: redirects not allowed", ErrRejected)
	}

	// Command must be a simple call expression or pipeline (no binary/case/...).
	switch statement.Cmd.(type) {
	case *syntax.CallExpr:
		call := statement.Cmd.(*syntax.CallExpr)
		// Disallow assignments in the call (e.g. FOO=bar echo ...)
		if len(call.Assigns) > 0 {
			return fmt.Errorf("%w: inline assignments not allowed", ErrRejected)
		}

		// There must be at least one argument (the command name).
		if len(call.Args) == 0 {
			return fmt.Errorf("%w: empty call", ErrRejected)
		}

		// Check that every word only consists of word parts with safe node types
		for _, word := range call.Args {
			err := validateWordParts(word, word.Parts)
			if err != nil {
				return err
			}
		}

		// The first argv element is the command name. Look it up in whitelist.
		cmdName := call.Args[0].Lit()
		if !sliceContains(whitelist, cmdName) {
			return fmt.Errorf("%w: command %q is not whitelisted", ErrRejected, cmdName)
		}
	case *syntax.BinaryCmd:
		binCmd := statement.Cmd.(*syntax.BinaryCmd)
		// Only allow pipeline
		if binCmd.Op != syntax.Pipe {
			return fmt.Errorf("%w: only simple commands and pipelines allowed (no subshells, control structures, etc)", ErrRejected)
		}

		// Validate statements on both sides of the operator
		errX := validateStatement(binCmd.X, whitelist)
		if errX != nil {
			return errX
		}
		errY := validateStatement(binCmd.Y, whitelist)
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

// Helper to determine whether a set of word parts contains any potentially dangerous constructs.
// Allowed word part types are:
//   - Lit: Represents a literal, will not be affected by expansion
//   - SglQuoted: Represents a string enclosed in single quotes, will not be affected by expansion
//   - DblQuoted: Represents word parts enclosed in double quotes, may be affected by expansion (requiring recursive validation)
func validateWordParts(word *syntax.Word, wordParts []syntax.WordPart) error {
	for _, part := range wordParts {
		switch partType := part.(type) {
		case *syntax.Lit:
			continue
		case *syntax.SglQuoted:
			continue
		case *syntax.DblQuoted:
			subParts := part.(*syntax.DblQuoted).Parts
			err := validateWordParts(word, subParts)
			if err != nil {
				return err
			}
			continue
		default:
			var wordString strings.Builder
			var wordPartString strings.Builder
			printer := syntax.NewPrinter()
			printer.Print(&wordString, word)
			printer.Print(&wordPartString, part)

			return fmt.Errorf("%w: '%s' contained in '%s' is invalid type: %s", ErrRejected, wordPartString.String(), wordString.String(), partType)
		}
	}

	return nil
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
