// Package errorledger enumerates the error codes a module registers, by parsing
// its source.
//
// It exists so that a ledger test can check its table against the REGISTRATIONS
// rather than against itself. A test that asserts "the table has 8 rows" and
// "codes 2 through 9 are present" proves only that the table agrees with facts
// hard-coded from the same table — it cannot notice a sentinel that was
// registered and never listed.
//
// That is not hypothetical, and it has now failed twice at different depths:
//
//   - The tables were first built by grepping for `errorsmod.Register(ModuleName,
//     N` on one line, which missed x/rewards code 10 because that call wraps its
//     arguments. Table and expected length came from the same faulty extraction,
//     so the count agreed with itself and one live code went unpinned.
//   - The first parser then looked only at a ValueSpec whose IMMEDIATE right-hand
//     side was a call on a selector, and silently skipped anything else. A
//     registration nested inside another call, or reached through a function
//     value, would execute in production and be absent from the inventory while
//     the non-empty guard still passed.
//
// Both are the same mistake: a partial view that reports success. So this parser
// treats anything it cannot account for as an ERROR rather than as absence. It
// inventories every Register call wherever it appears, requires each to be
// attributable to a declared sentinel, and refuses outright if the registration
// function escapes into a value it cannot follow.
package errorledger

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// errorsPackagePath is the module whose Register calls define an ABCI code.
const errorsPackagePath = `"cosmossdk.io/errors"`

// Registration is one error registration found in a module's errors file.
type Registration struct {
	// Code is the numeric ABCI error code.
	Code uint32
	// Sentinel is the name of the variable it is assigned to, so a failure can
	// name the error an author forgot rather than only its number.
	Sentinel string
}

// Registered parses filename and returns every code registered against the
// module's own codespace, keyed by code.
//
// Only calls whose codespace argument is the identifier `ModuleName` are
// collected: a registration against another codespace belongs to a different
// ledger. Both Register and RegisterWithGRPCCode are recognized; the code is the
// second argument in each.
//
// It returns an error rather than a partial inventory when it meets anything it
// cannot resolve — see the package comment for why that distinction is the whole
// point of the package.
func Registered(filename string) (map[uint32]Registration, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	alias, err := errorsAlias(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	// Every selector naming a registration function, and the subset actually in
	// call position. A selector that is NOT called is the function escaping into a
	// value — `register := errorsmod.Register` — after which registrations happen
	// through a name this parser cannot follow.
	seen := map[*ast.SelectorExpr]bool{}
	called := map[*ast.SelectorExpr]bool{}
	registrations := map[*ast.CallExpr]bool{} // value: attributed to a sentinel yet

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if isRegistrationSelector(node, alias) {
				seen[node] = true
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && isRegistrationSelector(sel, alias) {
				called[sel] = true
				registrations[node] = false
			}
		}
		return true
	})

	for sel := range seen {
		if !called[sel] {
			return nil, fmt.Errorf(
				"%s: %s.%s is used as a value at %s; registrations made through it cannot be "+
					"inventoried, so the ledger check would be incomplete",
				filename, alias, sel.Sel.Name, fset.Position(sel.Pos()))
		}
	}

	// Attribute each registration to the variable it ends up in. The call is
	// searched for anywhere inside a ValueSpec's value, not only as its immediate
	// right-hand side, so a registration wrapped in another expression is still
	// found and still belongs to its sentinel.
	found := map[uint32]Registration{}
	var walkErr error

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || walkErr != nil {
			return walkErr == nil
		}
		for i, value := range spec.Values {
			name := "<unnamed>"
			if i < len(spec.Names) {
				name = spec.Names[i].Name
			}
			ast.Inspect(value, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				if _, isRegistration := registrations[call]; !isRegistration {
					return true
				}
				code, err := registrationCode(call, fset)
				if err != nil {
					walkErr = fmt.Errorf("%s: %w", filename, err)
					return false
				}
				if code == nil { // a different codespace; not this ledger's
					registrations[call] = true
					return true
				}
				if previous, duplicate := found[*code]; duplicate {
					walkErr = fmt.Errorf("%s: code %d registered twice: %s and %s",
						filename, *code, previous.Sentinel, name)
					return false
				}
				found[*code] = Registration{Code: *code, Sentinel: name}
				registrations[call] = true
				return true
			})
			if walkErr != nil {
				return false
			}
		}
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// A registration that reached no sentinel still consumes a code, and nothing
	// downstream could pin it. Refuse rather than omit it.
	for call, attributed := range registrations {
		if !attributed {
			return nil, fmt.Errorf(
				"%s: a registration at %s is not assigned to a package-level sentinel, so it "+
					"cannot be pinned", filename, fset.Position(call.Pos()))
		}
	}

	if len(found) == 0 {
		// "This module registers nothing" would make every ledger vacuously pass —
		// the same shape of failure as the two this package exists to prevent.
		return nil, fmt.Errorf(
			"%s registers no errors against ModuleName; the ledger check would be vacuous", filename)
	}
	return found, nil
}

// errorsAlias returns the local name of the cosmossdk.io/errors import.
func errorsAlias(file *ast.File) (string, error) {
	for _, imp := range file.Imports {
		if imp.Path == nil || imp.Path.Value != errorsPackagePath {
			continue
		}
		if imp.Name == nil {
			return "errors", nil // the package's own name
		}
		switch imp.Name.Name {
		case ".":
			// Registrations would appear as bare identifiers indistinguishable from
			// any other call, so the inventory could not be trusted.
			return "", fmt.Errorf("cosmossdk.io/errors is dot-imported; registrations cannot be identified")
		case "_":
			return "", fmt.Errorf("cosmossdk.io/errors is blank-imported; it cannot be used to register")
		}
		return imp.Name.Name, nil
	}
	return "", fmt.Errorf("cosmossdk.io/errors is not imported; this file cannot be the module's error ledger")
}

func isRegistrationSelector(sel *ast.SelectorExpr, alias string) bool {
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != alias {
		return false
	}
	return sel.Sel.Name == "Register" || sel.Sel.Name == "RegisterWithGRPCCode"
}

// registrationCode returns the code a call registers, or nil when the call is
// against a codespace other than this module's.
func registrationCode(call *ast.CallExpr, fset *token.FileSet) (*uint32, error) {
	if len(call.Args) < 2 {
		return nil, fmt.Errorf("registration at %s has too few arguments", fset.Position(call.Pos()))
	}
	codespace, ok := call.Args[0].(*ast.Ident)
	if !ok || codespace.Name != "ModuleName" {
		return nil, nil
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return nil, fmt.Errorf("registration at %s has a non-literal code, which cannot be pinned",
			fset.Position(call.Pos()))
	}
	parsed, err := strconv.ParseUint(lit.Value, 0, 32)
	if err != nil {
		return nil, fmt.Errorf("registration at %s has an unparsable code %q: %w",
			fset.Position(call.Pos()), lit.Value, err)
	}
	code := uint32(parsed)
	return &code, nil
}
