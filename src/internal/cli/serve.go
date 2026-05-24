package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		reportPath string
		addr       string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the last HTML report from a local HTTP server",
		Long: `Start a local HTTP server that serves an HTML review report. The
report must be produced first by 'accurate-reviewer review --output
report.html'. The server binds to 127.0.0.1 only — never to a public
interface — because the report can carry sensitive details (file paths,
sometimes snippets of source under review) that must stay on the
developer's machine.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := reportPath
			if path == "" {
				path = "report.html"
			}
			if err := validateServePath(path); err != nil {
				return Exit(2, "invalid --report: %v", err)
			}

			// Resolve the bind address BEFORE checking the report file:
			// "refusing to bind to a non-loopback interface" is a misuse
			// error (exit 2) that should win over "report missing" (exit
			// 1), which only requires running the review first.
			//
			// CWE-200: leaking findings over the network would defeat the
			// "stays on your machine" promise the CLI/LLM-via-subprocess
			// architecture rests on, so we whitelist the host portion of
			// the address against the exact set of loopback identifiers.
			listenAddr, err := resolveLoopbackAddr(addr)
			if err != nil {
				return Exit(2, "invalid --addr: %v", err)
			}

			if _, err := os.Stat(path); err != nil {
				return Exit(1, "report not found: %s (run `accurate-reviewer review --output %s` first)", path, path)
			}

			ln, err := net.Listen("tcp", listenAddr)
			if err != nil {
				return Exit(1, "listen: %v", err)
			}

			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				// Only ever serve the single report file — we are not a
				// general-purpose file server, and refusing other paths
				// removes any LFI risk from an attacker who can reach the
				// loopback (e.g. via a malicious browser tab).
				if r.URL.Path != "/" && r.URL.Path != "/report" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				// Defence-in-depth: loopback already keeps the data on
				// this machine, but a same-machine attacker tab could
				// still pull the report into an iframe or run injected
				// script against it. CSP locks rendering to first-party
				// inline assets (the report ships its own <style>), and
				// X-Frame-Options blocks embedding.
				w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
				w.Header().Set("X-Frame-Options", "DENY")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("Referrer-Policy", "no-referrer")
				http.ServeFile(w, r, path)
			})

			srv := &http.Server{
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "[serve] http://%s/  (Ctrl-C to stop)\n", ln.Addr().String())
			fmt.Fprintf(cmd.OutOrStdout(), "serving %s at http://%s/\n", path, ln.Addr().String())

			// Graceful shutdown on Ctrl-C lets the BDD harness kill the
			// process cleanly without spurious "broken pipe" lines in the
			// transcript.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()
			select {
			case <-ctx.Done():
				shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutCtx)
				return nil
			case err := <-errCh:
				if err != nil && err != http.ErrServerClosed {
					return Exit(1, "serve: %v", err)
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVarP(&reportPath, "report", "r", "report.html", "path to the HTML report to serve")
	cmd.Flags().StringVar(&addr, "addr", "", "address to bind to (port number, or 127.0.0.1:PORT). Empty = random free port")
	return cmd
}

// validateServePath rejects --report values that point outside the current
// working directory — same reasoning as validateOutputPath in review.go.
func validateServePath(p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%q must stay within the working directory", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q must stay within the working directory", p)
	}
	return nil
}

// resolveLoopbackAddr turns the user's --addr value into a host:port we are
// willing to bind to. Only the IPv4 and IPv6 loopback literals are allowed;
// strings like "localhost", "0.0.0.0", "[::]", or a bare ":" are rejected
// because they can resolve to (or shadow) a routable interface. An empty
// input becomes "127.0.0.1:0" (random free port); a bare port number is
// prefixed with "127.0.0.1:" so `--addr 9999` keeps working.
func resolveLoopbackAddr(in string) (string, error) {
	if in == "" {
		return "127.0.0.1:0", nil
	}
	// Bare port like "9999".
	if !strings.ContainsAny(in, ":[") {
		if _, err := net.LookupPort("tcp", in); err != nil {
			return "", fmt.Errorf("%q is not a valid port", in)
		}
		return "127.0.0.1:" + in, nil
	}
	host, port, err := net.SplitHostPort(in)
	if err != nil {
		return "", fmt.Errorf("%q: %v", in, err)
	}
	switch host {
	case "127.0.0.1", "::1":
		// fine
	case "":
		return "", fmt.Errorf("refusing to bind to a non-loopback interface (host portion is empty)")
	default:
		return "", fmt.Errorf("refusing to bind to a non-loopback interface (%q is not 127.0.0.1 or ::1)", host)
	}
	if port == "" {
		port = "0"
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return "", fmt.Errorf("%q is not a valid port", port)
	}
	if host == "::1" {
		return "[::1]:" + port, nil
	}
	return host + ":" + port, nil
}
