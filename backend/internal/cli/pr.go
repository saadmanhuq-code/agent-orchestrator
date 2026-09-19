package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type prMergeOptions struct {
	project         string
	expectedHeadSHA string
}

// mergePRRequest mirrors the daemon's MergePRRequest body for
// POST /api/v1/prs/{id}/merge. Both fields are required by the daemon: prUrl
// pins the exact repository (a bare PR number is ambiguous across
// registered projects), and expectedHeadSha pins the exact commit so the
// daemon refuses the merge if the PR moved after the caller identified it.
type mergePRRequest struct {
	PRURL           string `json:"prUrl"`
	ExpectedHeadSHA string `json:"expectedHeadSha"`
}

type mergePRResponse struct {
	OK       bool   `json:"ok"`
	PRNumber int    `json:"prNumber"`
	Method   string `json:"method"`
}

type resolveCommentsRequest struct {
	CommentIDs []string `json:"commentIds,omitempty"`
}

type resolveCommentsResponse struct {
	OK       bool `json:"ok"`
	Resolved int  `json:"resolved"`
}

func newPRCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Manage pull requests",
	}
	cmd.AddCommand(newPRMergeCommand(ctx))
	cmd.AddCommand(newPRResolveCommentsCommand(ctx))
	return cmd
}

func newPRMergeCommand(ctx *commandContext) *cobra.Command {
	var opts prMergeOptions
	cmd := &cobra.Command{
		Use:   "merge <pr-ref>",
		Short: "Merge a pull request",
		Long: "Merge a pull request. <pr-ref> is a PR/MR number, resolved against the " +
			"identified project's repository, or a full PR/MR URL, which is unambiguous " +
			"on its own and needs no project. --expected-head-sha is required: the " +
			"daemon refuses the merge if the PR's head no longer matches it.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.mergePR(cmd.Context(), cmd, args[0], opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to resolve a bare PR number (default: AO_PROJECT_ID, AO_SESSION_ID's project, or the current registered repo)")
	cmd.Flags().StringVar(&opts.expectedHeadSHA, "expected-head-sha", "", "Commit SHA the PR must currently be at; the merge is refused if the head has moved (required)")
	return cmd
}

func (c *commandContext) mergePR(ctx context.Context, cmd *cobra.Command, ref string, opts prMergeOptions) error {
	expectedHeadSHA := strings.TrimSpace(opts.expectedHeadSHA)
	if expectedHeadSHA == "" {
		return usageError{errors.New("--expected-head-sha is required (the commit the PR must currently be at)")}
	}
	var project projectDetails
	if isNumericPRRef(ref) {
		// A bare number is only safe to resolve against a single, identified
		// project's repository. Never guess across registered projects: fail
		// closed instead of picking one at random when several could match.
		var err error
		project, err = c.resolveSpawnProject(ctx, opts.project)
		if err != nil {
			return err
		}
	}
	prURL, err := c.resolvePRRef(ctx, ref, project)
	if err != nil {
		return err
	}
	_, _, _, prNumber, err := cliParsePRURL(prURL)
	if err != nil || prNumber <= 0 {
		return usageError{errors.New("PR reference must be a PR/MR URL or a number")}
	}
	var res mergePRResponse
	req := mergePRRequest{PRURL: prURL, ExpectedHeadSHA: expectedHeadSHA}
	if err := c.postJSON(ctx, "prs/"+url.PathEscape(strconv.Itoa(prNumber))+"/merge", req, &res); err != nil {
		return err
	}
	if method := strings.TrimSpace(res.Method); method != "" {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "merged PR #%d using %s\n", res.PRNumber, method)
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "merged PR #%d\n", res.PRNumber)
	return err
}

func newPRResolveCommentsCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "resolve-comments <pr-number> [comment-id...]",
		Short: "Resolve review threads on a pull request",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNumber, err := normalizePRNumber(args[0])
			if err != nil {
				return err
			}
			commentIDs := make([]string, 0, len(args)-1)
			for _, id := range args[1:] {
				id = strings.TrimSpace(id)
				if id == "" {
					return usageError{errors.New("comment id must not be blank")}
				}
				commentIDs = append(commentIDs, id)
			}
			var res resolveCommentsResponse
			if err := ctx.postJSON(
				cmd.Context(),
				"prs/"+url.PathEscape(prNumber)+"/resolve-comments",
				resolveCommentsRequest{CommentIDs: commentIDs},
				&res,
			); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "resolved %d review thread(s) on PR #%s\n", res.Resolved, prNumber)
			return err
		},
	}
}

func normalizePRNumber(raw string) (string, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return "", usageError{errors.New("PR number must be a positive integer")}
	}
	return strconv.Itoa(n), nil
}
