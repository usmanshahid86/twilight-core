package main

import (
	"fmt"
	"os"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/cmd/twilightd/cmd"
)

func main() {
	root := cmd.NewRootCmd()
	if err := svrcmd.Execute(root, "TWILIGHT", app.DefaultNodeHome); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		os.Exit(1)
	}
}
