// Package errorledger enumerates the error codes a module registers, by parsing
// its source.
//
// It exists so that a ledger test can check its table against the REGISTRATIONS
// rather than against itself. A test that asserts "the table has 8 rows" and
// "codes 2 through 9 are present" proves only that the table agrees with facts
// hard-coded from the same table — it cannot notice a sentinel that was
// registered and never listed.
//
// That is not hypothetical. The first version of these ledgers was built by
// grepping for `errorsmod.Register(ModuleName, N` on a single line, which missed
// x/rewards code 10 because its call wraps its arguments onto the next line. The
// table and its expected length were derived from the same faulty extraction, so
// the count check passed and one live code went unpinned.
//
// Parsing the AST is what closes that: it is indifferent to line breaks,
// alignment and comments, so the set it returns is what the compiler sees rather
// than what a pattern happened to match.
package errorledger

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// Registration is one `errorsmod.Register` call found in a module's errors file.
type Registration struct {
	// Code is the numeric ABCI error code.
	Code uint32
	// Sentinel is the name of the variable the call is assigned to, so a failure
	// can name the error an author forgot rather than only its number.
	Sentinel string
}

// Registered parses filename and returns every code registered against the
// module's own codespace, keyed by code.
//
// Only calls whose codespace argument is the identifier `ModuleName` are
// collected. A registration against some other codespace belongs to a different
// ledger and is deliberately not this module's to pin.
//
// Both Register and RegisterWithGRPCCode are recognized; the code is the second
// argument in each.
func Registered(filename string) (map[uint32]Registration, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	found := map[uint32]Registration{}
	var walkErr error

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, value := range spec.Values {
			call, ok := value.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if sel.Sel.Name != "Register" && sel.Sel.Name != "RegisterWithGRPCCode" {
				continue
			}
			if len(call.Args) < 2 {
				continue
			}
			// Only this module's own codespace.
			codespace, ok := call.Args[0].(*ast.Ident)
			if !ok || codespace.Name != "ModuleName" {
				continue
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				walkErr = fmt.Errorf("registration at %s has a non-literal code", fset.Position(call.Pos()))
				return false
			}
			code, err := strconv.ParseUint(lit.Value, 0, 32)
			if err != nil {
				walkErr = fmt.Errorf("registration at %s has an unparsable code %q: %w",
					fset.Position(call.Pos()), lit.Value, err)
				return false
			}
			name := "<unnamed>"
			if i < len(spec.Names) {
				name = spec.Names[i].Name
			}
			if previous, duplicate := found[uint32(code)]; duplicate {
				walkErr = fmt.Errorf("code %d registered twice: %s and %s", code, previous.Sentinel, name)
				return false
			}
			found[uint32(code)] = Registration{Code: uint32(code), Sentinel: name}
		}
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(found) == 0 {
		// An empty result almost certainly means the file moved or the call shape
		// changed, and silently reporting "nothing is registered" would make every
		// ledger test vacuously pass — the exact failure this package exists to
		// prevent.
		return nil, fmt.Errorf("%s registers no errors against ModuleName; the ledger check would be vacuous", filename)
	}
	return found, nil
}
