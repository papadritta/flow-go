package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dapperlabs/bamboo-node/internal/cli/emulator"
	"github.com/dapperlabs/bamboo-node/internal/cli/initialize"
)

var cmd = &cobra.Command{
	Use:              "bamboo",
	TraverseChildren: true,
}

func init() {
	cmd.AddCommand(initialize.Cmd)
	cmd.AddCommand(emulator.Cmd)
}

func Execute() {
	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
