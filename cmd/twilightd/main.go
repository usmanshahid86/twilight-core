package main

import (
	"fmt"
	"os"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"

	"github.com/nyks/nyks-core/app"
	"github.com/nyks/nyks-core/cmd/nyksd/cmd"
)

func main() {
	root := cmd.NewRootCmd()
	if err := svrcmd.Execute(root, "NYKS", app.DefaultNodeHome); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		os.Exit(1)
	}
}
