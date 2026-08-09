// Command veil is Veil's deployment engine: a state-driven, resumable
// replacement for the old scripts/*.sh orchestration. Run `veil` with
// no arguments, or any subcommand with -h, for usage.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"veil/internal/deploy"
)

// version is set at build time via -ldflags "-X main.version=...". It
// defaults to "dev" for local `go run`/`go build` invocations.
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil {
		if se, ok := err.(reportable); ok {
			fmt.Fprintln(os.Stderr, se.Report())
		} else {
			fmt.Fprintf(os.Stderr, "veil: %v\n", err)
		}
		os.Exit(1)
	}
}

type reportable interface {
	Report() string
}

// globalFlags are accepted before or interspersed with the
// subcommand; every subcommand parses its own flag.FlagSet seeded
// with these defaults so `veil -network coston2 deploy all` and
// `veil deploy all -network coston2` both work.
type globalFlags struct {
	network string
	dryRun  bool
	verbose bool
	debug   bool
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "-h", "--help", "help":
		printUsage()
		return nil
	case "version":
		fmt.Println("veil " + version)
		return nil
	case "deploy":
		return cmdDeploy(ctx, rest)
	case "rollback":
		return cmdRollback(ctx, rest)
	case "status":
		return cmdStatus(ctx, rest)
	case "doctor":
		return cmdDoctor(ctx, rest)
	case "explain":
		return cmdExplain(ctx, rest)
	case "clean":
		return cmdClean(ctx, rest)
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printUsage() {
	fmt.Println(`veil — Veil's deployment engine

Usage:
  veil deploy all [-network NAME] [-dry-run] [-verbose] [-debug]
  veil deploy <stage> [-network NAME] [-dry-run] [-force]
  veil deploy resume [-network NAME]
  veil rollback [-network NAME]
  veil status [-network NAME]
  veil doctor [-network NAME]
  veil explain [stage]
  veil clean [-network NAME]
  veil version

Stages (in dependency order): validate, contracts, extension, machine, frontend, smoke`)
}

// newFlagSet builds a FlagSet pre-registered with the flags common to
// every subcommand.
func newFlagSet(name string) (*flag.FlagSet, *globalFlags) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	g := &globalFlags{}
	fs.StringVar(&g.network, "network", "local", "network config to use (configs/<network>.yaml)")
	fs.BoolVar(&g.dryRun, "dry-run", false, "validate every stage and print the plan without executing anything")
	fs.BoolVar(&g.verbose, "verbose", false, "verbose console output")
	fs.BoolVar(&g.debug, "debug", false, "debug console output (implies -verbose)")
	return fs, g
}

func projectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// Walk upward looking for go.mod so `veil` works from any
	// subdirectory of the checkout, the same way `go` commands do.
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return wd, nil // fall back to cwd; validate stage will explain what's missing
		}
		dir = parent
	}
}

func buildRunContext(ctx context.Context, g *globalFlags, stageForLog string) (*deploy.RunContext, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, err
	}

	cfg, err := deploy.LoadConfig(root, g.network)
	if err != nil {
		return nil, fmt.Errorf("load config: %w (create configs/%s.yaml, or copy configs/local.yaml as a starting point)", err, g.network)
	}

	state, err := deploy.LoadState(root, g.network)
	if err != nil {
		return nil, fmt.Errorf("load deployment state: %w", err)
	}
	if state.Network != "" && state.Network != g.network {
		return nil, fmt.Errorf("deployment/deployment.json was created for network %q, but -network %q was requested; use veil clean to start over on a different network", state.Network, g.network)
	}
	state.Network = g.network

	level := deploy.LevelInfo
	if g.debug {
		level = deploy.LevelDebug
	} else if g.verbose {
		level = deploy.LevelVerbose
	}
	logger, err := deploy.NewLogger(root, stageForLog, level)
	if err != nil {
		return nil, err
	}

	return &deploy.RunContext{
		ProjectRoot: root,
		Config:      cfg,
		State:       state,
		Logger:      logger,
		DryRun:      g.dryRun,
		Verbose:     g.verbose,
		Debug:       g.debug,
	}, nil
}

func buildEngine(rc *deploy.RunContext) (*deploy.Engine, error) {
	return deploy.NewEngine(rc, []deploy.Stage{
		deploy.ValidateStage{},
		deploy.ContractsStage{},
		deploy.ExtensionStage{},
		deploy.MachineStage{},
		deploy.FrontendStage{},
		deploy.SmokeStage{},
	})
}

// --- deploy ---

func cmdDeploy(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: veil deploy <all|resume|validate|contracts|extension|machine|frontend|smoke> [flags]")
	}
	target := args[0]
	rest := args[1:]

	fs, g := newFlagSet("deploy " + target)
	force := fs.Bool("force", false, "re-run this stage even if already completed")
	_ = fs.Parse(rest)

	rc, err := buildRunContext(ctx, g, target)
	if err != nil {
		return err
	}
	defer rc.Logger.Close()

	rc.State.GitCommit = firstNonEmpty(rc.State.GitCommit, deploy.GitCommit(ctx, rc.ProjectRoot))

	engine, err := buildEngine(rc)
	if err != nil {
		return err
	}

	var outcomes []deploy.StageOutcome
	switch target {
	case "all":
		outcomes, err = engine.Run(ctx, engine.AllStageNames(), *force)
	case "resume":
		outcomes, err = deploy.Resume(ctx, engine)
		if err == deploy.ErrAlreadyComplete {
			fmt.Println("deployment already complete — nothing to resume")
			return nil
		}
	default:
		if _, ok := engine.Stage(target); !ok {
			return fmt.Errorf("unknown stage %q (valid: %s)", target, strings.Join(engine.AllStageNames(), ", "))
		}
		outcomes, err = engine.Run(ctx, []string{target}, *force)
	}

	printOutcomes(outcomes)
	if err != nil {
		return err
	}
	if !rc.DryRun {
		fmt.Println("\ndeployment state: " + deploy.StatePath(rc.ProjectRoot))
	}
	return nil
}

func printOutcomes(outcomes []deploy.StageOutcome) {
	for _, o := range outcomes {
		switch {
		case o.Err != nil:
			fmt.Printf("✗ %-12s FAILED (%s)\n", o.Name, o.Elapsed.Round(time.Millisecond))
		case o.Skipped:
			fmt.Printf("• %-12s skipped\n", o.Name)
		default:
			fmt.Printf("✓ %-12s completed (%s)\n", o.Name, o.Elapsed.Round(time.Millisecond))
		}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- rollback ---

func cmdRollback(ctx context.Context, args []string) error {
	fs, g := newFlagSet("rollback")
	_ = fs.Parse(args)

	rc, err := buildRunContext(ctx, g, "rollback")
	if err != nil {
		return err
	}
	defer rc.Logger.Close()

	engine, err := buildEngine(rc)
	if err != nil {
		return err
	}

	outcomes, err := deploy.Rollback(ctx, engine)
	for _, o := range outcomes {
		switch {
		case o.RolledBack:
			fmt.Printf("✓ %-12s rolled back\n", o.Name)
		case o.Unsupported:
			fmt.Printf("✗ %-12s cannot be rolled back: %s\n", o.Name, o.Reason)
		case o.Err != nil:
			fmt.Printf("✗ %-12s rollback failed: %v\n", o.Name, o.Err)
		}
	}
	return err
}

// --- status ---

func cmdStatus(ctx context.Context, args []string) error {
	fs, g := newFlagSet("status")
	_ = fs.Parse(args)

	rc, err := buildRunContext(ctx, g, "status")
	if err != nil {
		return err
	}
	defer rc.Logger.Close()

	engine, err := buildEngine(rc)
	if err != nil {
		return err
	}

	fmt.Printf("network:  %s\n", rc.State.Network)
	fmt.Printf("status:   %s\n", rc.State.Status)
	if rc.State.GitCommit != "" {
		fmt.Printf("commit:   %s\n", rc.State.GitCommit)
	}
	fmt.Printf("updated:  %s\n\n", rc.State.UpdatedAt.Format(time.RFC3339))

	for _, name := range engine.AllStageNames() {
		st, ok := rc.State.Stages[name]
		status := deploy.StagePending
		if ok {
			status = st.Status
		}
		fmt.Printf("  %-12s %s\n", name, status)
		if ok {
			for _, k := range sortedKeys(st.Outputs) {
				fmt.Printf("      %s = %s\n", k, st.Outputs[k])
			}
			if st.Error != "" {
				fmt.Printf("      error: %s\n", st.Error)
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- doctor ---

func cmdDoctor(ctx context.Context, args []string) error {
	fs, g := newFlagSet("doctor")
	_ = fs.Parse(args)

	rc, err := buildRunContext(ctx, g, "doctor")
	if err != nil {
		// doctor should still report what IT can even if config
		// loading failed — most commonly, this is exactly what
		// doctor is for.
		fmt.Printf("✗ config       %v\n", err)
		rc = &deploy.RunContext{ProjectRoot: mustProjectRoot(), Logger: mustNullLogger()}
	}
	defer rc.Logger.Close()

	failed := false
	for _, c := range deploy.RunDoctor(ctx, rc) {
		symbol := "✓"
		switch c.Status {
		case deploy.CheckWarn:
			symbol = "!"
		case deploy.CheckFail:
			symbol = "✗"
			failed = true
		}
		fmt.Printf("%s %-18s %s\n", symbol, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Printf("    fix: %s\n", c.Fix)
		}
	}
	if failed {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

func mustProjectRoot() string {
	root, err := projectRoot()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return root
}

func mustNullLogger() *deploy.Logger {
	root := mustProjectRoot()
	l, err := deploy.NewLogger(root, "doctor", deploy.LevelInfo)
	if err != nil {
		// Fall back to a logger writing only under os.TempDir so
		// `veil doctor` never fails purely because the project
		// directory itself is unwritable.
		l, _ = deploy.NewLogger(os.TempDir(), "doctor", deploy.LevelInfo)
	}
	return l
}

// --- explain ---

func cmdExplain(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println("Stages (in dependency order): validate, contracts, extension, machine, frontend, smoke")
		fmt.Println("Run `veil explain <stage>` for details on one of them.")
		return nil
	}
	e, ok := deploy.Explain(args[0])
	if !ok {
		return fmt.Errorf("no such stage %q", args[0])
	}
	fmt.Printf("%s\n\n", e.Stage)
	fmt.Printf("Purpose\n  %s\n\n", e.Purpose)
	printList("Consumes", e.Consumes)
	printList("Produces", e.Produces)
	printList("Required before", e.RequiredBefore)
	printList("Common failures", e.CommonFailures)
	printList("Related stages", e.RelatedStages)
	return nil
}

func printList(title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Println(title)
	for _, it := range items {
		fmt.Printf("  - %s\n", it)
	}
	fmt.Println()
}

// --- clean ---

func cmdClean(ctx context.Context, args []string) error {
	fs, g := newFlagSet("clean")
	all := fs.Bool("all", false, "also remove deployment/logs/")
	_ = fs.Parse(args)

	root, err := projectRoot()
	if err != nil {
		return err
	}

	statePath := deploy.StatePath(root)
	if err := removeIfExists(statePath); err != nil {
		return err
	}
	fmt.Println("removed " + statePath)

	registerTeeState := filepath.Join(root, "deployment", "register-tee.state")
	if err := removeIfExists(registerTeeState); err != nil {
		return err
	}

	if *all {
		logsDir := deploy.LogsDir(root)
		if err := os.RemoveAll(logsDir); err != nil {
			return err
		}
		fmt.Println("removed " + logsDir)
	}

	_ = g.network // clean is network-agnostic; flag kept for CLI consistency
	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
