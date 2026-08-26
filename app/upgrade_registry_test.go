package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
)

// A duplicate upgrade name is dangerous asymmetrically, which is why it must be
// refused rather than resolved.
//
// Handler registration is a map assignment, so the LAST entry with a given name
// wins. The store loader scans the slice and takes the FIRST entry with non-nil
// StoreUpgrades. Two entries sharing a name can therefore pair one declaration's
// store layout with another's migration, under a name the operator believes
// identifies a single upgrade — and the mismatch would only appear at the height
// where it is least recoverable.
func TestValidateUpgrades(t *testing.T) {
	migrate := func(sdk.Context, app.MigrationKeepers) error { return nil }

	for _, tc := range []struct {
		name     string
		upgrades []app.Upgrade
		wantErr  string
	}{
		{
			name:     "empty registry is valid",
			upgrades: nil,
		},
		{
			name:     "a single named entry is valid",
			upgrades: []app.Upgrade{{Name: "v0.2.0", Migrate: migrate}},
		},
		{
			name:     "distinct names are valid",
			upgrades: []app.Upgrade{{Name: "v0.2.0"}, {Name: "v0.3.0", Migrate: migrate}},
		},
		{
			// x/upgrade's Plan.ValidateBasic requires a non-empty name, so such an
			// entry can never match a plan: it is unreachable, and its presence means
			// the registry does not say what its author thought it said.
			name:     "an empty name is refused",
			upgrades: []app.Upgrade{{Name: ""}},
			wantErr:  "empty name",
		},
		{
			name:     "an empty name is refused even beside valid entries",
			upgrades: []app.Upgrade{{Name: "v0.2.0"}, {Name: ""}},
			wantErr:  "empty name",
		},
		{
			name: "a duplicate name is refused",
			upgrades: []app.Upgrade{
				{Name: "v0.2.0", StoreUpgrades: &storetypes.StoreUpgrades{Added: []string{"a"}}},
				{Name: "v0.2.0", Migrate: migrate},
			},
			wantErr: "more than once",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := app.ValidateUpgrades(tc.upgrades)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// The registry is compiled in, so a malformed one is a build mistake every node
// carrying that binary shares. Refusing at construction is the cheapest moment to
// say so — the alternative is discovering it at an upgrade height.
func TestMalformedUpgradeRegistryFailsAppConstruction(t *testing.T) {
	withUpgrades(t, []app.Upgrade{{Name: "v0.2.0"}, {Name: "v0.2.0"}})
	require.Panics(t, func() {
		newAppOnDB(t, memDB())
	}, "a duplicate upgrade name must stop the binary from starting")
}

// And the shipped registry must itself be valid.
func TestShippedUpgradeRegistryIsValid(t *testing.T) {
	require.NoError(t, app.ValidateUpgrades(app.Upgrades))
}
