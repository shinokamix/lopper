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
	out, err = in.Git.Run(ctx, dir, "merge-base", "--all", base, head)
	if err != nil {
		return lopper.NotMerged, err
	}
	bases := strings.Fields(out)
	if len(bases) != 1 {
		return lopper.NotMerged, nil
	}
	// Most branches are not merged, and the content check usually tells so
	// before reading any blob; diffing every local commit comes after it.
	kind, err := in.contentMerge(ctx, dir, bases[0], head, base)
	if err != nil || kind == lopper.NotMerged {
		return kind, err
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
	return kind, nil
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

// The limits below bound what one squash check reads.
const (
	// maxMergeSize is the largest file merged as text; a larger changed
	// file counts as not merged.
	maxMergeSize = 8 << 20
	// maxReadSize is the most blob data one cat-file reads into memory,
	// enough for the three versions of the largest file.
	maxReadSize = 3 * maxMergeSize
	// maxArgsSize keeps the paths given to one ls-tree within command
	// line limits.
	maxArgsSize = 16 << 10
)

// rawChange is one file of `git diff --raw`.
type rawChange struct {
	srcMode, dstMode, srcID, dstID, status, path string
}

// treeEntry is one object of `git ls-tree`.
type treeEntry struct{ mode, id string }

// textMerge names the blobs of one file that replaying ancestor..other
// onto current must leave unchanged.
type textMerge struct{ current, ancestor, other string }

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
	changes := make([]rawChange, 0, len(entries)/2)
	paths := make([]string, 0, len(entries)/2)
	for i := 0; i < len(entries); i += 2 {
		f := strings.Fields(strings.TrimPrefix(entries[i], ":"))
		if len(f) != 5 {
			return lopper.NotMerged, fmt.Errorf("invalid raw diff entry: %q", entries[i])
		}
		changes = append(changes, rawChange{f[0], f[1], f[2], f[3], f[4], entries[i+1]})
		paths = append(paths, entries[i+1])
	}
	tree, err := in.treeEntries(ctx, dir, base, paths)
	if err != nil {
		return lopper.NotMerged, err
	}
	var merges []textMerge
	for _, ch := range changes {
		cur, ok := tree[ch.path]
		if ch.status == "D" {
			if ok {
				return lopper.NotMerged, nil
			}
			continue
		}
		if !ok {
			return lopper.NotMerged, nil
		}
		if ch.srcMode != ch.dstMode && cur.mode != ch.dstMode {
			return lopper.NotMerged, nil
		}
		if cur.mode != ch.dstMode && (!regularMode(cur.mode) || !regularMode(ch.dstMode)) {
			return lopper.NotMerged, nil
		}
		if cur.id == ch.dstID {
			continue
		}
		// Base still has the file as the branch started from: the change
		// is not in base, and a merge would only repeat that.
		if cur.id == ch.srcID {
			return lopper.NotMerged, nil
		}
		if !regularMode(ch.srcMode) || !regularMode(ch.dstMode) || !regularMode(cur.mode) {
			return lopper.NotMerged, nil
		}
		merges = append(merges, textMerge{cur.id, ch.srcID, ch.dstID})
	}
	if len(merges) == 0 {
		return lopper.MergedSquash, nil
	}
	ids := make([]string, 0, 3*len(merges))
	for _, m := range merges {
		ids = append(ids, m.current, m.ancestor, m.other)
	}
	sizes, err := gitx.BlobSizes(ctx, in.Git, dir, ids)
	if err != nil {
		return lopper.NotMerged, err
	}
	if slices.ContainsFunc(ids, func(id string) bool { return sizes[id] > maxMergeSize }) {
		return lopper.NotMerged, nil
	}
	// Blobs are read in batches, which bounds the memory they take.
	for len(merges) > 0 {
		n, size := 0, 0
		for n < len(merges) {
			m := merges[n]
			next := sizes[m.current] + sizes[m.ancestor] + sizes[m.other]
			if n > 0 && size+next > maxReadSize {
				break
			}
			size += next
			n++
		}
		blobs, err := gitx.ReadBlobs(ctx, in.Git, dir, ids[:3*n])
		if err != nil {
			return lopper.NotMerged, err
		}
		for _, m := range merges[:n] {
			unchanged, err := gitx.MergeUnchanged(ctx, in.Git, dir, blobs[m.current], blobs[m.ancestor], blobs[m.other])
			if err != nil {
				return lopper.NotMerged, err
			}
			if !unchanged {
				return lopper.NotMerged, nil
			}
		}
		merges, ids = merges[n:], ids[3*n:]
	}
	return lopper.MergedSquash, nil
}

// treeEntries returns the entry of each of paths that tree has, a
// subtree included. Paths go to ls-tree in batches that fit a command
// line; -t keeps a tree listed when another path leads into it.
func (in Inspector) treeEntries(ctx context.Context, dir, tree string, paths []string) (map[string]treeEntry, error) {
	entries := make(map[string]treeEntry, len(paths))
	for len(paths) > 0 {
		n, size := 0, 0
		for n < len(paths) && (n == 0 || size+len(paths[n]) < maxArgsSize) {
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
			fields := strings.Fields(header) // mode, type, object
			if !ok || len(fields) != 3 {
				return nil, fmt.Errorf("invalid ls-tree entry: %q", record)
			}
			entries[path] = treeEntry{mode: fields[0], id: fields[2]}
		}
		paths = paths[n:]
	}
	return entries, nil
}

func regularMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}
