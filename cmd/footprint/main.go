// Command footprint collects raw data about one person's work across an org's repositories.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/marleypowell/footprint/internal/scrape"
)

const usage = `footprint collects raw data about one person's work across an org's repositories.

Usage:
  footprint init --org <org>          write the config for the authenticated user
  footprint [flags] <stage>...        run stages in the order given
  footprint [flags] all               run every stage

Stages, in the order "all" runs them:
  inventory   every repo in the org, with code host metadata  -> <data>/org/repos.json
  mirror      bare clone/fetch of every non-empty repo         -> <cache>/mirrors/<repo>.git
              (branches, tags and change-request heads, so commits from squash-merged
              PRs with deleted branches are still reachable)
  identities  every identity resembling the user, with counts  -> <data>/org/identities.json
  prs         pull requests authored by the user               -> <data>/repos/<repo>/prs.jsonl
  commits     the user's commits on any ref (author, committer, co-author, or in one of
              the user's PRs), full message + per-file stats   -> <data>/repos/<repo>/commits.jsonl
  catalog     every catalog entity on every default branch     -> <data>/catalog/
  projects    per-repo "what is this" bundle + the index       -> <data>/repos/<repo>/project.json,
                                                                  <data>/index.json
  verify      cross-check against the host's attribution       -> <data>/org/verification.json

The code host, catalog and issue tracker are set in the config (code_host, catalog,
issue_tracker); supported: github, backstage, jira.

Flags (anywhere on the command line):
`

func main() {
	flags := flag.NewFlagSet("footprint", flag.ExitOnError)
	var opts scrape.Options
	flags.StringVar(&opts.ConfigPath, "config", "config.json", "personal config file (identity, org)")
	flags.StringVar(&opts.DataDir, "data", "data/raw", "output directory")
	flags.StringVar(&opts.CacheDir, "cache", "cache", "git mirrors and run state")
	flags.StringVar(&opts.Only, "only", "", "comma-separated repo names to limit the mirror and commits stages to")
	flags.StringVar(&opts.Org, "org", "", "init: the org (GitHub) or group (GitLab) to collect from")
	flags.StringVar(&opts.CodeHost, "code-host", "github", "init: the code host adapter")
	flags.BoolVar(&opts.Force, "force", false, "init: overwrite an existing config file")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}

	// The flag package stops at the first positional argument; keep parsing so flags can follow
	// stage names (`footprint commits --only pay`).
	var stages []string
	for args := os.Args[1:]; ; {
		_ = flags.Parse(args) // ExitOnError
		if flags.NArg() == 0 {
			break
		}
		stages = append(stages, flags.Arg(0))
		args = flags.Args()[1:]
	}
	if len(stages) == 0 {
		flags.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, opts, stages)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts scrape.Options, stages []string) error {
	if slices.Contains(stages, "init") {
		if len(stages) > 1 {
			return errors.New("run init on its own, review the config it writes, then run the stages")
		}
		if opts.Org == "" {
			return errors.New("init needs --org <org>")
		}
		host, err := newCodeHost(opts.CodeHost, opts.Org, nil)
		if err != nil {
			return err
		}
		return scrape.Init(ctx, opts, host)
	}
	stages, err := scrape.Stages(stages)
	if err != nil {
		return err
	}
	cfg, err := scrape.LoadConfig(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s not found: run `footprint init --org <org>` first", opts.ConfigPath)
	}
	if err != nil {
		return err
	}
	tools, err := newTools(cfg)
	if err != nil {
		return err
	}
	return scrape.Run(ctx, opts, cfg, tools, stages)
}
