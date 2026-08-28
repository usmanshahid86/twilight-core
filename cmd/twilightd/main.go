package main

import (
	"fmt"
	"os"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
	sdkversion "github.com/cosmos/cosmos-sdk/version"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/cmd/twilightd/cmd"
)

// The chain and binary names are compiled in rather than injected at link time.
// They never vary between builds, and leaving them to -ldflags means a binary
// built with a plain `go build` — which is what CI and the localnet scripts do —
// reports an empty name and the SDK's `<appd>` placeholder, so it cannot say what
// it is. Version and Commit DO vary per build and are stamped by the Makefile;
// an unstamped build reports them empty, which is honest: it was not released.
func init() {
	sdkversion.Name = app.Name
	sdkversion.AppName = "twilightd"
}

func main() {
	root := cmd.NewRootCmd()
	if err := svrcmd.Execute(root, "TWILIGHT", app.DefaultNodeHome); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), err)
		os.Exit(1)
	}
}
