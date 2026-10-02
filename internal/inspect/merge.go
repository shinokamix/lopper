package inspect

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	for _, commit := range commits {
		parents := len(strings.Fields(commit)) - 1
		if parents <= 0 {
			return lopper.NotMerged, nil
		}
	}
	local, err := in.nonemptyCommits(ctx, dir, base+".."+head)
	if err != nil {
		return lopper.NotMerged, err
	}
	// Keep empty commits, including merges with no changes against their first
	// parent, even when the combined diff still matches an earlier squash.
	if local != len(commits) {
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
	out, err := in.Git.Run(ctx, dir, "log", "--diff-merges=first-parent", "--no-patch", "--no-show-signature", "--format=%H",
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
	changes := make([][]string, 0, len(entries)/2)
	paths := make([]string, 0, len(entries)/2)
	for i := 0; i < len(entries); i += 2 {
		change := strings.Fields(strings.TrimPrefix(entries[i], ":"))
		if len(change) != 5 {
			return lopper.NotMerged, fmt.Errorf("invalid raw diff entry: %q", entries[i])
		}
		changes = append(changes, change)
		paths = append(paths, entries[i+1])
	}
	tree, err := in.treeEntries(ctx, dir, base, paths)
	if err != nil {
		return lopper.NotMerged, err
	}
	var merges [][3]string // blobs of base, ancestor and head
	for i, change := range changes {
		current, ok := tree[paths[i]]
		if change[4] == "D" {
			if ok {
				return lopper.NotMerged, nil
			}
			continue
		}
		if !ok {
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
		merges = append(merges, [3]string{current[2], change[2], change[3]})
	}
	// Blobs are read in batches, which bounds the memory they take.
	for batch := range slices.Chunk(merges, 64) {
		var ids []string
		for _, m := range batch {
			ids = append(ids, m[:]...)
		}
		blobs, err := gitx.ReadBlobs(ctx, in.Git, dir, ids)
		if err != nil {
			return lopper.NotMerged, err
		}
		for _, m := range batch {
			unchanged, err := gitx.MergeUnchanged(ctx, in.Git, dir, blobs[m[0]], blobs[m[1]], blobs[m[2]])
			if err != nil {
				return lopper.NotMerged, err
			}
			if !unchanged {
				return lopper.NotMerged, nil
			}
		}
	}
	return lopper.MergedSquash, nil
}

// treeEntries returns the mode, type and object of each of paths that
// tree has, a subtree included. Paths go to ls-tree in batches that fit
// a command line; -t keeps a tree listed when another path leads into it.
func (in Inspector) treeEntries(ctx context.Context, dir, tree string, paths []string) (map[string][]string, error) {
	entries := make(map[string][]string, len(paths))
	for len(paths) > 0 {
		n, size := 0, 0
		for n < len(paths) && (n == 0 || size+len(paths[n]) < 16<<10) {
			size += len(paths[n]) + 1
			n++
		}
		args := append([]string{"--literal-pathspecs", "ls-tree", "-t", "-z", tree, "--"}, paths[:n]...)
		out, err := in.Git.Run(ctx, dir, args...)
		if err != nil {
			return nil, err
		}
		for record := range strings.SplitSeq(out, "\x00") {
			if record == "" {
				continue
			}
			header, path, ok := strings.Cut(record, "\t")
			fields := strings.Fields(header)
			if !ok || len(fields) != 3 {
				return nil, fmt.Errorf("invalid ls-tree entry: %q", record)
			}
			entries[path] = fields
		}
		paths = paths[n:]
	}
	return entries, nil
}

func regularMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}
