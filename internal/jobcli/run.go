// Package jobcli implements the job-management commands of the wire binary.
package jobcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

const maxBody = 4 << 20

// Run executes a management command and writes the coordinator's JSON response.
// It never retries mutations or follows redirects to another coordinator.
func Run(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := pflag.NewFlagSet("wire jobs", pflag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(errOut, "Usage: wire jobs list|get|submit|cancel|pause|resume [job-id] [flags]")
		_, _ = fmt.Fprintln(errOut, "       wire savepoints list|get|trigger|delete job-id [savepoint-id] [flags]")
		_, _ = fmt.Fprintln(errOut, "       wire cluster status|remove [node-id] [flags]")
		flags.PrintDefaults()
	}
	endpoint := flags.String("coordinator", "http://localhost:4001", "coordinator HTTP URL")
	timeout := flags.Duration("timeout", 30*time.Second, "request timeout")
	file := flags.String("file", "", "submission JSON file")
	status := flags.String("status", "", "job-list status filter")
	savepoint := new(bool)
	var restorePath string
	submissionCommand := len(args) >= 2 && args[0] == "jobs" && args[1] == "submit"
	if submissionCommand {
		flags.StringVar(&restorePath, "savepoint", "", "restore submission from a completed savepoint path")
	} else {
		flags.BoolVar(savepoint, "savepoint", false, "take a completed savepoint before canceling the job")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return err
	}
	words := flags.Args()
	if len(words) < 2 {
		return fmt.Errorf("usage: wire jobs list|get|submit|cancel|pause|resume; wire savepoints list|get|trigger|delete; wire cluster status|remove")
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	base, err := url.Parse(*endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("invalid coordinator URL")
	}
	method, path, want := http.MethodGet, "", 2
	switch words[0] + " " + words[1] {
	case "jobs list":
		path = "/api/v1/jobs"
	case "jobs submit":
		method = http.MethodPost
		path = "/api/v1/jobs"
	case "jobs get":
		want = 3
	case "jobs cancel", "jobs pause", "jobs resume":
		method = http.MethodPost
		want = 3
	case "savepoints list":
		want = 3
	case "savepoints trigger":
		method = http.MethodPost
		want = 3
	case "savepoints get":
		want = 4
	case "savepoints delete":
		method = http.MethodDelete
		want = 4
	case "cluster status":
		path = "/api/v1/cluster"
	case "cluster remove":
		method = http.MethodDelete
		want = 3
	default:
		return fmt.Errorf("unknown management command %q", strings.Join(words[:2], " "))
	}
	if len(words) != want {
		return fmt.Errorf("command requires %d positional arguments", want)
	}
	for _, id := range words[2:] {
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
			return fmt.Errorf("invalid identifier")
		}
	}
	if words[0] == "cluster" && words[1] == "remove" {
		path = "/api/v1/cluster/nodes/" + url.PathEscape(words[2])
	} else if want >= 3 {
		path = "/api/v1/jobs/" + url.PathEscape(words[2])
		if words[0] == "savepoints" {
			path += "/savepoints"
			if want == 4 {
				path += "/" + url.PathEscape(words[3])
			}
		} else if words[1] != "get" {
			path += "/" + words[1]
		}
	}
	if *file != "" && (words[0] != "jobs" || words[1] != "submit") {
		return fmt.Errorf("--file is only valid for jobs submit")
	}
	if flags.Changed("savepoint") && !submissionCommand && (words[0] != "jobs" || words[1] != "cancel") {
		return fmt.Errorf("--savepoint is only valid for jobs cancel")
	}
	var body []byte
	if words[0] == "jobs" && words[1] == "submit" {
		if *file == "" {
			return fmt.Errorf("jobs submit requires --file with a REST submission JSON body")
		}
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err = io.ReadAll(io.LimitReader(f, maxBody+1))
		if err != nil {
			return err
		}
		if len(body) > maxBody || !json.Valid(body) {
			return fmt.Errorf("submission must be valid JSON at most 4 MiB")
		}
	}
	if submissionCommand && flags.Changed("savepoint") {
		if restorePath == "" {
			return fmt.Errorf("--savepoint requires a nonempty restore path")
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
			return fmt.Errorf("submission must be a JSON object")
		}
		payload["savepoint"], _ = json.Marshal(restorePath)
		body, err = json.Marshal(payload)
		if err != nil || len(body) > maxBody {
			return fmt.Errorf("submission exceeds 4 MiB after adding savepoint")
		}
	}
	target := strings.TrimRight(base.String(), "/") + path
	if *status != "" {
		if words[0] != "jobs" || words[1] != "list" {
			return fmt.Errorf("--status is only valid for jobs list")
		}
		target += "?" + url.Values{"status": {*status}}.Encode()
	}
	if *savepoint {
		target += "?savepoint=true"
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return fmt.Errorf("response exceeds 4 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coordinator HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if len(data) == 0 {
		return nil
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return fmt.Errorf("coordinator returned invalid JSON: %w", err)
	}
	pretty.WriteByte('\n')
	_, err = out.Write(pretty.Bytes())
	return err
}
