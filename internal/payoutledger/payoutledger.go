// Package payoutledger enumerates, from source, every module account that
// transfers value to an ordinary account.
//
// # Why this exists
//
// app.newAccountFundingRestriction exempts protocol payouts from the
// first-funding minimum, and that exemption is deliberately narrow: it names
// the modules allowed to bypass the rule rather than exempting every module
// account. Narrowness has a cost. A module that gains a payout path and is not
// added to the list does not fail at review; it fails in production, at epoch
// finalization, as a halted block — because a refused payout propagates out of
// EndBlock.
//
// So the list needs a check that cannot be satisfied by agreeing with itself. A
// hand-maintained inventory compared against a hand-maintained allow-list would
// prove nothing: both are edited by the same person in the same commit. This
// package derives the inventory from the syntax tree of the real source, so a
// new payout path is visible to the test whether or not anyone remembered it.
//
// # Fail closed
//
// The parser reports an ERROR for anything it cannot resolve, never absence. A
// sender argument it cannot follow to a concrete module name is a failure, not
// a call site to skip — a skipped call site is exactly the shape that would let
// an unlisted payout path reach production. That discipline is the whole value:
// this repository has previously shipped an inventory built from a grep whose
// expected count came from the same grep.
package payoutledger

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The bank method whose first argument names the sending module account.
const payoutMethod = "SendCoinsFromModuleToAccount"

// Payout is one call site that moves value from a module account to an account.
type Payout struct {
	Module   string // resolved module name, e.g. "rewards"
	Position string // file:line, for a failure message that points somewhere
}

// SendingModules walks every non-test Go file under the given roots and returns
// the distinct module names that transfer to accounts, sorted.
//
// It returns an error rather than a partial answer when a call site cannot be
// resolved.
func SendingModules(roots ...string) ([]string, []Payout, error) {
	var found []Payout
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				// Vendored or generated trees would add call sites this rule does
				// not govern.
				if name := entry.Name(); name == "testdata" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			payouts, err := payoutsInFile(path)
			if err != nil {
				return err
			}
			found = append(found, payouts...)
			return nil
		}); err != nil {
			return nil, nil, err
		}
	}

	unique := map[string]struct{}{}
	for _, payout := range found {
		unique[payout.Module] = struct{}{}
	}
	modules := make([]string, 0, len(unique))
	for module := range unique {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	sort.Slice(found, func(i, j int) bool { return found[i].Position < found[j].Position })
	return modules, found, nil
}

func payoutsInFile(filename string) ([]Payout, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	aliases, err := importAliases(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	var payouts []Payout
	var walkErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if walkErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != payoutMethod {
			return true
		}
		position := fset.Position(call.Pos()).String()
		if len(call.Args) < 2 {
			walkErr = fmt.Errorf(
				"%s: a call to %s has %d arguments; the sending module cannot be identified",
				position, payoutMethod, len(call.Args))
			return false
		}
		module, err := resolveModule(call.Args[1], aliases, filename)
		if err != nil {
			walkErr = fmt.Errorf("%s: %w", position, err)
			return false
		}
		payouts = append(payouts, Payout{Module: module, Position: position})
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return payouts, nil
}

// resolveModule follows the sender argument to a concrete module name, or
// refuses. The two shapes it accepts are the two the codebase actually uses:
// a package-qualified ModuleName constant, and a string literal.
func resolveModule(arg ast.Expr, aliases map[string]string, filename string) (string, error) {
	switch expr := arg.(type) {
	case *ast.BasicLit:
		if expr.Kind != token.STRING {
			return "", fmt.Errorf("sending module is a non-string literal, which cannot be a module name")
		}
		value, err := strconv.Unquote(expr.Value)
		if err != nil {
			return "", fmt.Errorf("sending module literal is unparseable: %w", err)
		}
		return value, nil

	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		if !ok {
			return "", fmt.Errorf(
				"sending module is a nested selector this parser cannot follow; "+
					"give it as a package-qualified %s constant or a string literal", "ModuleName")
		}
		importPath, known := aliases[pkg.Name]
		if !known {
			return "", fmt.Errorf(
				"sending module %s.%s refers to no import in this file; it cannot be resolved",
				pkg.Name, expr.Sel.Name)
		}
		if expr.Sel.Name != "ModuleName" {
			return "", fmt.Errorf(
				"sending module is %s.%s rather than a ModuleName constant; "+
					"this parser will not guess what it holds", pkg.Name, expr.Sel.Name)
		}
		return moduleNameConst(importPath, filename)

	default:
		return "", fmt.Errorf(
			"sending module is an expression this parser cannot follow (%T); a payout path "+
				"that hides its module cannot be checked against the exemption list", arg)
	}
}

// moduleNameConst reads the ModuleName constant out of the named package. The
// package is located relative to the module root, which is found by walking up
// from the file being parsed until go.mod appears.
func moduleNameConst(importPath, fromFile string) (string, error) {
	root, modulePath, err := moduleRoot(fromFile)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(importPath, modulePath+"/") {
		return "", fmt.Errorf(
			"sending module comes from %s, which is outside this module; a payout path "+
				"from an external package cannot be attributed", importPath)
	}
	dir := filepath.Join(root, strings.TrimPrefix(importPath, modulePath+"/"))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cannot read %s to resolve ModuleName: %w", dir, err)
	}

	// Read the directory directly rather than through parser.ParseDir, which is
	// deprecated for not honoring build tags. Every non-test file is parsed, and
	// a file that will not parse is an error rather than a file to skip.
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return "", fmt.Errorf("cannot parse %s to resolve ModuleName: %w", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range value.Names {
					if ident.Name != "ModuleName" || i >= len(value.Values) {
						continue
					}
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return "", fmt.Errorf(
							"%s declares ModuleName as a non-literal; it cannot be pinned", importPath)
					}
					return strconv.Unquote(lit.Value)
				}
			}
		}
	}
	return "", fmt.Errorf("%s declares no ModuleName constant", importPath)
}

func moduleRoot(fromFile string) (root, modulePath string, err error) {
	dir, err := filepath.Abs(filepath.Dir(fromFile))
	if err != nil {
		return "", "", err
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(candidate); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if after, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
					return dir, strings.TrimSpace(after), nil
				}
			}
			return "", "", fmt.Errorf("%s declares no module path", candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("no go.mod above %s", fromFile)
		}
		dir = parent
	}
}

// importAliases maps the name each import is referred to by in this file to its
// path. A dot-import is refused: it would let a ModuleName reach a call site
// under no qualifier at all, which this parser could not attribute.
func importAliases(file *ast.File) (map[string]string, error) {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("unparseable import path %s", spec.Path.Value)
		}
		switch {
		case spec.Name == nil:
			aliases[path[strings.LastIndex(path, "/")+1:]] = path
		case spec.Name.Name == ".":
			return nil, fmt.Errorf(
				"%s is dot-imported; a module payout made through it could not be attributed", path)
		case spec.Name.Name == "_":
			// Blank imports cannot supply an identifier, so they are irrelevant.
		default:
			aliases[spec.Name.Name] = path
		}
	}
	return aliases, nil
}
