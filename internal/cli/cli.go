// Package cli wires the commands together.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/mauritzuph/tesla-order-cli/internal/api"
	"github.com/mauritzuph/tesla-order-cli/internal/auth"
	"github.com/mauritzuph/tesla-order-cli/internal/callback"
	"github.com/mauritzuph/tesla-order-cli/internal/debug"
	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
	"github.com/mauritzuph/tesla-order-cli/internal/ordered"
	"github.com/mauritzuph/tesla-order-cli/internal/render"
	"github.com/mauritzuph/tesla-order-cli/internal/store"
	"github.com/mauritzuph/tesla-order-cli/internal/timeline"
	"github.com/mauritzuph/tesla-order-cli/internal/tracker"
	"github.com/mauritzuph/tesla-order-cli/internal/urlhandler"
)

// ExitError carries a process exit code.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// HandlerOps is the tesla:// helper, injectable for tests.
type HandlerOps struct {
	Supported func() bool
	Installed func(dataDir string) bool
	Install   func(dataDir string) (string, error)
	Uninstall func(dataDir string) (bool, error)
	Current   func() string
	// RemoveLegacy deletes the helper app from before the project rename.
	RemoveLegacy func(dataDir string) bool
}

// Deps are the outside world; tests replace them.
type Deps struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	NewHTTP        func(httpx.Recorder) (httpx.Client, error)
	OpenBrowser    func(url, browser string) error
	NewClipboard   func() (callback.Source, bool)
	Handler        HandlerOps
	Clock          callback.Clock
	Version        string
}

func DefaultDeps(version string) Deps {
	return Deps{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		NewHTTP: func(r httpx.Recorder) (httpx.Client, error) {
			return httpx.New(r)
		},
		OpenBrowser: OpenBrowser,
		NewClipboard: func() (callback.Source, bool) {
			c, ok := callback.NewClipboard()
			return c, ok
		},
		Handler: HandlerOps{
			Supported:    urlhandler.Supported,
			Installed:    urlhandler.Installed,
			Install:      urlhandler.Install,
			Uninstall:    urlhandler.Uninstall,
			Current:      urlhandler.CurrentHandler,
			RemoveLegacy: urlhandler.RemoveLegacy,
		},
		Version: version,
	}
}

type app struct {
	deps     Deps
	dataDir  string
	verbose  bool
	debug    bool
	debugDir string
	store    *store.Store
	http     httpx.Client
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string, deps Deps) int {
	a := &app{deps: deps}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return 0
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		if exit.Msg != "" {
			fmt.Fprintln(deps.Stderr, exit.Msg)
		}
		return exit.Code
	}
	fmt.Fprintln(deps.Stderr, "Error:", err)
	return 1
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "tesla-order-cli",
		Short:         "Show your Tesla order details and what changed since the last check.",
		Version:       a.deps.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			level := slog.LevelInfo
			if a.verbose || a.debug {
				level = slog.LevelDebug
			}
			slog.SetDefault(slog.New(newLogHandler(a.deps.Stderr, level)))
			if a.dataDir == "" {
				a.dataDir = store.DefaultDir()
				if os.Getenv(store.EnvDataDir) == "" {
					a.migrateLegacyData()
				}
			}
			a.store = store.New(a.dataDir)
			return nil
		},
	}
	f := root.PersistentFlags()
	f.StringVar(&a.dataDir, "data-dir", "", "where tokens and order snapshots are stored (default: $TESLA_ORDER_CLI_HOME or ~/.tesla-order-cli)")
	f.BoolVarP(&a.verbose, "verbose", "v", false, "debug logging")
	f.BoolVar(&a.debug, "debug", false, "dump every API request/response (secrets redacted) to stderr and <data dir>/debug/<timestamp>/; implies --verbose")
	f.StringVar(&a.debugDir, "debug-dir", "", "where --debug writes its dumps (default: <data dir>/debug)")

	root.AddCommand(a.loginCmd(), a.logoutCmd(), a.checkCmd(), a.showCmd(), a.dumpCmd(), a.urlHandlerCmd())
	return root
}

// migrateLegacyData moves data from before the rename to tesla-order-cli.
func (a *app) migrateLegacyData() {
	old, err := store.MigrateLegacyDir(a.dataDir)
	if err != nil {
		slog.Warn("could not move old data folder", "err", err)
		return
	}
	if old == "" {
		return
	}
	slog.Info("Moved your data from " + old + " to " + a.dataDir + ".")
	if a.deps.Handler.RemoveLegacy != nil && a.deps.Handler.RemoveLegacy(a.dataDir) {
		slog.Info("Removed the old tesla:// helper; `login` sets up the new one when needed.")
	}
}

// client creates the HTTP client once per run; in --debug mode it records.
func (a *app) client() (httpx.Client, error) {
	if a.http != nil {
		return a.http, nil
	}
	var rec httpx.Recorder
	if a.debug {
		base := a.debugDir
		if base == "" {
			base = filepath.Join(a.dataDir, "debug")
		}
		r, err := debug.ForRun(base, a.deps.Stderr)
		if err != nil {
			return nil, err
		}
		slog.Info("debug mode: dumping API traffic to " + r.Dir)
		rec = r
	}
	c, err := a.deps.NewHTTP(rec)
	if err != nil {
		return nil, err
	}
	a.http = c
	return c, nil
}

// login ----------------------------------------------------------------------

func (a *app) loginCmd() *cobra.Command {
	var manual, noHandler bool
	var browser string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to your Tesla account (once)",
		Long: "Log in to your Tesla account.\n\nOn macOS the login completes automatically: a small helper app handles the\n" +
			"tesla:// redirect. On Linux and Windows you paste the redirect URL yourself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pkce := auth.NewPKCE()
			loginURL := pkce.AuthorizeURL()
			var code string
			var err error
			if manual || !a.deps.Handler.Supported() {
				code, err = a.pasteLogin(loginURL, browser, pkce.State)
			} else {
				code, err = a.browserLogin(loginURL, browser, pkce.State, noHandler, timeout)
			}
			if err != nil {
				return &ExitError{Code: tracker.ExitError, Msg: "Login failed: " + err.Error()}
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			tokens, err := auth.Client{HTTP: c, Now: a.now}.ExchangeCode(code, pkce)
			if err != nil {
				return &ExitError{Code: tracker.ExitError, Msg: "Login failed: " + err.Error()}
			}
			if err := a.store.SaveTokens(tokens); err != nil {
				return err
			}
			fmt.Fprintln(a.deps.Stdout, "Logged in. Tokens saved to "+a.store.TokensPath())
			return nil
		},
	}
	cmd.Flags().BoolVar(&manual, "manual", false, "paste the tesla:// URL into the terminal yourself (always the case on Linux and Windows)")
	cmd.Flags().BoolVar(&noHandler, "no-handler", false, "don't register the tesla:// helper app; only watch the clipboard (macOS)")
	cmd.Flags().StringVar(&browser, "browser", "", "browser to log in with instead of the default, e.g. firefox, safari, chrome")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait for the login")
	return cmd
}

func (a *app) browserLogin(loginURL, browser, state string, noHandler bool, timeout time.Duration) (string, error) {
	var sources []callback.Source
	if !noHandler {
		installed := a.deps.Handler.Installed(a.dataDir)
		if !installed {
			fmt.Fprintln(a.deps.Stdout, "Registering a small helper app for tesla:// links (one time)...")
			if _, err := a.deps.Handler.Install(a.dataDir); err != nil {
				fmt.Fprintln(a.deps.Stderr, "Could not register the tesla:// helper:", err)
			} else {
				installed = true // don't re-check: macOS may report the new default with a delay
			}
		}
		if installed {
			inbox := callback.FileInbox{Path: urlhandler.InboxPath(a.dataDir)}
			inbox.Clear()
			sources = append(sources, inbox)
		}
	}
	if clip, ok := a.deps.NewClipboard(); ok {
		sources = append(sources, clip)
	}
	if err := a.deps.OpenBrowser(loginURL, browser); err != nil {
		return "", err
	}
	out := a.deps.Stdout
	fmt.Fprintln(out, "Log in with your Tesla account in the browser window that just opened.")
	if len(sources) > 1 {
		fmt.Fprintln(out, "When the browser asks to open 'TeslaOrderCLICallback', allow it.\nThe login then completes here automatically.")
	}
	fmt.Fprintf(out, "\nIf nothing happens after logging in: open the Developer Tools\n"+
		"(Cmd+Option+I), find the 'tesla://auth/callback?code=...' URL in the Console or\n"+
		"Network tab and copy it. It is picked up from the clipboard, no need to paste.\n"+
		"\nLogin page: %s\nWaiting... (Ctrl+C to cancel)\n", loginURL)
	return callback.WaitForCode(sources, state, timeout, a.deps.Clock)
}

func (a *app) pasteLogin(loginURL, browser, state string) (string, error) {
	out := a.deps.Stdout
	fmt.Fprintf(out, "Opening the Tesla login page in your browser:\n%s\n\n", loginURL)
	if err := a.deps.OpenBrowser(loginURL, browser); err != nil {
		fmt.Fprintln(a.deps.Stderr, "Could not open a browser:", err, "- open the URL above yourself.")
	}
	fmt.Fprint(out, "After logging in, the browser tries to open a 'tesla://auth/callback?code=...' URL\n"+
		"and fails. Open the Developer Tools (F12 / Cmd+Option+I), find that URL in the\n"+
		"Console or Network tab, copy it and paste it here.\nCallback URL: ")
	line, err := bufio.NewReader(a.deps.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no URL entered")
	}
	return auth.ParseCallback(line, state)
}

var browserApps = map[string]string{
	"firefox": "Firefox", "safari": "Safari", "chrome": "Google Chrome", "chromium": "Chromium",
	"edge": "Microsoft Edge", "brave": "Brave Browser", "arc": "Arc", "opera": "Opera",
}

// OpenBrowser opens url in the default browser, or in the named one.
func OpenBrowser(url, browser string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if browser == "" {
			cmd = exec.Command("open", url)
		} else {
			app := browserApps[strings.ToLower(browser)]
			if app == "" {
				app = browser
			}
			cmd = exec.Command("open", "-a", app, url)
		}
	case "windows":
		if browser == "" {
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		} else {
			cmd = exec.Command(browser, url)
		}
	default:
		if browser == "" {
			cmd = exec.Command("xdg-open", url)
		} else {
			cmd = exec.Command(browser, url)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		name := browser
		if name == "" {
			name = "the default browser"
		}
		return fmt.Errorf("could not open %s: %s", name, strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return nil
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete the stored tokens",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			deleted, err := a.store.DeleteTokens()
			if err != nil {
				return err
			}
			if deleted {
				fmt.Fprintln(a.deps.Stdout, "Tokens deleted.")
			} else {
				fmt.Fprintln(a.deps.Stdout, "No stored tokens.")
			}
			return nil
		},
	}
}

// check / show / dump ----------------------------------------------------------

type formatFlags struct {
	format string
	all    bool
}

func addFormatFlags(cmd *cobra.Command, f *formatFlags) {
	cmd.Flags().StringVarP(&f.format, "format", "f", "pretty", "output format: "+strings.Join(render.Formats, ", "))
	cmd.Flags().BoolVar(&f.all, "all", false, "also list every raw data field (everything except UI text)")
}

func (f formatFlags) validate() error {
	if !slices.Contains(render.Formats, f.format) {
		return &ExitError{Code: 1, Msg: fmt.Sprintf("unknown format %q (use %s)", f.format, strings.Join(render.Formats, ", "))}
	}
	return nil
}

func (a *app) checkCmd() *cobra.Command {
	var ff formatFlags
	var quiet bool
	var country string
	var ignore []string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Fetch orders, show them and what changed since the last check",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := ff.validate(); err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			res := tracker.Run(a.store, c, tracker.Options{Country: country, Ignore: ignore, Now: a.now})
			switch res.ExitCode {
			case tracker.ExitLoginRequired:
				return &ExitError{Code: res.ExitCode, Msg: res.Err.Error() + " Run `tesla-order-cli login` first."}
			case tracker.ExitError:
				return &ExitError{Code: res.ExitCode, Msg: res.Err.Error()}
			}
			if !quiet {
				doc := render.Build(res.Orders, "", ff.all)
				doc.Timelines = res.Timeline
				if err := render.Render(a.deps.Stdout, doc, ff.format); err != nil {
					return err
				}
			}
			switch {
			case res.FirstRun:
				slog.Info("Saved a first snapshot; later checks show what changed.")
			case len(res.Changes) > 0:
				fmt.Fprintf(a.deps.Stdout, "\nChanges since the last check:\n\n%s\n", tracker.FormatChanges(res.Changes))
			default:
				slog.Info("No changes since the last check.")
			}
			return nil
		},
	}
	addFormatFlags(cmd, &ff)
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "only print what changed")
	cmd.Flags().StringVar(&country, "country", "DE", "country code for order details")
	cmd.Flags().StringArrayVar(&ignore, "ignore", nil, "ignore changes at matching paths, e.g. '*.lastUpdated' (repeatable)")
	return cmd
}

func (a *app) showCmd() *cobra.Command {
	var ff formatFlags
	var input string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the last saved snapshot (no network)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := ff.validate(); err != nil {
				return err
			}
			var data map[string]any
			var timelines timeline.Timelines
			if input != "" {
				raw, err := os.ReadFile(input)
				if err == nil {
					err = json.Unmarshal(raw, &data)
				}
				if err != nil {
					return &ExitError{Code: 1, Msg: fmt.Sprintf("Cannot read %s: %v", input, err)}
				}
			} else {
				raw, err := os.ReadFile(a.store.SnapshotPath())
				if err != nil || json.Unmarshal(raw, &data) != nil {
					return &ExitError{Code: 1, Msg: "No snapshot yet. Run `tesla-order-cli check`."}
				}
				timelines = a.store.LoadTimeline()
			}
			fetched, _ := data["fetched_at"].(string)
			doc := render.Build(OrdersFromFile(data), fetched, ff.all)
			doc.Timelines = timelines
			return render.Render(a.deps.Stdout, doc, ff.format)
		},
	}
	cmd.Flags().StringVarP(&input, "input", "i", "", "show this file instead: a snapshot or a `dump` output file")
	addFormatFlags(cmd, &ff)
	return cmd
}

// OrdersFromFile accepts both the snapshot format and `dump` output.
func OrdersFromFile(data map[string]any) map[string]any {
	switch orders := data["orders"].(type) {
	case map[string]any: // snapshot: {ref: {"order": ..., "details": ...}}
		return orders
	case []any: // dump: orders list + details by reference number
		details, _ := data["details"].(map[string]any)
		out := map[string]any{}
		for _, o := range orders {
			order, _ := o.(map[string]any)
			ref, _ := order["referenceNumber"].(string)
			out[ref] = map[string]any{"order": order, "details": details[ref]}
		}
		return out
	}
	return map[string]any{}
}

func (a *app) dumpCmd() *cobra.Command {
	var country, output, format string
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Print the raw API data (orders, details, token claims); saves nothing",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if format != "json" && format != "yaml" {
				return &ExitError{Code: 1, Msg: "dump supports --format json or yaml"}
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			tokens, err := tracker.ValidTokens(a.store, auth.Client{HTTP: c, Now: a.now}, a.now())
			if err != nil {
				return fetchError(err)
			}
			client := api.API{HTTP: c, Token: tokens.AccessToken, Country: country}
			orders, err := client.ListOrders()
			if err != nil {
				return fetchError(err)
			}
			details := map[string]any{}
			for _, o := range orders {
				ref, _ := o["referenceNumber"].(string)
				if details[ref], err = client.OrderDetails(ref); err != nil {
					return fetchError(err)
				}
			}
			data := map[string]any{
				"fetched_at": a.now().UTC().Format(time.RFC3339Nano),
				"token": map[string]any{
					"expires_at": time.Unix(int64(tokens.ExpiresAt), 0).UTC().Format(time.RFC3339),
					"claims":     debug.Redact(auth.JWTClaims(tokens.AccessToken)),
				},
				"orders":  orders,
				"details": details,
			}
			var text []byte
			if format == "yaml" {
				text, err = yaml.Marshal(data)
			} else {
				text, err = ordered.MarshalIndent(data)
			}
			if err != nil {
				return err
			}
			if output == "" {
				_, err = a.deps.Stdout.Write(text)
				return err
			}
			if err := os.WriteFile(output, text, 0o600); err != nil {
				return err
			}
			fmt.Fprintln(a.deps.Stderr, "Wrote "+output)
			return nil
		},
	}
	cmd.Flags().StringVar(&country, "country", "DE", "country code")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of stdout")
	cmd.Flags().StringVarP(&format, "format", "f", "json", "output format: json or yaml")
	return cmd
}

func fetchError(err error) error {
	var login *auth.ErrLoginRequired
	if errors.As(err, &login) {
		return &ExitError{Code: tracker.ExitLoginRequired, Msg: err.Error() + " Run `tesla-order-cli login`."}
	}
	return &ExitError{Code: tracker.ExitError, Msg: "API request failed: " + err.Error()}
}

// url-handler ------------------------------------------------------------------

func (a *app) urlHandlerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "url-handler", Short: "Manage the macOS tesla:// helper app"}
	check := func() error {
		if !a.deps.Handler.Supported() {
			return &ExitError{Code: 1, Msg: urlhandler.ErrUnsupported.Error()}
		}
		return nil
	}
	cmd.AddCommand(
		&cobra.Command{Use: "install", Short: "Register the helper (login does this for you)", Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				if err := check(); err != nil {
					return err
				}
				path, err := a.deps.Handler.Install(a.dataDir)
				if err != nil {
					return &ExitError{Code: 1, Msg: err.Error()}
				}
				fmt.Fprintln(a.deps.Stdout, "Installed "+path)
				return nil
			}},
		&cobra.Command{Use: "uninstall", Short: "Remove the helper", Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				if err := check(); err != nil {
					return err
				}
				removed, err := a.deps.Handler.Uninstall(a.dataDir)
				if err != nil {
					return err
				}
				if removed {
					fmt.Fprintln(a.deps.Stdout, "Helper removed.")
				} else {
					fmt.Fprintln(a.deps.Stdout, "Helper not installed.")
				}
				return nil
			}},
		&cobra.Command{Use: "status", Short: "Show which app handles tesla:// links", Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				if err := check(); err != nil {
					return err
				}
				current := a.deps.Handler.Current()
				if current == "" {
					current = "none"
				}
				installed := "no"
				if a.deps.Handler.Installed(a.dataDir) {
					installed = "yes"
				}
				fmt.Fprintf(a.deps.Stdout, "Default tesla:// handler: %s\nHelper installed: %s\n", current, installed)
				return nil
			}},
	)
	return cmd
}

func (a *app) now() time.Time {
	if a.deps.Clock.Now != nil {
		return a.deps.Clock.Now()
	}
	return time.Now()
}
