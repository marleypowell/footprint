// Command footprint collects raw data about one person's work across a GitHub org.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/marleypowell/footprint/internal/scrape"
)

const usage = `footprint collects raw data about one person's work across a GitHub org.

Usage:
  footprint init --org <github-org>   write the config for the gh-authenticated user
  footprint [flags] <stage>...        run stages in the order given
  footprint [flags] all               run every stage

Stages, in the order "all" runs them:
  inventory   every repo in the org with GitHub metadata      -> <data>/org/repos.json
  mirror      bare clone/fetch of every non-empty repo         -> <cache>/mirrors/<repo>.git
              (branches, tags and refs/pull/*/head, so commits from squash-merged PRs
              with deleted branches are still reachable)
  identities  every identity resembling the user, with counts  -> <data>/org/identities.json
  prs         pull requests authored by the user               -> <data>/repos/<repo>/prs.jsonl
  commits     the user's commits on any ref (author, committer, co-author, or in one of
              the user's PRs), full message + per-file stats   -> <data>/repos/<repo>/commits.jsonl
  catalog     every Backstage entity on every default branch   -> <data>/backstage/
  projects    per-repo "what is this" bundle + the index       -> <data>/repos/<repo>/project.json,
                                                                  <data>/index.json
  verify      cross-check against GitHub's commit attribution  -> <data>/org/verification.json

Flags (anywhere on the command line):
`

func main() {
	fs := flag.NewFlagSet("footprint", flag.ExitOnError)
	var opts scrape.Options
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "personal config file (identity, org)")
	fs.StringVar(&opts.DataDir, "data", "data/raw", "output directory")
	fs.StringVar(&opts.CacheDir, "cache", "cache", "git mirrors and run state")
	fs.StringVar(&opts.Only, "only", "", "comma-separated repo names to limit the mirror and commits stages to")
	fs.StringVar(&opts.Org, "org", "", "init: the GitHub org to collect from")
	fs.BoolVar(&opts.Force, "force", false, "init: overwrite an existing config file")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}

	// The flag package stops at the first positional argument; keep parsing so flags can follow
	// stage names (`footprint commits --only pay`).
	var stages []string
	for args := os.Args[1:]; ; {
		_ = fs.Parse(args) // ExitOnError
		if fs.NArg() == 0 {
			break
		}
		stages = append(stages, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(stages) == 0 {
		fs.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := scrape.Run(ctx, opts, stages)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
		os.Exit(1)
	}
}
