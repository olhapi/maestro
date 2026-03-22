package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/olhapi/maestro/skills"
)

func (a *cliApp) newInstallCmd() *cobra.Command {
	var skillsOnly bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the bundled Maestro skill",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !skillsOnly {
				_ = cmd.Help()
				return usageErrorf("specify --skills to install the Maestro skill bundle")
			}

			homeDir, err := os.UserHomeDir()
			if err != nil {
				return wrapRuntime(err, "resolve home directory")
			}

			targets := []string{
				filepath.Join(homeDir, ".agents", "skills", "maestro"),
				filepath.Join(homeDir, ".claude", "skills", "maestro"),
			}

			for _, target := range targets {
				if err := skills.InstallMaestro(target); err != nil {
					return wrapRuntime(err, "failed to install Maestro skill into %s", target)
				}
			}

			_, _ = fmt.Fprintln(a.stdout, "Installed Maestro skill bundle:")
			for _, target := range targets {
				_, _ = fmt.Fprintf(a.stdout, " - %s\n", target)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&skillsOnly, "skills", false, "Install the bundled Maestro skill into Codex and Claude Code")
	return cmd
}
