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
	"time"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/envfile"
	"github.com/marleypowell/footprint/internal/logx"
)

const usage = `footprint collects raw data about one person's work across an org: commits, pull
requests, issues and wiki pages.

Usage:
  footprint init --org <org>          write the config for the authenticated user
  footprint [flags] <stage>...        run stages in the order given
  footprint [flags] all               run every stage, then doctor
  footprint [flags] doctor            say whether the dataset is complete, and what to run or
                                      change when it is not (exits 1 when it is not)
  footprint [flags] facts             decide what counts, from the collected data -> <facts>/
  footprint [flags] check <file>...   check a note or draft against the facts
  footprint [flags] count             count what the facts count, for a window, repo or item
  footprint [flags] search <term>...  find the records that mention every term

Stages, in the order "all" runs them:
  inventory   every repo in the org, with code host metadata  -> <data>/org/repos.json
  mirror      bare clone/fetch of every non-empty repo         -> <cache>/mirrors/<repo>.git
              (branches, tags and change-request heads, so commits from squash-merged
              PRs with deleted branches are still reachable)
  identities  every identity resembling the user, with counts  -> <data>/org/identities.json
  prs         pull requests authored by the user               -> <data>/repos/<repo>/prs.jsonl
  reviews     other people's pull requests the user reviewed   -> <data>/repos/<repo>/reviews.jsonl,
              or commented on, with only the user's part          <data>/org/reviews.json
  commits     the user's commits on any ref (author, committer, co-author, or in one of
              the user's PRs), full message + per-file stats   -> <data>/repos/<repo>/commits.jsonl
  catalog     every catalog entity on every default branch     -> <data>/catalog/
  ownership   who wrote the lines the default branch has     -> <data>/repos/<repo>/ownership.json
              now, in the files the user ever changed (git blame)
  projects    per-repo "what is this" bundle, the decision     -> <data>/repos/<repo>/project.json,
              records (ADRs) the user committed to, the index     decisions.jsonl, <data>/index.json
  verify      cross-check against the host's attribution       -> <data>/org/verification.json
  issues      issues ever assigned to the user, or named in their commits and PRs, with
              comments, change history and worklogs, and the issues
              above them (stories, epics)                      -> <data>/issues/<project>.jsonl
  pages       wiki pages, blog posts etc. the user created or edited, with versions
              and the body as Markdown                         -> <data>/wiki/<space>.jsonl

doctor reads the dataset, the mirror state and the config (if any), and the names of the variables
the tools authenticate with (never their values): verify (commits the code host credits to the
user that the dataset lacks, with the emails to add), identities (variants that may be the user;
identity.not_me dismisses the rest), issues (keys that failed to load), pages, mirror, stale
(output older than what it was made from) and env.

facts, check, count and search read only the collected data, the notes and the context (no config
or tokens):
  facts       work items, changes, issues, docs, repos and totals, with an evidence pack per
              item and the notes still to write; -from/-to also write that window's view
              (both days included)                             -> <facts>/, <facts>/windows/
  check       every [[citation]] must name a record (in a note: its item's), and every number
              should be a fact in scope or the user's own; -plain or -links also write
              <file>.final.md, with the citations removed or turned into links
  count       the totals facts.json reports, for the events in -from..-to, -repo and -item
              (an initiative's include its features'), or with -tech those technologies.jsonl
              reports for a technology, broken down -by year, quarter, repo or item; JSON on
              stdout
  search      the items, issues, changes, docs, reviews and ownership areas whose ids, title
              and text (an item's note, a change's description, a doc's body, an area's path,
              entity, role and colleagues) have every term, ignoring case, narrowed by
              -from..-to, -repo, -item and -kind; one per line on stdout: kind, id, item,
              first..last, title, link (tab-separated)

Several employers (data/careers.json, README.md: Several employers):
  datasets    the datasets data/careers.json lists, the jobs by when you started, then personal ones: id, employer, months,
              what is on disk (full, facts, context), frozen, directory (tab-separated)
  -dataset <id> runs any command on that dataset: it sets -config, -data, -cache, -env,
              -facts, -notes and -context, unless given; collecting into a frozen one, or rebuilding
              its facts, is refused, and check reads its stored facts once its raw data is gone
  freeze      -dataset <id>: keep a past job's facts as its record: build them a last time, hash
              them into <facts>/frozen.json, and mark it frozen in the careers file; -drop-packs
              and -drop-raw then remove the packs or the raw data

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
	flags.StringVar(&opts.Org, "org", "", "init: the org to collect from (a GitHub organization, or your user account for a personal dataset)")
	flags.StringVar(&opts.CodeHost, "code-host", "github", "init: the code host adapter")
	flags.BoolVar(&opts.Force, "force", false, "init: overwrite an existing config file")
	var fo factsOptions
	flags.StringVar(&fo.dir, "facts", "data/facts", "facts: the output directory")
	flags.StringVar(&fo.notes, "notes", "data/notes", "facts, check: the notes on work items, one <id>.md each")
	flags.StringVar(&fo.context, "context", "data/context", "facts, check: the user's own words (profile, framework, items/<id>.md)")
	flags.StringVar(&fo.from, "from", "", "facts: also write the window from this day (YYYY-MM-DD); count, search: look from this day")
	flags.StringVar(&fo.to, "to", "", "facts, count, search: ... to this day, included")
	flags.StringVar(&fo.since, "since", "", "facts: also write recent/: the work items to ask the user about since this day (YYYY-MM-DD)")
	flags.BoolVar(&fo.plain, "plain", false, "check: also write <file>.final.md without the citations")
	flags.BoolVar(&fo.links, "links", false, "check: also write <file>.final.md with the citations as links")
	flags.StringVar(&fo.repo, "repo", "", "count, search: only what is in this repo")
	flags.StringVar(&fo.item, "item", "", "count, search: only what counts toward this work item (an initiative's include its features')")
	flags.StringVar(&fo.tech, "tech", "", "count: a technology's totals (by name or alias: TS, Golang) instead of the user's")
	flags.StringVar(&fo.by, "by", "", "count: also break the totals down by year, quarter, repo or item")
	flags.StringVar(&fo.kind, "kind", "", "search: only these kinds of record, comma-separated: item, issue, change, doc, review, area")
	var careersPath, datasetID string
	flags.StringVar(&careersPath, "careers", "data/careers.json", "the datasets: one per job, and personal projects (README.md: Several employers)")
	flags.StringVar(&datasetID, "dataset", "", "run on this dataset, from the careers file: it sets the path flags not given")
	var fz freezeOptions
	flags.BoolVar(&fz.dropRaw, "drop-raw", false, "freeze: then remove the raw data, keeping the facts alone")
	flags.BoolVar(&fz.dropPacks, "drop-packs", false, "freeze: then remove the packs (notes can no longer be revised)")
	var doctorJSON bool
	flags.BoolVar(&doctorJSON, "json", false, "doctor: write the report as JSON")
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

	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	emp, err := applyDataset(careersPath, datasetID, set, &opts, &fo, &envPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
		os.Exit(1)
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
			if err = refuseFrozen(careersPath, emp, opts.DataDir); err == nil {
				err = runFacts(opts.DataDir, fo)
			}
		}
	case "freeze":
		err = errors.New("run freeze on its own")
		if len(stages) == 1 {
			err = runFreeze(careersPath, emp, opts, fo, fz, time.Now())
		}
	case "check":
		err = runCheck(opts.DataDir, fo, stages[1:])
	case "count":
		err = runCount(opts.DataDir, fo, stages[1:])
	case "search":
		err = runSearch(opts.DataDir, fo, stages[1:])
	case "datasets":
		err = errors.New("run datasets on its own")
		if len(stages) == 1 {
			err = runDatasets(os.Stdout, careersPath)
		}
	case "doctor":
		err = errors.New("run doctor on its own")
		if len(stages) == 1 {
			err = runDoctor(opts, envPath, doctorJSON)
		}
	default:
		if err = refuseFrozen(careersPath, emp, opts.DataDir); err != nil {
			break
		}
		err = run(ctx, opts, stages)
		if err == nil && slices.Contains(stages, "all") {
			logx.Printf("=== doctor")
			err = runDoctor(opts, envPath, false)
		}
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
