package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/flow"
	"github.com/edihasaj/vmlab/internal/project"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/spf13/cobra"
)

func newVerifyCmd() *cobra.Command {
	var (
		dryRun   bool
		asJSON   bool
		format   string
		logPath  string
		listOnly bool
	)
	c := &cobra.Command{
		Use:   "verify [project]",
		Short: "Run a project's saved verification flow against its target",
		Long: `Resolve a project profile and run its flow against its target — so you
don't have to remember which flow/target pairs with which repo.

Profiles live in ~/.vmlab/projects/<name>.yaml (or .vmlab/projects/ in a repo):

  name: dayshape
  path: ~/Projects/dayshape/dayshape   # cwd at/under this auto-selects it
  target: win11-ssh                    # any run selector
  flow: ~/Projects/agent-scripts/vmlab/flows/dayshape-win-verify.yaml

Resolution: an explicit [project] name wins; otherwise the profile whose path
is the deepest ancestor of the working directory is used.

Examples:
  vmlab verify                 # auto-detect from the current directory
  vmlab verify dayshape        # by name, from anywhere
  vmlab verify --list          # show configured projects
  vmlab verify --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(p); err != nil {
				return err
			}
			profiles, err := project.Load(p)
			if err != nil {
				return err
			}

			if listOnly {
				return listProjects(cmd.OutOrStdout(), profiles)
			}
			if len(profiles) == 0 {
				return fmt.Errorf("no project profiles found — add one at %s/projects/<name>.yaml", p.UserDir)
			}

			pr, err := resolveProfile(profiles, args)
			if err != nil {
				return err
			}
			if pr.Target == "" || pr.Flow == "" {
				return fmt.Errorf("project %q must set both 'target' and 'flow' (%s)", pr.Name, pr.SourceFile)
			}

			if logPath != "" {
				lf, err := os.Create(logPath)
				if err != nil {
					return fmt.Errorf("--log: %w", err)
				}
				defer lf.Close()
				cmd.SetOut(io.MultiWriter(cmd.OutOrStdout(), lf))
				cmd.SetErr(io.MultiWriter(cmd.ErrOrStderr(), lf))
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "verify %s → target %s, flow %s\n", pr.Name, pr.Target, pr.ExpandedFlow())

			r, err := target.Load(p)
			if err != nil {
				return err
			}
			ts, err := target.NewSelector(pr.Target).Resolve(r)
			if err != nil {
				return err
			}
			if len(ts) == 0 {
				return fmt.Errorf("project %q: target selector %q matched no targets", pr.Name, pr.Target)
			}

			loadedFlow, err := flow.Load(pr.ExpandedFlow())
			if err != nil {
				return fmt.Errorf("project %q flow: %w", pr.Name, err)
			}

			return executeTargetRun(cmd, p, cfg, pr.Target, ts, loadedFlow, "", runFlags{
				asJSON: asJSON,
				dryRun: dryRun,
				format: format,
			})
		},
	}
	c.Flags().BoolVar(&listOnly, "list", false, "list configured project profiles and exit")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan (target + steps) without executing")
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON summary at the end")
	c.Flags().StringVar(&format, "format", "", "compact output: matrix")
	c.Flags().StringVar(&logPath, "log", "", "tee merged stdout+stderr to a file")
	return c
}

// resolveProfile picks the profile named in args, or auto-detects from the cwd.
func resolveProfile(profiles []project.Profile, args []string) (project.Profile, error) {
	if len(args) == 1 {
		pr, ok := project.ByName(profiles, args[0])
		if !ok {
			return project.Profile{}, fmt.Errorf("no project profile named %q (try `vmlab verify --list`)", args[0])
		}
		return pr, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return project.Profile{}, err
	}
	pr, ok := project.Detect(profiles, cwd)
	if !ok {
		return project.Profile{}, fmt.Errorf("no project profile matches %s — pass a name or set 'path:' on a profile (try `vmlab verify --list`)", cwd)
	}
	return pr, nil
}

func listProjects(w io.Writer, profiles []project.Profile) error {
	if len(profiles) == 0 {
		fmt.Fprintln(w, "no project profiles configured")
		return nil
	}
	sorted := append([]project.Profile(nil), profiles...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTARGET\tPATH\tFLOW")
	for _, pr := range sorted {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", pr.Name, pr.Target, collapseHome(pr.Path), collapseHome(pr.Flow))
	}
	return tw.Flush()
}

// collapseHome is the inverse of ~ expansion, for tidy listings.
func collapseHome(s string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(s, home) {
		return "~" + s[len(home):]
	}
	return s
}
