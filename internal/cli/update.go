package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shinokamix/lopper/internal/update"
)

func newUpdateCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Install the latest release of lopper over this one",
		Long: "update downloads the latest release from " + update.Repo + ", checks it " +
			"against the release's checksums and replaces the running lopper binary with it.",
		Args: cobra.NoArgs,
		// Updating needs no git: skip the root's check for it.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !update.Released(version) {
				return fmt.Errorf("this lopper was built from source, not installed from a release; "+
					"install one with install.sh from %s", update.Repo)
			}
			u, err := update.New()
			if err != nil {
				return err
			}
			latest, err := u.Latest(cmd.Context())
			if err != nil {
				return err
			}
			current := "v" + strings.TrimPrefix(version, "v") // goreleaser sets it without
			if !update.Newer(version, latest) {
				fmt.Fprintf(cmd.OutOrStdout(), "lopper %s is the latest release\n", current)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updating lopper %s to %s…\n", current, latest)
			if err := u.Install(cmd.Context(), latest); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s to %s\n", u.Exe, latest)
			return nil
		},
	}
}
