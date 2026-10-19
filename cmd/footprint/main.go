// Command footprint collects raw data about one person's work across an org: commits, pull
// requests, issues and wiki pages.
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
	"strings"
	"syscall"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/envfile"
	"github.com/marleypowell/footprint/internal/logx"
)

const usage = `footprint collects raw data about one person's work across an org: commits, pull
requests, issues and wiki pages.

Usage:
  footprint init --org <org>          write the config for the authenticated user
  footprint [flags] <stage>...        run stages in the order given
  footprint [flags] all               run every stage
  footprint [flags] facts             decide what counts, from the collected data -> <facts>/
  footprint [flags] check <file>...   check a note or draft against the facts

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
  issues      issues ever assigned to the user, or named in their commits and PRs, with
              comments, change history and worklogs, and the issues
              above them (stories, epics)                      -> <data>/issues/<project>.jsonl
  pages       wiki pages, blog posts etc. the user created or edited, with versions
              and the body as Markdown                         -> <data>/wiki/<space>.jsonl

facts and check read only the collected data, the notes and the context (no config or tokens):
  facts       work items, changes, issues, docs, repos and totals, with an evidence pack per
              item and the notes still to write; -from/-to also write that window's view
              (both days included)                             -> <facts>/, <facts>/windows/
  check       every [[citation]] must name a record (in a note: its item's), and every number
              should be a fact in scope or the user's own; -plain or -links also write
              <file>.final.md, with the citations removed or turned into links

The code host, catalog, issue tracker and wiki are set in the config (code_host, catalog,
issue_tracker, wiki; the last two are optional); supported: github, backstage, jira, confluence.
Jira and Confluence authenticate with ATLASSIAN_EMAIL and ATLASSIAN_API_TOKEN, read from the
environment or from the .env file (see .env.example and README.md). With Jira configured, prs,
commits and projects need the token too, to list its projects.

Flags (anywhere on the command line):
`

func main() {
	flags := flag.NewFlagSet("footprint", flag.ExitOnError)
	var opts collect.Options
	var envPath string
	flags.StringVar(&envPath, "env", ".env", "file of KEY=value tokens loaded into the environment (variables already set win)")
	flags.StringVar(&opts.ConfigPath, "config", "config.json", "personal config file (identity, org)")
	flags.StringVar(&opts.DataDir, "data", "data/raw", "output directory")
	flags.StringVar(&opts.CacheDir, "cache", "cache", "git mirrors and run state")
	flags.StringVar(&opts.Only, "only", "", "comma-separated repo names to limit the mirror and commits stages to")
	flags.StringVar(&opts.Org, "org", "", "init: the org to collect from (a GitHub organization)")
	flags.StringVar(&opts.CodeHost, "code-host", "github", "init: the code host adapter")
	flags.BoolVar(&opts.Force, "force", false, "init: overwrite an existing config file")
	var fo factsOptions
	flags.StringVar(&fo.dir, "facts", "data/facts", "facts: the output directory")
	flags.StringVar(&fo.notes, "notes", "data/notes", "facts, check: the notes on work items, one <id>.md each")
	flags.StringVar(&fo.context, "context", "data/context", "facts, check: the user's own words (profile, framework, items/<id>.md)")
	flags.StringVar(&fo.from, "from", "", "facts: also write the window from this day (YYYY-MM-DD)")
	flags.StringVar(&fo.to, "to", "", "facts: ... to this day, included")
	flags.BoolVar(&fo.plain, "plain", false, "check: also write <file>.final.md without the citations")
	flags.BoolVar(&fo.links, "links", false, "check: also write <file>.final.md with the citations as links")
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

	loaded, err := envfile.Load(envPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
		os.Exit(1)
	}
	if len(loaded) > 0 {
		logx.Printf("env: loaded %s from %s", strings.Join(loaded, ", "), envPath)
		if envfile.TooOpen(envPath) {
			logx.Printf("env: WARNING: %s holds tokens and other users can read it; run chmod 600 %s", envPath, envPath)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	switch stages[0] {
	case "facts":
		err = errors.New("run facts on its own")
		if len(stages) == 1 {
			err = runFacts(opts.DataDir, fo)
		}
	case "check":
		err = runCheck(opts.DataDir, fo, stages[1:])
	default:
		err = run(ctx, opts, stages)
	}
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts collect.Options, stages []string) error {
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
		return collect.Init(ctx, opts, host)
	}
	stages, err := collect.Stages(stages)
	if err != nil {
		return err
	}
	cfg, err := collect.LoadConfig(opts.ConfigPath)
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
	return collect.Run(ctx, opts, cfg, tools, stages)
}
