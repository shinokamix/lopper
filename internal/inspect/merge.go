package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shinokamix/lopper/internal/gitx"
	"github.com/shinokamix/lopper/internal/lopper"
)

// mergeKind checks ancestry, then whether base contains the branch's changes.
// Comparisons use fixed commits so moving branches cannot mix histories.
func (in Inspector) mergeKind(ctx context.Context, dir, base string) (lopper.MergeKind, error) {
	out, err := in.Git.Run(ctx, dir, "rev-parse", "--revs-only", "--end-of-options", base+"^{commit}", "HEAD^{commit}")
	if err != nil {
		return lopper.NotMerged, err
	}
	refs := strings.Fields(out)
	if len(refs) != 2 {
		return lopper.NotMerged, fmt.Errorf("expected base and HEAD commits, got %q", out)
	}
	base, head := refs[0], refs[1]
	out, err = in.Git.Run(ctx, dir, "rev-list", "--parents", base+".."+head)
	if err != nil {
		return lopper.NotMerged, err
	}
	if out == "" {
		return lopper.MergedFF, nil
	}
	commits := strings.Split(out, "\n")
	ordinary := 0
	for _, commit := range commits {
		parents := len(strings.Fields(commit)) - 1
		if parents <= 0 {
			return lopper.NotMerged, nil
		}
		if parents == 1 {
			ordinary++
		}
	}
	local, err := in.nonemptyCommits(ctx, dir, base+".."+head)
	if err != nil {
		return lopper.NotMerged, err
	}
	// Do not silently drop empty commits, including a new one made after squash
	// while the combined diff still matches.
	if local != ordinary {
		return lopper.NotMerged, nil
	}
	out, err = in.Git.Run(ctx, dir, "merge-base", "--all", base, head)
	if err != nil {
		return lopper.NotMerged, err
	}
	bases := strings.Fields(out)
	if len(bases) != 1 {
		return lopper.NotMerged, nil
	}
	return in.contentMerge(ctx, dir, bases[0], head, base)
}

func (in Inspector) nonemptyCommits(ctx context.Context, dir, commits string) (int, error) {
	out, err := in.Git.Run(ctx, dir, "log", "--no-merges", "--no-show-signature", "--format=%H",
		"--diff-filter=ACDMRTUXB", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-relative",
		"--ignore-submodules=none", commits, "--")
	if err != nil {
		return 0, err
	}
	return countLines(out), nil
}

// contentMerge checks the entire branch's file changes against base.
// Replaying them must leave every file unchanged, without conflicts.
func (in Inspector) contentMerge(ctx context.Context, dir, ancestor, head, base string) (lopper.MergeKind, error) {
	out, err := in.Git.Run(ctx, dir, "diff", "--raw", "--no-abbrev", "-z", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--no-relative", "--ignore-submodules=none", ancestor, head, "--")
	if err != nil {
		return lopper.NotMerged, err
	}
	if out == "" {
		return lopper.NotMerged, nil
	}
	entries := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(entries)%2 != 0 {
		return lopper.NotMerged, errors.New("invalid raw diff output")
	}
	for i := 0; i < len(entries); i += 2 {
		change := strings.Fields(strings.TrimPrefix(entries[i], ":"))
		if len(change) != 5 {
			return lopper.NotMerged, fmt.Errorf("invalid raw diff entry: %q", entries[i])
		}
		out, err = in.Git.Run(ctx, dir, "--literal-pathspecs", "ls-tree", "-z", base, "--", entries[i+1])
		if err != nil {
			return lopper.NotMerged, err
		}
		if change[4] == "D" {
			if out != "" {
				return lopper.NotMerged, nil
			}
			continue
		}
		header, _, _ := strings.Cut(out, "\t")
		current := strings.Fields(header)
		if len(current) != 3 {
			return lopper.NotMerged, nil
		}
		if change[0] != change[1] && current[0] != change[1] {
			return lopper.NotMerged, nil
		}
		if current[0] != change[1] && (!regularMode(current[0]) || !regularMode(change[1])) {
			return lopper.NotMerged, nil
		}
		if current[2] == change[3] {
			continue
		}
		if !regularMode(change[0]) || !regularMode(change[1]) || !regularMode(current[0]) {
			return lopper.NotMerged, nil
		}
		unchanged, err := gitx.MergeBlobsUnchanged(ctx, in.Git, dir, current[2], change[2], change[3])
		if err != nil {
			return lopper.NotMerged, err
		}
		if !unchanged {
			return lopper.NotMerged, nil
		}
	}
	return lopper.MergedSquash, nil
}

func regularMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}
