package errorledger_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/internal/errorledger"
)

// The fixtures are .go.txt rather than .go so the toolchain does not try to
// build them: several are deliberately shapes we refuse, and one imports a
// package under a dot-import purely to be rejected.
func fixture(name string) string { return filepath.Join("testdata", name+".go.txt") }

// The shapes that must be inventoried. Each is a legitimate way to register an
// error, and a parser that misses any of them reports a partial set as success —
// which is the failure this package exists to prevent, and which it has now
// exhibited twice.
func TestRegisteredFindsEveryRegistrationShape(t *testing.T) {
	for name, want := range map[string]map[uint32]string{
		"direct":    {2: "ErrOne", 3: "ErrTwo"},
		"multiline": {2: "ErrOne", 3: "ErrWrapped"},
		"grpccode":  {2: "ErrOne", 3: "ErrGRPC"},
		// Registers for real while the immediate callee is another function. The
		// first parser skipped it silently and the non-empty guard still passed,
		// because ErrOne kept the inventory populated.
		"nested": {2: "ErrOne", 3: "ErrNested"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := errorledger.Registered(fixture(name))
			require.NoError(t, err)
			require.Len(t, got, len(want))
			for code, sentinel := range want {
				require.Contains(t, got, code)
				require.Equal(t, sentinel, got[code].Sentinel)
				require.Equal(t, code, got[code].Code)
			}
		})
	}
}

// Everything this parser cannot account for must be an ERROR, never an omission.
//
// The distinction is the whole design. A partial inventory returned as success
// makes every downstream completeness check agree with a subset of reality, and
// the resulting green is indistinguishable from a real one.
func TestRegisteredFailsClosed(t *testing.T) {
	for name, wantErr := range map[string]string{
		// The registration function escapes into a value, after which calls happen
		// through a name that cannot be followed back to the package.
		"aliased": "used as a value",
		// A computed code cannot be pinned to a number.
		"nonliteral": "non-literal code",
		// Two sentinels claiming one code: the SDK would panic at init, but the
		// inventory must not silently keep whichever it saw last.
		"duplicate": "registered twice",
		// Imported, but nothing registered against this module's codespace.
		"empty": "registers no errors",
		// Bare identifiers indistinguishable from any other call.
		"dotimport": "dot-imported",
		// Consumes a code but reaches no sentinel, so nothing downstream can pin it.
		"unattributed": "not assigned to a package-level sentinel",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := errorledger.Registered(fixture(name))
			require.Error(t, err, "this shape must be refused, not silently skipped")
			require.Nil(t, got, "a refused parse must return no inventory to be mistaken for a complete one")
			require.Contains(t, err.Error(), wantErr)
		})
	}
}

func TestRegisteredRejectsAnUnreadableFile(t *testing.T) {
	got, err := errorledger.Registered(fixture("does-not-exist"))
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "parsing")
}

// The real ledgers, so a change to any module's error file that this parser
// cannot read is caught here rather than as a confusing failure in that module's
// own ledger test.
func TestRegisteredReadsEveryModuleErrorFile(t *testing.T) {
	for module, wantCount := range map[string]int{
		"coreslot": 21,
		"rewards":  9,
		"mining":   6,
	} {
		t.Run(module, func(t *testing.T) {
			got, err := errorledger.Registered(
				filepath.Join("..", "..", "x", module, "types", "errors.go"))
			require.NoError(t, err)
			require.Len(t, got, wantCount)

			// Every sentinel is named. An inventory of anonymous codes would satisfy
			// containment while telling an author nothing about what to add.
			for code, reg := range got {
				require.NotEmpty(t, reg.Sentinel, "code %d has no sentinel name", code)
				require.NotEqual(t, "<unnamed>", reg.Sentinel, "code %d is unattributed", code)
			}
		})
	}
}
