package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	appName = "amonhen"
	service = "amonhen/bws-access-token"
)

var version = "dev"

type Project struct {
	Name    string            `json:"name"`
	Secrets map[string]string `json:"secrets"`
}

type Secret struct {
	Value string `json:"value"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		var exitErr *commandExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		fmt.Fprintln(os.Stderr, "amonhen:", scrub(err.Error()))
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
	case "token":
		return tokenCommand(args[1:])
	case "project":
		return projectCommand(args[1:])
	case "install":
		return installCommand(args[1:])
	case "run":
		return runCommand(args[1:])
	case "help", "--help", "-h":
		return usage()
	default:
		return fmt.Errorf("unknown command %q; run 'amonhen help'", args[0])
	}
}

func usage() error {
	fmt.Print(helpText(""))
	return nil
}

func isHelp(arg string) bool { return arg == "--help" || arg == "-h" }

func helpText(topic string) string {
	switch topic {
	case "token":
		return `Usage: amonhen token set

Save a BWS machine-account access token in the operating system credential store.
The token is entered without terminal echo and is not accepted as an argument.

Options:
  -h, --help  Show help for token commands
`
	case "token set":
		return `Usage: amonhen token set

Prompt for a BWS access token without echo and save it in the OS credential store.

Options:
  -h, --help  Show this help
`
	case "project":
		return `Usage: amonhen project <command>

Commands:
  add <name> KEY=bws://<secret-uuid> [...]  Add a named project
  list                                     List configured projects

Options:
  -h, --help  Show help for project commands
`
	case "project add":
		return `Usage: amonhen project add <name> KEY=bws://<secret-uuid> [...]

Add a uniquely named project and map environment variable names to BWS secret IDs.
Project definitions store references only; they never contain resolved values.

Options:
  -h, --help  Show this help
`
	case "project list":
		return `Usage: amonhen project list

List project names and the number of BWS secret references configured for each.

Options:
  -h, --help  Show this help
`
	case "run":
		return `Usage: amonhen run <project> -- <command> [args...]

Resolve a project's BWS secret references and start the requested command with
the values in its environment. Child stdout and stderr are masked for literal
secret values.

Options:
  -h, --help  Show this help
`
	case "install":
		return `Usage: amonhen install

Install this Amon Hen executable for the current user and add its directory to
the user's PATH. No administrator access is required.

Options:
  -h, --help  Show this help
`
	default:
		return `Amon Hen — pass Bitwarden Secrets Manager values by reference

Usage: amonhen [command]
  amonhen token set
  amonhen project add <name> KEY=bws://<secret-uuid> [...]
  amonhen project list
  amonhen install
  amonhen run <project> -- <command> [args...]
  amonhen version
  amonhen -V, --version

The BWS access token is requested without echo and stored in the OS credential
store. Project definitions contain secret references, never secret values.

Options:
  -h, --help  Show help for a command
  -v, -V, --version  Show version
`
	}
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

func tokenCommand(args []string) error {
	if len(args) > 0 && isHelp(args[0]) || len(args) > 1 && args[0] == "set" && isHelp(args[1]) {
		topic := "token"
		if len(args) > 0 && args[0] == "set" || len(args) > 1 && args[1] == "set" {
			topic = "token set"
		}
		fmt.Print(helpText(topic))
		return nil
	}
	if len(args) != 1 || args[0] != "set" {
		return errors.New("usage: amonhen token set (use --help for details)")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("token set requires an interactive terminal; token input is not accepted as an argument or piped value")
	}
	fmt.Fprint(os.Stderr, "BWS access token: ")
	token, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	defer clear(token)
	if len(token) == 0 {
		return errors.New("token cannot be empty")
	}
	if err := keyring.Set(service, "default", string(token)); err != nil {
		return fmt.Errorf("store token in OS credential store: %w", err)
	}
	fmt.Println("BWS token saved in the OS credential store")
	return nil
}

func projectCommand(args []string) error {
	for _, arg := range args {
		if !isHelp(arg) {
			continue
		}
		topic := "project"
		if args[0] == "add" || args[0] == "list" {
			topic += " " + args[0]
		}
		fmt.Print(helpText(topic))
		return nil
	}
	if len(args) == 1 && args[0] == "list" {
		projects, err := loadProjects()
		if err != nil {
			return err
		}
		for _, p := range projects {
			fmt.Printf("%s\t%d secret reference(s)\n", p.Name, len(p.Secrets))
		}
		return nil
	}
	if len(args) < 3 || args[0] != "add" {
		return errors.New("usage: amonhen project add <name> KEY=bws://<secret-uuid> [...] (use --help for details)")
	}
	name := args[1]
	if !validName(name) {
		return errors.New("project name must contain only letters, numbers, dots, underscores, and hyphens")
	}
	project := Project{Name: name, Secrets: make(map[string]string)}
	for _, pair := range args[2:] {
		key, ref, ok := strings.Cut(pair, "=")
		if !ok || !validEnvName(key) || !strings.HasPrefix(ref, "bws://") || len(strings.TrimPrefix(ref, "bws://")) == 0 {
			return fmt.Errorf("invalid mapping %q; expected KEY=bws://<secret-uuid>", pair)
		}
		if strings.EqualFold(key, "BWS_ACCESS_TOKEN") {
			return errors.New("BWS_ACCESS_TOKEN is reserved and cannot be a project environment variable")
		}
		project.Secrets[key] = ref
	}
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	for _, existing := range projects {
		if existing.Name == name {
			return fmt.Errorf("project %q already exists", name)
		}
	}
	projects = append(projects, project)
	return saveProjects(projects)
}

func runCommand(args []string) error {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Print(helpText("run"))
		return nil
	}
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 1 || separator+1 >= len(args) {
		return errors.New("usage: amonhen run <project> -- <command> [args...] (use --help for details)")
	}
	name := args[0]
	projects, err := loadProjects()
	if err != nil {
		return err
	}
	var project *Project
	for i := range projects {
		if projects[i].Name == name {
			project = &projects[i]
			break
		}
	}
	if project == nil {
		return fmt.Errorf("project %q not found", name)
	}
	token, err := keyring.Get(service, "default")
	if err != nil {
		return errors.New("BWS token not found; run 'amonhen token set'")
	}
	defer func() {
		b := []byte(token)
		clear(b)
	}()

	values := make(map[string]string, len(project.Secrets))
	for envName, ref := range project.Secrets {
		id := strings.TrimPrefix(ref, "bws://")
		value, err := getBWSSecret(token, id)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", envName, err)
		}
		values[envName] = value
	}

	cmd := exec.Command(args[separator+1], args[separator+2:]...)
	cmd.Stdin = os.Stdin
	stdoutMask := newMaskWriter(os.Stdout, values)
	stderrMask := newMaskWriter(os.Stderr, values)
	cmd.Stdout = stdoutMask
	cmd.Stderr = stderrMask
	childEnv := make(map[string]string, len(values))
	for k, v := range values {
		childEnv[k] = v
	}
	cmd.Env = mergeEnv(os.Environ(), childEnv)
	runErr := cmd.Run()
	maskErr := errors.Join(stdoutMask.close(), stderrMask.close())
	if maskErr != nil {
		return maskErr
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return &commandExitError{code: exitErr.ExitCode()}
		}
		return scrubError(runErr, values)
	}
	return nil
}

func getBWSSecret(token, id string) (string, error) {
	cmd := exec.Command("bws", "secret", "get", id)
	cmd.Env = mergeEnv(os.Environ(), map[string]string{"BWS_ACCESS_TOKEN": token})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New("BWS secret retrieval failed (details suppressed to avoid credential leakage)")
	}
	var secret Secret
	if err := json.Unmarshal(stdout.Bytes(), &secret); err != nil {
		return "", errors.New("BWS returned an unexpected response")
	}
	if secret.Value == "" {
		return "", errors.New("BWS returned an empty secret")
	}
	return secret.Value, nil
}

func mergeEnv(base []string, additions map[string]string) []string {
	result := make([]string, 0, len(base)+len(additions))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := additions[key]; !replaced && !strings.EqualFold(key, "BWS_ACCESS_TOKEN") {
			result = append(result, entry)
		}
	}
	for key, value := range additions {
		result = append(result, key+"="+value)
	}
	return result
}

func loadProjects() ([]Project, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project configuration: %w", err)
	}
	var projects []Project
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, errors.New("project configuration is invalid JSON")
	}
	return projects, nil
}

func saveProjects(projects []Project) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("save project config: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("restrict project config permissions: %w", err)
	}
	return nil
}

func configPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, appName, "projects.json"), nil
}

func validName(s string) bool { return regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(s) }
func validEnvName(s string) bool {
	return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(s)
}

type maskWriter struct {
	dst    io.Writer
	values []string
	buf    []byte
	keep   int
}

func newMaskWriter(dst io.Writer, values map[string]string) *maskWriter {
	list := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			list = append(list, value)
		}
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	keep := 0
	for _, value := range list {
		if len(value)-1 > keep {
			keep = len(value) - 1
		}
	}
	return &maskWriter{dst: dst, values: list, keep: keep}
}

func (w *maskWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.keep {
		cut := len(w.buf) - w.keep
		// Keep a suffix that could be the start of a secret split across writes.
		prefix := string(w.buf[:cut])
		for n := min(w.keep, len(prefix)); n > 0; n-- {
			suffix := prefix[len(prefix)-n:]
			candidate := false
			for _, value := range w.values {
				if strings.HasPrefix(value, suffix) {
					candidate = true
					break
				}
			}
			if candidate {
				cut -= n
				break
			}
		}
		if _, err := w.flush(w.buf[:cut]); err != nil {
			return 0, err
		}
		w.buf = append([]byte(nil), w.buf[cut:]...)
	}
	return len(p), nil
}

func (w *maskWriter) flush(data []byte) (int, error) {
	text := string(data)
	for _, value := range w.values {
		text = strings.ReplaceAll(text, value, "***")
	}
	return io.WriteString(w.dst, text)
}

func (w *maskWriter) close() error {
	_, err := w.flush(w.buf)
	w.buf = nil
	return err
}

type commandExitError struct{ code int }

func (e *commandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}

func scrub(s string) string {
	if token, err := keyring.Get(service, "default"); err == nil && token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}

func scrubError(err error, values map[string]string) error {
	s := err.Error()
	ordered := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			ordered = append(ordered, value)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, value := range ordered {
		s = strings.ReplaceAll(s, value, "***")
	}
	return errors.New(scrub(s))
}
