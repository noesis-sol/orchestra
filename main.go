// Command orchestrate works through a Beads backlog one ticket at a time, handing each ticket to a
// coding agent in its own Herdr tab and git worktree.
//
// Each ticket gets branch wt/<ticket>, created from the current branch of the main checkout, in
// $WT_ROOT/<ticket>. Beads resolves to the main checkout's database from inside a worktree, so
// workers see the same tickets. When a ticket closes with a commit and a clean worktree, the branch
// is fast-forwarded into the main checkout's branch and the worktree, branch and tab are removed.
// Anything else keeps the worktree and the tab for review.
//
// Run from anywhere inside the main checkout (not from a worktree), in a Herdr pane:
//
//	WORKSPACE=<herdr workspace id> orchestrate
//
// Every event is shown in the terminal and appended to .claude/orchestrate.log.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type Config struct {
	Repo         string
	Base         string // branch finished tickets are merged into
	Workspace    string
	Limit        int
	DoneSoFar    int
	AgentKind    string
	WorkerPrompt string
	Notify       bool
	WTRoot       string
	LogPath      string
	Plain        bool
}

func envInt(name string, def int, problems *[]string) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		*problems = append(*problems, fmt.Sprintf("%s must be a whole number (got '%s').", name, v))
	}
	return n
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// loadConfig reads flags (defaulting to the environment variables orchestrate.sh used) and
// collects every setup problem, so they can be reported together.
func loadConfig() (Config, []string) {
	var c Config
	var problems []string

	flag.StringVar(&c.Workspace, "workspace", os.Getenv("WORKSPACE"), "Herdr workspace for the worker tabs (list IDs with: herdr workspace list) [WORKSPACE]")
	flag.IntVar(&c.Limit, "limit", envInt("LIMIT", 40, &problems), "stop after this many tickets in total [LIMIT]")
	flag.IntVar(&c.DoneSoFar, "done-so-far", envInt("DONE_SO_FAR", 0, &problems), "tickets dispatched in earlier runs, counted toward -limit [DONE_SO_FAR]")
	flag.StringVar(&c.AgentKind, "agent", envOr("AGENT_KIND", "claude"), "Herdr agent kind for the workers [AGENT_KIND]")
	flag.StringVar(&c.WorkerPrompt, "prompt", envOr("WORKER_PROMPT", ".claude/worker-prompt.md"), "worker instructions with TICKET_ID as placeholder [WORKER_PROMPT]")
	flag.BoolVar(&c.Notify, "notify", os.Getenv("NOTIFY") != "0", "macOS notifications for finished tickets and stops [NOTIFY=0 turns off]")
	flag.StringVar(&c.WTRoot, "worktrees", os.Getenv("WT_ROOT"), "folder for the per-ticket worktrees, outside the repository (default: <repo>-worktrees next to it) [WT_ROOT]")
	flag.BoolVar(&c.Plain, "plain", false, "print plain log lines instead of the interactive view (automatic when not on a terminal)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: WORKSPACE=<id> orchestrate [flags]\n\nWork through 'bd ready' one ticket at a time, one agent per Herdr tab and git worktree.\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nExit codes: 0 done, 2 setup problem, 3 worker blocked or paused, 4 Herdr/Beads/git failure,\n5 main checkout dirty or off its branch, 6 merge failed, 130 Ctrl+C.\n")
	}
	flag.Parse()

	if out, err := run("", "git", "rev-parse", "--show-toplevel"); err == nil {
		c.Repo = strings.TrimSpace(out)
	} else {
		problems = append(problems, "Not inside a git repository: cd into the project first.")
	}
	if c.Workspace == "" {
		problems = append(problems, "WORKSPACE is not set. Find the ID with 'herdr workspace list', then run: WORKSPACE=<id> orchestrate")
	}
	if os.Getenv("HERDR_ENV") != "1" {
		problems = append(problems, "Not running inside a Herdr pane (HERDR_ENV is not 1). Start 'herdr' and run this from a pane.")
	}
	for _, cmd := range []string{"bd", "herdr", "git"} {
		if _, err := exec.LookPath(cmd); err != nil {
			problems = append(problems, "Required command not found: "+cmd)
		}
	}

	if c.Repo != "" {
		if !filepath.IsAbs(c.WorkerPrompt) {
			c.WorkerPrompt = filepath.Join(c.Repo, c.WorkerPrompt)
		}
		if b, err := os.ReadFile(c.WorkerPrompt); err != nil {
			problems = append(problems, "Worker prompt not found: "+c.WorkerPrompt)
		} else if !strings.Contains(string(b), "TICKET_ID") {
			problems = append(problems, "Worker prompt has no TICKET_ID placeholder: "+c.WorkerPrompt)
		}
		if st, err := os.Stat(filepath.Join(c.Repo, ".beads")); err != nil || !st.IsDir() {
			problems = append(problems, "No Beads database in "+c.Repo+". Run: bd init")
		}

		// Finished tickets are merged into the main checkout's branch, so run from there, on a branch.
		gitDir, _ := run(c.Repo, "git", "rev-parse", "--absolute-git-dir")
		commonDir, _ := run(c.Repo, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if strings.TrimSpace(gitDir) != strings.TrimSpace(commonDir) {
			problems = append(problems, c.Repo+" is a linked worktree. Run this from the main checkout.")
		}
		if c.Base = currentBranch(c.Repo); c.Base == "" {
			problems = append(problems, "The main checkout is on a detached HEAD. Check out the branch finished tickets should land on.")
		}

		if c.WTRoot == "" {
			c.WTRoot = filepath.Join(filepath.Dir(c.Repo), filepath.Base(c.Repo)+"-worktrees")
		}
		if abs, err := filepath.Abs(c.WTRoot); err == nil {
			c.WTRoot = abs
		}
		if c.WTRoot == c.Repo || strings.HasPrefix(c.WTRoot, c.Repo+"/") {
			problems = append(problems, fmt.Sprintf("WT_ROOT (%s) must be outside the repository, or git sees the worktrees as untracked files.", c.WTRoot))
		}
		c.LogPath = filepath.Join(c.Repo, ".claude", "orchestrate.log")
	}
	return c, problems
}

func main() {
	cfg, problems := loadConfig()
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "orchestrate cannot start:")
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  - "+p)
		}
		os.Exit(exitSetup)
	}

	prompt, _ := os.ReadFile(cfg.WorkerPrompt)
	os.MkdirAll(cfg.WTRoot, 0o755)
	os.MkdirAll(filepath.Dir(cfg.LogPath), 0o755)
	log, err := openLogger(cfg.LogPath, cfg.Notify, filepath.Base(cfg.Repo))
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestrate cannot open its log:", err)
		os.Exit(exitSetup)
	}
	if err := os.Chdir(cfg.Repo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitSetup)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	orch := &Orch{cfg: cfg, log: log, prompt: string(prompt)}

	if cfg.Plain || !term.IsTerminal(int(os.Stdout.Fd())) {
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		orch.sink = plainSink{}
		orch.reportInterrupt = true
		os.Exit(orch.Run(ctx))
	}

	p := tea.NewProgram(newModel(cfg, cancel))
	orch.sink = teaSink{p}
	codes := make(chan int, 1)
	go func() {
		codes <- orch.Run(ctx)
		p.Send(finishedMsg{})
	}()
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestrate:", err)
	}
	if m, ok := final.(model); ok && m.interrupted {
		// The loop may be in the middle of a command; log the stop and leave the worker to the user.
		msg := "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree"
		if m.st.Ticket != "" {
			msg = fmt.Sprintf("INTERRUPTED: stopped with Ctrl+C while %s was running; its tab %s and worktree are left open", m.st.Ticket, m.st.Tab)
		}
		ev := Event{Kind: EvStop, Text: msg}
		ev.Time = time.Now()
		log.Line(ev.Time, msg)
		fmt.Println(renderEvent(ev))
		os.Exit(exitInterrupted)
	}
	if m, ok := final.(model); ok && m.final != nil {
		fmt.Println(renderEvent(*m.final))
	}
	os.Exit(<-codes)
}
