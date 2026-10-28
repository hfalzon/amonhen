package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	appName = "amonhen"
	service = "amonhen/bws-machine-account"
)

var version = "dev"

type Project struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

func main() {
	if len(os.Args) == 1 && installOnExplorerDoubleClick() {
		return
	}
	if err := run(os.Args[1:]); err != nil {
		var exitErr *commandExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		fmt.Fprintln(os.Stderr, "amonhen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	if args[0] == "--version" || args[0] == "-V" || args[0] == "-v" || args[0] == "version" {
		fmt.Println("amonhen", version)
		return nil
	}
	switch args[0] {
	case "storage":
		return storageCommand(args[1:])
	case "broker":
		return brokerCommand(args[1:])
	case "token":
		return tokenCommand(args[1:])
	case "project":
		return projectCommand(args[1:])
	case "ls", "list":
		return listProjects(args[1:])
	case "delete":
		if containsHelp(args[1:]) {
			fmt.Print(helpText("project delete"))
			return nil
		}
		return withProjectLock(func() error { return deleteProject(args[1:]) })
	case "install":
		return installCommand(args[1:])
	case "run":
		return runCommand(args[1:])
	case "test":
		return testCommand(args[1:])
	case "help", "--help", "-h":
		return usage()
	default:
		return fmt.Errorf("unknown command %q; run 'amonhen help'", args[0])
	}
}

func usage() error {
	fmt.Print(helpText(""))
	fmt.Println("\nStorage: amonhen storage <path|migrate|init>\nRename: amonhen project rename -N <name> --new-name <name>\nBroker: amonhen broker <serve|grant|revoke|fetch>")
	return nil
}
func isHelp(arg string) bool { return arg == "--help" || arg == "-h" }

func helpText(topic string) string {
	switch topic {
	case "project":
		return "Usage: amonhen project <add|list|uuid|token|rename|delete>\n\nOptions:\n  -h, --help  Show this help\n"
	case "project add":
		return "Usage: amonhen project add -N <name> -U <BWS-project-UUID> -T\n\nAdd a project. -T prompts for its machine-account token without echo; BWS access is checked before the token is stored. Raw tokens are not accepted as arguments.\n\nOptions:\n  -N, --name   Unique local project name\n  -U, --uuid   Bitwarden Secrets Manager project UUID\n  -T, --token  Prompt securely for the project machine-account token\n  -h, --help   Show this help\n"
	case "project list":
		return "Usage: amonhen project list (alias: amonhen ls)\n\nShow configured project names and BWS project UUIDs. Tokens are never listed.\n\nOptions:\n  -h, --help  Show this help\n"
	case "project uuid":
		return "Usage: amonhen project uuid -N <project-name>\n\nPrint the BWS project UUID for the named project.\n\nOptions:\n  -N, --name  Select the Amon Hen project\n  -h, --help  Show this help\n"
	case "project token":
		return "Usage: amonhen project token set -N <project-name> -T\n\nReplace or restore the stored BWS machine-account token for an existing project. The token is entered at a hidden prompt.\n\nOptions:\n  -h, --help  Show this help\n"
	case "project token set":
		return "Usage: amonhen project token set -N <project-name> -T\n\nSecurely replace the stored token for an existing project. BWS access is checked before the existing credential is replaced. The token is entered at a hidden prompt, never as a command-line argument.\n\nOptions:\n  -N, --name   Select the existing project\n  -T, --token  Prompt securely for the replacement token\n  -h, --help   Show this help\n"
	case "project delete":
		return "Usage: amonhen project delete (-N <name> | -U <BWS-project-UUID>)\n\nDelete one project and its stored machine-account token.\n\nOptions:\n  -N, --name  Select by local project name\n  -U, --uuid  Select by BWS project UUID\n  -h, --help  Show this help\n"
	case "run":
		return "Usage: amonhen run <project-name> -- <command> [args...]\n\nRun bws run scoped to the project's UUID. BWS injects its secrets into the command environment.\n\nOptions:\n  -h, --help  Show this help\n"
	case "test":
		return "Usage: amonhen test token -N <project-name>\n\nCheck whether the stored token can access its configured BWS project. Makes a read-only BWS request and never prints the token.\n\nOptions:\n  -h, --help  Show this help\n"
	case "test token":
		return "Usage: amonhen test token -N <project-name>\n\nCheck whether the stored token can access its configured BWS project. Makes a read-only BWS request and never prints the token.\n\nOptions:\n  -N, --name  Select the configured project\n  -h, --help  Show this help\n"
	case "install":
		return "Usage: amonhen install\n\nInstall this executable for the current user and add its directory to PATH.\n\nOptions:\n  -h, --help  Show this help\n"
	default:
		if topic == "token" {
			return "Usage: amonhen token -N <project-name> -- <command> [args...]\n\nRun a child process with that project's stored BWS machine token in BWS_ACCESS_TOKEN. Amon Hen does not print the token. The child process can read it.\n\nOptions:\n  -N, --name  Select the project whose token to pass\n  -h, --help  Show this help\n"
		}
		return "Amon Hen — manage Bitwarden Secrets Manager machine-account keys\n\nUsage:\n  amonhen project add -N <name> -U <BWS-project-UUID> -T\n  amonhen project token set -N <name> -T\n  amonhen test token -N <project-name>\n  amonhen project list\n  amonhen project uuid -N <name>\n  amonhen ls\n  amonhen project delete -N <name>\n  amonhen project delete -U <BWS-project-UUID>\n  amonhen token -N <project-name> -- <command> [args...]\n  amonhen run <project-name> -- <command> [args...]\n  amonhen install\n  amonhen version | -V | --version\n\nProject tokens are stored in the OS credential store and never displayed by Amon Hen.\nOptions: -h, --help show help for a command.\n"
	}
}

func testCommand(args []string) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Print(helpText("test"))
		return nil
	}
	if args[0] != "token" {
		return errors.New("usage: amonhen test token -N <project-name> (use --help for details)")
	}
	if containsHelp(args[1:]) {
		fmt.Print(helpText("test token"))
		return nil
	}
	opts, err := parseOptions(args[1:], map[string]bool{"name": true})
	if err != nil {
		return err
	}
	name := opts["name"]
	if !validName(name) {
		return errors.New("a valid project name is required with -N")
	}
	project, err := findProject(name)
	if err != nil {
		return err
	}
	token, err := keyring.Get(service, project.Name)
	if err != nil {
		return projectTokenReadError(project.Name, err)
	}
	defer clear([]byte(token))
	if err := validateBwsToken(project.UUID, token); err != nil {
		return fmt.Errorf("BWS token check failed for project %q: %w", project.Name, err)
	}
	fmt.Printf("Token for project %s can access BWS project %s.\n", project.Name, project.UUID)
	return nil
}

func findProject(name string) (*Project, error) {
	projects, err := loadProjects()
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if strings.EqualFold(projects[i].Name, name) {
			return &projects[i], nil
		}
	}
	return nil, fmt.Errorf("project %q not found", name)
}

type bwsCommandRunner func(env []string, args ...string) ([]byte, error)

func runBwsCommand(env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("bws", args...)
	cmd.Env = env
	return cmd.CombinedOutput()
}

func validateBwsToken(uuid, token string) error {
	return validateBwsTokenUsing(uuid, token, os.Environ(), runBwsCommand)
}

func validateBwsTokenUsing(uuid, token string, env []string, run bwsCommandRunner) error {
	if !validUUID(uuid) {
		return errors.New("configured BWS project UUID is invalid")
	}
	if token == "" {
		return errors.New("stored token is empty")
	}
	_, err := run(withBWSAccessToken(env, token), "project", "get", uuid, "--output", "none")
	if err == nil {
		return nil
	}
	// BWS diagnostics can contain credentials or secret values. Do not relay them.
	return fmt.Errorf("could not verify token access to project %s; check BWS installation, connectivity, and machine-account permissions", uuid)
}

func validateTokenBeforeStore(uuid, token string, validate func(string, string) error, store func() error) error {
	if err := validate(uuid, token); err != nil {
		return fmt.Errorf("token was not stored because BWS validation failed: %w", err)
	}
	return store()
}

func tokenCommand(args []string) error {
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	optionArgs := args
	if separator >= 0 {
		optionArgs = args[:separator]
	}
	if containsHelp(optionArgs) {
		fmt.Print(helpText("token"))
		return nil
	}
	if separator < 0 || separator+1 >= len(args) {
		return errors.New("usage: amonhen token -N <project-name> -- <command> [args...] (use --help for details)")
	}
	opts, err := parseOptions(args[:separator], map[string]bool{"name": true})
	if err != nil {
		return err
	}
	name := opts["name"]
	if !validName(name) {
		return errors.New("a valid project name is required with -N")
	}
	project, err := findProject(name)
	if err != nil {
		return err
	}
	// Amon Lhaw's existing BWS secret-list call can use an explicitly granted
	// broker without passing a machine credential into the sandbox.
	child := args[separator+1:]
	binary := strings.TrimSuffix(strings.ToLower(filepath.Base(child[0])), ".exe")
	if len(child) == 4 && binary == "bws" && child[1] == "secret" && child[2] == "list" && strings.EqualFold(child[3], project.UUID) {
		_, _, grantErr := brokerFindClientGrant("", project.Name)
		if grantErr == nil {
			return brokerFetch("", project.Name, 30*time.Second)
		}
		if !errors.Is(grantErr, errBrokerGrantNotFound) {
			return grantErr
		}
	}
	token, err := keyring.Get(service, project.Name)
	if err != nil {
		return projectTokenReadError(name, err)
	}
	defer clear([]byte(token))
	cmd := exec.Command(args[separator+1], args[separator+2:]...)
	cmd.Env = withBWSAccessToken(os.Environ(), token)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &commandExitError{code: exitErr.ExitCode()}
		}
		return fmt.Errorf("run token child process: %w", err)
	}
	return nil
}

func installCommand(args []string) error {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Print(helpText("install"))
		return nil
	}
	if len(args) != 0 {
		return errors.New("usage: amonhen install (use --help for details)")
	}
	return installSelf()
}

func projectCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: amonhen project <add|list|uuid|token|delete> (use --help for details)")
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Print(helpText("project"))
		return nil
	}
	switch args[0] {
	case "rename":
		if containsHelp(args[1:]) {
			return renameProject(args[1:])
		}
		return withProjectLock(func() error { return renameProject(args[1:]) })
	case "add":
		if containsHelp(args[1:]) {
			fmt.Print(helpText("project add"))
			return nil
		}
		return withProjectLock(func() error { return addProject(args[1:]) })
	case "list":
		return listProjects(args[1:])
	case "uuid":
		if containsHelp(args[1:]) {
			fmt.Print(helpText("project uuid"))
			return nil
		}
		return projectUUID(args[1:])
	case "token":
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Print(helpText("project token"))
			return nil
		}
		if len(args) > 1 && args[1] == "set" && containsHelp(args[2:]) {
			fmt.Print(helpText("project token set"))
			return nil
		}
		return withProjectLock(func() error { return projectTokenCommand(args[1:]) })
	case "delete":
		if containsHelp(args[1:]) {
			fmt.Print(helpText("project delete"))
			return nil
		}
		return withProjectLock(func() error { return deleteProject(args[1:]) })
	default:
		return errors.New("usage: amonhen project <add|list|uuid|token|delete> (use --help for details)")
	}
}

func projectUUID(args []string) error {
	opts, err := parseOptions(args, map[string]bool{"name": true})
	if err != nil {
		return err
	}
	name := opts["name"]
	if !validName(name) {
		return errors.New("a valid project name is required with -N")
	}
	project, err := findProject(name)
	if err != nil {
		return err
	}
	fmt.Println(project.UUID)
	return nil
}

func projectTokenCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: amonhen project token set -N <project-name> -T (use --help for details)")
	}
	if args[0] != "set" {
		return errors.New("usage: amonhen project token set -N <project-name> -T (use --help for details)")
	}
	return setProjectToken(args[1:])
}

func setProjectToken(args []string) error {
	opts, err := parseOptions(args, map[string]bool{"name": true, "token": true})
	if err != nil {
		return err
	}
	name := opts["name"]
	if !validName(name) {
		return errors.New("a valid project name is required with -N")
	}
	if opts["token"] != "true" {
		return errors.New("-T is required to securely prompt for the replacement token")
	}
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	found := false
	projectUUID := ""
	for _, project := range projects {
		if strings.EqualFold(project.Name, name) {
			name = project.Name
			projectUUID = project.UUID
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("project %q not found; add it with 'amonhen project add' first", name)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("project token set requires an interactive terminal for the hidden token prompt")
	}
	fmt.Fprint(os.Stderr, "Replacement BWS machine-account token: ")
	token, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read replacement token: %w", err)
	}
	defer clear(token)
	if len(token) == 0 {
		return errors.New("token cannot be empty")
	}
	if err := validateTokenBeforeStore(projectUUID, string(token), validateBwsToken, func() error {
		return keyring.Set(service, name, string(token))
	}); err != nil {
		return fmt.Errorf("replace token for project %q: %w", name, err)
	}
	fmt.Printf("Updated stored token for project %s.\n", name)
	return nil
}

func projectTokenReadError(name string, err error) error {
	if errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("token for project %q was not found in the OS credential store; restore it with 'amonhen project token set -N %s -T'", name, name)
	}
	return fmt.Errorf("read token for project %q from the OS credential store: %w", name, err)
}

func containsHelp(args []string) bool {
	for _, a := range args {
		if isHelp(a) {
			return true
		}
	}
	return false
}

func parseOptions(args []string, allowed map[string]bool) (map[string]string, error) {
	values := make(map[string]string)
	for i := 0; i < len(args); i++ {
		key := args[i]
		switch key {
		case "-N", "--name":
			key = "name"
		case "-U", "--uuid":
			key = "uuid"
		case "-T", "--token":
			key = "token"
		case "--new-name":
			key = "new-name"
		}
		if !allowed[key] {
			return nil, errors.New("unexpected argument; tokens are entered only at the hidden prompt")
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("option %q was provided more than once", args[i])
		}
		if key == "token" {
			values[key] = "true"
			continue
		}
		i++
		if i >= len(args) || strings.HasPrefix(args[i], "-") {
			return nil, fmt.Errorf("option %q requires a value", args[i-1])
		}
		values[key] = args[i]
	}
	return values, nil
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func validUUID(s string) bool { return uuidPattern.MatchString(s) }

func addProject(args []string) error {
	opts, err := parseOptions(args, map[string]bool{"name": true, "uuid": true, "token": true})
	if err != nil {
		return err
	}
	name, uuid := opts["name"], strings.ToLower(opts["uuid"])
	if !validName(name) {
		return errors.New("project name is required and may contain only letters, numbers, dots, underscores, and hyphens")
	}
	if !validUUID(uuid) {
		return errors.New("a valid BWS project UUID is required with -U")
	}
	if opts["token"] != "true" {
		return errors.New("-T is required to securely prompt for the project machine-account token")
	}
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, name) {
			return fmt.Errorf("project name %q already exists", name)
		}
		if strings.EqualFold(p.UUID, uuid) {
			return fmt.Errorf("BWS project UUID %q is already configured", uuid)
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("project add requires an interactive terminal for the hidden token prompt")
	}
	fmt.Fprint(os.Stderr, "BWS machine-account token: ")
	token, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read machine-account token: %w", err)
	}
	defer clear(token)
	if len(token) == 0 {
		return errors.New("token cannot be empty")
	}
	if err = validateTokenBeforeStore(uuid, string(token), validateBwsToken, func() error {
		return keyring.Set(service, name, string(token))
	}); err != nil {
		return fmt.Errorf("add project %q: %w", name, err)
	}
	projects = append(projects, Project{Name: name, UUID: uuid})
	if err = saveProjects(projects); err != nil {
		rollback := keyring.Delete(service, name)
		if rollback != nil {
			return fmt.Errorf("save project config: %v (also failed to remove stored token: %w)", err, rollback)
		}
		return err
	}
	fmt.Printf("Added project %s (%s); machine token stored in OS credential store.\n", name, uuid)
	return nil
}

func listProjects(args []string) error {
	if containsHelp(args) {
		fmt.Print(helpText("project list"))
		return nil
	}
	if len(args) > 0 {
		return errors.New("usage: amonhen project list")
	}
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	fmt.Println("NAME\tBWS_PROJECT_UUID")
	for _, p := range projects {
		fmt.Printf("%s\t%s\n", p.Name, p.UUID)
	}
	return nil
}

func deleteProject(args []string) error {
	if containsHelp(args) {
		fmt.Print(helpText("project delete"))
		return nil
	}
	opts, err := parseOptions(args, map[string]bool{"name": true, "uuid": true})
	if err != nil {
		return err
	}
	name, uuid := opts["name"], strings.ToLower(opts["uuid"])
	if (name == "") == (uuid == "") {
		return errors.New("select exactly one project using -N <name> or -U <uuid>")
	}
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	index := -1
	for i, p := range projects {
		if name != "" && strings.EqualFold(p.Name, name) || uuid != "" && strings.EqualFold(p.UUID, uuid) {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("project not found")
	}
	p := projects[index]
	updated := append(projects[:index], projects[index+1:]...)
	if err := saveProjects(updated); err != nil {
		return err
	}
	if err := keyring.Delete(service, p.Name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("project removed, but could not remove its OS-stored token: %w", err)
	}
	fmt.Printf("Deleted project %s (%s) and its stored token.\n", p.Name, p.UUID)
	return nil
}

func runCommand(args []string) error {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Print(helpText("run"))
		return nil
	}
	separator := -1
	for i, a := range args {
		if a == "--" {
			separator = i
			break
		}
	}
	if separator < 1 || separator+1 >= len(args) {
		return errors.New("usage: amonhen run <project-name> -- <command> [args...] (use --help for details)")
	}
	name := args[0]
	project, err := findProject(name)
	if err != nil {
		return err
	}
	token, err := keyring.Get(service, project.Name)
	if err != nil {
		return projectTokenReadError(project.Name, err)
	}
	defer clear([]byte(token))
	cmdArgs := bwsRunArgs(project.UUID, bwsChildCommand(args[separator+1:]))
	cmd := exec.Command("bws", cmdArgs...)
	cmd.Env = withBWSAccessToken(os.Environ(), token)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &commandExitError{code: exitErr.ExitCode()}
		}
		return fmt.Errorf("run BWS command: %w", err)
	}
	return nil
}

func bwsRunArgs(uuid string, command []string) []string {
	args := []string{"run", "--no-inherit-env", "--project-id", uuid, "--"}
	return append(args, command...)
}
func withBWSAccessToken(env []string, token string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "BWS_ACCESS_TOKEN") {
			result = append(result, entry)
		}
	}
	return append(result, "BWS_ACCESS_TOKEN="+token)
}

type commandExitError struct{ code int }

func (e *commandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}

func validName(s string) bool { return regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(s) }
