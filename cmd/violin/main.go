package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/film/violin/internal/auth"
	"github.com/film/violin/internal/config"
	"github.com/film/violin/internal/credentials"
	"github.com/film/violin/internal/health"
	"github.com/film/violin/internal/jobs"
	"github.com/film/violin/internal/laya"
	"github.com/film/violin/internal/mcp"
	"github.com/film/violin/internal/models"
	"github.com/film/violin/internal/setup"
	"github.com/film/violin/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "mcp", "serve":
		if len(os.Args) > 2 && os.Args[2] == "install" {
			err = installMCP(os.Args[3:])
		} else {
			err = mcp.Run(os.Stdin, os.Stdout)
		}
	case "init":
		err = installMCP(os.Args[2:])
	case "install":
		err = installRuntime()
	case "uninstall":
		err = uninstallMCP(os.Args[2:])
	case "skills":
		err = skillsCommand(os.Args[2:])
	case "auth":
		err = authCommand(os.Args[2:])
	case "health":
		err = healthCommand(os.Args[2:])
	case "run":
		err = runCommand(os.Args[2:])
	case "wait":
		err = waitCommand(os.Args[2:])
	case "list":
		err = listCommand()
	case "interrupt":
		err = interruptCommand(os.Args[2:])
	case "config":
		err = configCommand(os.Args[2:])
	case "model":
		err = modelCommand(os.Args[2:])
	case "worker":
		err = workerCommand(os.Args[2:])
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func workerCommand(args []string) error {
	options, err := worker.Parse(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return worker.Run(ctx, options)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: violin {install|mcp|init|uninstall|skills|auth|health|run|wait|list|interrupt|config|model}")
}

func installRuntime() error {
	if err := laya.EnsureDefaultModel(root()); err != nil {
		return err
	}
	if _, err := setup.ConfigPlan(true); err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	plan, err := setup.MCPPlan(os.Args[0], true)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"action": "install", "model": laya.DefaultModelVersion, "mcp": plan})
}

func healthCommand(args []string) error {
	provider := "all"
	if len(args) > 0 {
		provider = args[0]
	}
	settings, err := config.LoadFor("")
	if err != nil {
		return err
	}
	if provider == "all" {
		result := map[string]health.Result{}
		for _, name := range []string{"qwen", "agy", "claude"} {
			result[name] = health.Check(context.Background(), settings, name, credentials.Default())
		}
		return printJSON(result)
	}
	if provider != "qwen" && provider != "agy" && provider != "claude" {
		return fmt.Errorf("unknown provider %q", provider)
	}
	return printJSON(health.Check(context.Background(), settings, provider, credentials.Default()))
}

func authCommand(args []string) error {
	if len(args) < 2 {
		return errors.New("auth requires status/login/set/remove and provider")
	}
	manager := auth.NewManager(credentials.Default())
	provider := args[1]
	if args[0] == "status" {
		settings, err := config.LoadFor("")
		if err != nil {
			return err
		}
		if provider == "all" {
			result, err := manager.AllStatusWithConfig(context.Background(), settings)
			if err != nil {
				return err
			}
			return printJSON(result)
		}
		status, err := manager.StatusWithBackend(context.Background(), provider, settings.Backend[provider])
		if err != nil {
			return err
		}
		return printJSON(status)
	}
	if args[0] == "login" {
		settings, err := config.LoadFor("")
		if err != nil {
			return err
		}
		return manager.LoginWithBackend(context.Background(), provider, settings.Backend[provider])
	}
	if args[0] == "set" {
		value, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && len(value) == 0 {
			return err
		}
		return manager.SetAPIKey(context.Background(), provider, strings.TrimSpace(value))
	}
	if args[0] == "remove" {
		return manager.RemoveAPIKey(context.Background(), provider)
	}
	return errors.New("auth requires status or login")
}

func installMCP(args []string) error {
	apply := false
	rollback := ""
	for _, arg := range args {
		if arg == "--apply" {
			apply = true
		}
		if arg == "--dry-run" {
			apply = false
		}
	}
	for index, arg := range args {
		if arg == "--rollback" && index+1 < len(args) {
			rollback = args[index+1]
		}
	}
	if rollback != "" {
		plan, err := setup.MCPRollbackPlan(rollback, apply)
		if err != nil {
			return err
		}
		return printJSON(plan)
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	plan, err := setup.MCPPlan(binary, apply)
	if err != nil {
		return err
	}
	return printJSON(plan)
}

func uninstallMCP(args []string) error {
	apply := false
	for _, arg := range args {
		if arg == "--apply" {
			apply = true
		}
		if arg == "--dry-run" {
			apply = false
		}
	}
	plan, err := setup.MCPUninstallPlan(apply)
	if err != nil {
		return err
	}
	return printJSON(plan)
}

func skillsCommand(args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return errors.New("skills requires install")
	}
	apply := false
	for _, arg := range args[1:] {
		if arg == "--apply" {
			apply = true
		}
		if arg == "--dry-run" {
			apply = false
		}
	}
	plan, err := setup.SkillsPlan(os.Getenv("VIOLIN_SKILLS_DIR"), apply)
	if err != nil {
		return err
	}
	return printJSON(plan)
}

func root() string {
	if value := os.Getenv("VIOLIN_WORKER_RUNS"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "violin-workers")
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	backend := fs.String("backend", "auto", "backend")
	cwd := fs.String("C", ".", "workspace")
	task := fs.String("task", "", "task text")
	taskFile := fs.String("task-file", "", "task file")
	mode := fs.String("mode", "inspect", "inspect or implement")
	timeout := fs.Int("timeout", 0, "timeout seconds")
	idle := fs.Int("idle-timeout", 300, "idle timeout seconds")
	background := fs.Bool("background", false, "return without waiting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*task == "") == (*taskFile == "") {
		return errors.New("exactly one of --task or --task-file is required")
	}
	content := *task
	if *taskFile != "" {
		data, err := os.ReadFile(*taskFile)
		if err != nil {
			return err
		}
		content = string(data)
	}
	workspace, err := filepath.Abs(*cwd)
	if err != nil {
		return err
	}
	job, err := jobs.Spawn(jobs.Options{Root: root(), Workspace: workspace, Backend: *backend, RequestedBackend: *backend, Mode: *mode, Task: content, Timeout: *timeout, IdleTimeout: *idle})
	if err != nil {
		return err
	}
	if *background {
		return printJSON(job.Live())
	}
	report, err := job.Wait(0)
	if err != nil {
		return err
	}
	return printJSON(report)
}

func waitCommand(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	seconds := fs.Int("wait-seconds", 50, "wait seconds")
	agentID := ""
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		agentID, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if agentID == "" && fs.NArg() == 1 {
		agentID = fs.Arg(0)
	}
	if agentID == "" || fs.NArg() > 1 {
		return errors.New("wait requires agent id")
	}
	job, err := jobs.Open(root(), agentID)
	if err != nil {
		return err
	}
	value, err := job.Wait(*seconds)
	if err != nil {
		return err
	}
	return printJSON(value)
}

func listCommand() error {
	values, err := jobs.List(root())
	if err != nil {
		return err
	}
	return printJSON(values)
}

func interruptCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("interrupt requires agent id")
	}
	job, err := jobs.Open(root(), args[0])
	if err != nil {
		return err
	}
	value, err := job.Interrupt()
	if err != nil {
		return err
	}
	return printJSON(value)
}

func configCommand(args []string) error {
	if len(args) > 0 && args[0] == "init" {
		apply := false
		for _, arg := range args[1:] {
			if arg == "--apply" {
				apply = true
			}
			if arg == "--dry-run" {
				apply = false
			}
		}
		plan, err := setup.ConfigPlan(apply)
		if err != nil {
			return err
		}
		return printJSON(plan)
	}
	if len(args) != 1 || (args[0] != "show" && args[0] != "validate") {
		return errors.New("config requires init, show, or validate")
	}
	c, err := config.Load("")
	if err != nil {
		return err
	}
	if args[0] == "validate" {
		fmt.Println("valid")
		return nil
	}
	return printJSON(c)
}

func modelCommand(args []string) error {
	if len(args) < 1 {
		return errors.New("model requires status, verify, rollback, update, or recalibrate")
	}
	m, err := models.NewManager(filepath.Join(root(), "models"))
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		return printJSON(m.Status())
	case "verify":
		return m.Verify()
	case "rollback":
		if len(args) != 2 {
			return errors.New("model rollback requires version")
		}
		return m.Rollback(args[1])
	case "update":
		fs := flag.NewFlagSet("model update", flag.ContinueOnError)
		manifestPath := fs.String("manifest", "", "manifest JSON")
		sourceDir := fs.String("source-dir", "", "downloaded model bundle directory")
		baseURL := fs.String("base-url", "", "base URL for model artifacts")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *manifestPath == "" || (*sourceDir == "" && *baseURL == "") {
			return errors.New("model update requires --manifest and either --source-dir or --base-url")
		}
		data, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		var manifest models.Manifest
		if err = json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		if *baseURL != "" {
			err = m.InstallFromURL(manifest, *baseURL)
		} else {
			err = m.Install(manifest, *sourceDir, true)
		}
		if err != nil {
			return err
		}
		return printJSON(m.Status())
	case "recalibrate":
		report, err := laya.Recalibrate(root())
		if err != nil {
			return err
		}
		return printJSON(report)
	default:
		return errors.New("unknown model command")
	}
}

func printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = io.WriteString(os.Stdout, string(data)+"\n")
	return err
}
